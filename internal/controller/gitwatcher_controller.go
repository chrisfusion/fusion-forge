// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package controller

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/buildtrigger"
	"fusion-platform.io/fusion-forge/internal/config"
	"fusion-platform.io/fusion-forge/internal/db"
	"fusion-platform.io/fusion-forge/internal/gitutil"
	"fusion-platform.io/fusion-forge/internal/indexclient"
	"fusion-platform.io/fusion-forge/internal/validation"

	"github.com/jackc/pgx/v5"
)

// GitWatcherReconciler polls git repositories and triggers builds on version changes.
type GitWatcherReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	DB           *db.Queries
	IndexClient  *indexclient.Client
	Cfg          *config.Config
	GitRules     validation.GitRules
	PollInterval time.Duration
	MaxFailures  int
}

// +kubebuilder:rbac:groups=build.fusion-platform.io,resources=gitwatchers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=build.fusion-platform.io,resources=gitwatchers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=build.fusion-platform.io,resources=cibuilds,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get

func (r *GitWatcherReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var watcher buildv1alpha1.GitWatcher
	if err := r.Get(ctx, req.NamespacedName, &watcher); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Spec-disabled: mark Disabled and sleep long.
	if watcher.Spec.Enabled != nil && !*watcher.Spec.Enabled {
		if watcher.Status.Phase != buildv1alpha1.GitWatcherPhaseDisabled {
			base := client.MergeFrom(watcher.DeepCopy())
			watcher.Status.Phase = buildv1alpha1.GitWatcherPhaseDisabled
			watcher.Status.Message = "disabled by spec"
			_ = r.Status().Patch(ctx, &watcher, base)
		}
		return ctrl.Result{RequeueAfter: r.PollInterval * 10}, nil
	}

	// Failure-disabled: sleep long until re-enabled manually.
	if watcher.Status.Phase == buildv1alpha1.GitWatcherPhaseDisabled {
		return ctrl.Result{RequeueAfter: r.PollInterval * 10}, nil
	}

	// --- Check in-flight build ---
	if watcher.Status.LastBuildName != "" {
		done, requeueAfter, err := r.checkInFlightBuild(ctx, &watcher, req.Namespace)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !done {
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		// In-flight build completed (Succeeded or Failed).
		// If now disabled, stop.
		if watcher.Status.Phase == buildv1alpha1.GitWatcherPhaseDisabled {
			return ctrl.Result{RequeueAfter: r.PollInterval * 10}, nil
		}
		// Succeeded case returns early inside checkInFlightBuild.
		// Failed case clears LastBuildName and falls through to retry.
	}

	// --- Resolve token ---
	token, err := r.resolveToken(ctx, &watcher)
	if err != nil {
		logger.Error(err, "resolve token", "watcher", req.Name)
		base := client.MergeFrom(watcher.DeepCopy())
		now := metav1.Now()
		watcher.Status.LastCheckedAt = &now
		watcher.Status.LastError = fmt.Sprintf("token secret error: %s", err)
		_ = r.Status().Patch(ctx, &watcher, base)
		return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
	}

	// --- Poll remote HEAD ---
	repoRef := watcher.Spec.RepoRef
	if repoRef == "" {
		repoRef = "main"
	}
	head, err := gitutil.FetchRemoteHEAD(ctx, watcher.Spec.RepoURL, repoRef, token)
	if err != nil {
		logger.Error(err, "poll remote HEAD", "watcher", req.Name)
		base := client.MergeFrom(watcher.DeepCopy())
		now := metav1.Now()
		watcher.Status.LastCheckedAt = &now
		watcher.Status.LastError = fmt.Sprintf("poll failed: %s", err)
		_ = r.Status().Patch(ctx, &watcher, base)
		return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
	}

	base := client.MergeFrom(watcher.DeepCopy())
	now := metav1.Now()
	watcher.Status.LastCheckedAt = &now
	watcher.Status.LastError = ""
	watcher.Status.Phase = buildv1alpha1.GitWatcherPhaseActive

	// No new commit — update timestamp and return.
	if head == watcher.Status.LastSeenCommit {
		_ = r.Status().Patch(ctx, &watcher, base)
		return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
	}
	watcher.Status.LastSeenCommit = head

	// --- Resolve version from repo ---
	name, version, appMeta, resolveErr := r.resolveVersionAndMeta(ctx, &watcher, repoRef, token)
	if resolveErr != nil {
		logger.Error(resolveErr, "resolve version", "watcher", req.Name)
		watcher.Status.LastError = fmt.Sprintf("version resolve failed: %s", resolveErr)
		_ = r.Status().Patch(ctx, &watcher, base)
		return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
	}

	// Skip if version unchanged since last successful build.
	if version != "" && version == watcher.Status.LastBuiltVersion {
		logger.Info("version unchanged — skipping", "watcher", req.Name, "version", version)
		watcher.Status.Message = fmt.Sprintf("version %s already built — skipping", version)
		_ = r.Status().Patch(ctx, &watcher, base)
		return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
	}

	// Check DB for an existing row with the same (name, version).
	if existing, dbErr := r.DB.GetVenvBuildByNameAndVersion(ctx, name, version); dbErr == nil {
		switch existing.Status {
		case "SUCCESS":
			logger.Info("version already built in DB — skipping", "watcher", req.Name, "version", version)
			watcher.Status.LastBuiltVersion = version
			watcher.Status.ConsecutiveFailures = 0
			_ = r.Status().Patch(ctx, &watcher, base)
			return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
		case "PENDING", "BUILDING":
			logger.Info("version build already in progress in DB", "watcher", req.Name, "version", version)
			_ = r.Status().Patch(ctx, &watcher, base)
			return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
		case "FAILED":
			r.cleanupFailedRow(ctx, existing)
		}
	} else if !errors.Is(dbErr, pgx.ErrNoRows) {
		logger.Error(dbErr, "query DB for existing build", "name", name, "version", version)
		_ = r.Status().Patch(ctx, &watcher, base)
		return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
	}

	// --- Trigger build ---
	deps := buildtrigger.Deps{
		DB:          r.DB,
		K8sCRClient: r.Client,
		IndexClient: r.IndexClient,
		Cfg:         r.Cfg,
		GitRules:    r.GitRules,
	}

	var buildID int64
	var ciBuildName string
	var triggerErr error

	if watcher.Spec.BuildType == "app" {
		if appMeta == nil {
			logger.Error(nil, "appMeta is nil for app build — should not happen")
			_ = r.Status().Patch(ctx, &watcher, base)
			return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
		}
		builderImage, imgErr := r.Cfg.BuilderImageFor(appMeta.BuilderImage)
		if imgErr != nil {
			logger.Error(imgErr, "unknown builder image", "key", appMeta.BuilderImage)
			watcher.Status.LastError = imgErr.Error()
			_ = r.Status().Patch(ctx, &watcher, base)
			return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
		}
		buildID, ciBuildName, triggerErr = buildtrigger.TriggerAppBuild(ctx, deps, buildtrigger.AppBuildInput{
			RepoURL:      watcher.Spec.RepoURL,
			RepoRef:      repoRef,
			ProjectDir:   watcher.Spec.ProjectDir,
			BuilderImage: builderImage,
			Meta:         *appMeta,
		})
	} else {
		pythonVersion := watcher.Spec.PythonVersion
		if pythonVersion == "" {
			pythonVersion = "3.12"
		}
		builderImage, imgErr := r.Cfg.BuilderImageFor(pythonVersion)
		if imgErr != nil {
			logger.Error(imgErr, "unknown builder image", "key", pythonVersion)
			watcher.Status.LastError = imgErr.Error()
			_ = r.Status().Patch(ctx, &watcher, base)
			return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
		}
		buildID, ciBuildName, triggerErr = buildtrigger.TriggerGitBuild(ctx, deps, buildtrigger.GitBuildInput{
			Name:           name,
			Version:        version,
			Description:    watcher.Spec.Description,
			RepoURL:        watcher.Spec.RepoURL,
			RepoRef:        repoRef,
			MetadataSource: watcher.Spec.MetadataSource,
			EntrypointFile: watcher.Spec.EntrypointFile,
			ProjectDir:     watcher.Spec.ProjectDir,
			PythonVersion:  pythonVersion,
			BuilderImage:   builderImage,
		})
	}
	_ = buildID

	if triggerErr != nil {
		if errors.Is(triggerErr, buildtrigger.ErrConflict) {
			logger.Info("version already exists — skipping", "watcher", req.Name, "version", version)
			watcher.Status.LastBuiltVersion = version
			watcher.Status.Message = fmt.Sprintf("version %s already exists — skipped", version)
		} else {
			logger.Error(triggerErr, "trigger build", "watcher", req.Name, "version", version)
			failures := watcher.Status.ConsecutiveFailures + 1
			watcher.Status.ConsecutiveFailures = failures
			watcher.Status.LastError = fmt.Sprintf("trigger failed: %s", triggerErr)
			if failures >= r.MaxFailures {
				watcher.Status.Phase = buildv1alpha1.GitWatcherPhaseDisabled
				watcher.Status.Message = fmt.Sprintf("disabled after %d consecutive failures", failures)
			}
		}
		_ = r.Status().Patch(ctx, &watcher, base)
		return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
	}

	logger.Info("triggered build", "watcher", req.Name, "ciBuildName", ciBuildName, "version", version)
	watcher.Status.LastBuildName = ciBuildName
	watcher.Status.LastBuildVersion = version
	watcher.Status.Message = fmt.Sprintf("triggered build %s for version %s", ciBuildName, version)
	if err := r.Status().Patch(ctx, &watcher, base); err != nil {
		logger.Error(err, "patch status after trigger")
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
}

// checkInFlightBuild reads the in-flight CIBuild CR and updates watcher status accordingly.
// Returns (done=true) if the build reached a terminal state; (done=false) if still running.
func (r *GitWatcherReconciler) checkInFlightBuild(ctx context.Context, watcher *buildv1alpha1.GitWatcher, namespace string) (done bool, requeueAfter time.Duration, err error) {
	logger := log.FromContext(ctx)

	var ciBuild buildv1alpha1.CIBuild
	getErr := r.Get(ctx, types.NamespacedName{Name: watcher.Status.LastBuildName, Namespace: namespace}, &ciBuild)
	if getErr != nil && !k8serrors.IsNotFound(getErr) {
		logger.Error(getErr, "get in-flight CIBuild", "ciBuildName", watcher.Status.LastBuildName)
		return false, r.jitteredInterval(watcher.Name), nil
	}

	phase := ciBuild.Status.Phase
	switch phase {
	case buildv1alpha1.CIBuildPhaseBuilding, buildv1alpha1.CIBuildPhasePending, "":
		return false, r.jitteredInterval(watcher.Name), nil

	case buildv1alpha1.CIBuildPhaseSucceeded:
		base := client.MergeFrom(watcher.DeepCopy())
		built := watcher.Status.LastBuildVersion
		watcher.Status.LastBuiltVersion = built
		watcher.Status.ConsecutiveFailures = 0
		watcher.Status.LastBuildName = ""
		watcher.Status.LastBuildVersion = ""
		watcher.Status.Message = fmt.Sprintf("version %s built successfully", built)
		if pErr := r.Status().Patch(ctx, watcher, base); pErr != nil {
			logger.Error(pErr, "patch status after success")
			return true, 0, pErr
		}
		return true, 0, nil

	case buildv1alpha1.CIBuildPhaseFailed:
		base := client.MergeFrom(watcher.DeepCopy())
		if existing, dbErr := r.DB.GetVenvBuildByCIBuildName(ctx, watcher.Status.LastBuildName); dbErr == nil {
			r.cleanupFailedRow(ctx, existing)
		}
		failures := watcher.Status.ConsecutiveFailures + 1
		watcher.Status.ConsecutiveFailures = failures
		watcher.Status.LastBuildName = ""
		watcher.Status.LastBuildVersion = ""
		// Clear LastSeenCommit so next reconcile re-polls and retries.
		watcher.Status.LastSeenCommit = ""
		watcher.Status.LastError = fmt.Sprintf("build %s failed", ciBuild.Name)
		if failures >= r.MaxFailures {
			watcher.Status.Phase = buildv1alpha1.GitWatcherPhaseDisabled
			watcher.Status.Message = fmt.Sprintf("disabled after %d consecutive failures", failures)
		}
		if pErr := r.Status().Patch(ctx, watcher, base); pErr != nil {
			logger.Error(pErr, "patch status after failure")
			return true, 0, pErr
		}
		return true, 0, nil
	}

	return false, r.jitteredInterval(watcher.Name), nil
}

// resolveToken reads the token from the referenced K8s Secret, if any.
func (r *GitWatcherReconciler) resolveToken(ctx context.Context, watcher *buildv1alpha1.GitWatcher) (string, error) {
	if watcher.Spec.TokenSecretRef == nil {
		return "", nil
	}
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Name:      watcher.Spec.TokenSecretRef.Name,
		Namespace: watcher.Namespace,
	}, &secret); err != nil {
		return "", fmt.Errorf("get secret %s: %w", watcher.Spec.TokenSecretRef.Name, err)
	}
	return string(secret.Data[watcher.Spec.TokenSecretRef.Key]), nil
}

// resolveVersionAndMeta fetches the version (and full metadata for app builds) from the repo.
func (r *GitWatcherReconciler) resolveVersionAndMeta(
	ctx context.Context,
	watcher *buildv1alpha1.GitWatcher,
	repoRef, token string,
) (name, version string, appMeta *gitutil.AppMetadata, err error) {
	if watcher.Spec.BuildType == "app" {
		meta, ferr := gitutil.FetchAppMetadata(ctx, watcher.Spec.RepoURL, repoRef, watcher.Spec.ProjectDir, token)
		if ferr != nil {
			return "", "", nil, ferr
		}
		return meta.Name, meta.Version, &meta, nil
	}
	// git build
	src := watcher.Spec.MetadataSource
	if src == "" {
		src = "manual"
	}
	switch src {
	case "manual":
		return watcher.Spec.Name, watcher.Spec.Version, nil, nil
	case "version":
		meta, ferr := gitutil.FetchPyprojectMeta(ctx, watcher.Spec.RepoURL, repoRef, watcher.Spec.ProjectDir, token)
		if ferr != nil {
			return "", "", nil, ferr
		}
		return watcher.Spec.Name, meta.Version, nil, nil
	case "full":
		meta, ferr := gitutil.FetchPyprojectMeta(ctx, watcher.Spec.RepoURL, repoRef, watcher.Spec.ProjectDir, token)
		if ferr != nil {
			return "", "", nil, ferr
		}
		return meta.Name, meta.Version, nil, nil
	default:
		return "", "", nil, fmt.Errorf("unknown metadata_source %q", src)
	}
}

// cleanupFailedRow deletes the FAILED build row and its orphaned fusion-index version (best-effort).
func (r *GitWatcherReconciler) cleanupFailedRow(ctx context.Context, existing db.VenvBuild) {
	logger := log.FromContext(ctx)
	if existing.IndexArtifactID != nil && existing.IndexArtifactVersion != nil {
		if err := r.IndexClient.DeleteVersion(ctx, *existing.IndexArtifactID, *existing.IndexArtifactVersion); err != nil {
			logger.Error(err, "delete version from index", "artifactID", *existing.IndexArtifactID, "version", *existing.IndexArtifactVersion)
		}
	}
	if err := r.DB.DeleteVenvBuild(ctx, existing.ID); err != nil {
		logger.Error(err, "delete failed build row", "buildID", existing.ID)
	}
}

// jitteredInterval spreads poll intervals using an FNV hash of the watcher name.
func (r *GitWatcherReconciler) jitteredInterval(name string) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	maxJitterSec := uint32(r.PollInterval.Seconds() / 4)
	if maxJitterSec == 0 {
		maxJitterSec = 1
	}
	return r.PollInterval + time.Duration(h.Sum32()%maxJitterSec)*time.Second
}

// SetupWithManager registers the GitWatcher reconciler with the controller-runtime manager.
func (r *GitWatcherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&buildv1alpha1.GitWatcher{}).
		Named("gitwatcher").
		Complete(r)
}
