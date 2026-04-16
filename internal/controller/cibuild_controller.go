// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
	"fusion-platform.io/fusion-forge/internal/jobbuilder"
)

// ciBuilGVK is used when constructing owner references.
// r.Get zeroes out TypeMeta on returned objects — set it explicitly.
var ciBuildGVK = schema.GroupVersionKind{
	Group:   buildv1alpha1.GroupVersion.Group,
	Version: buildv1alpha1.GroupVersion.Version,
	Kind:    "CIBuild",
}

// CIBuildReconciler reconciles CIBuild objects by managing the lifecycle of
// the corresponding Kubernetes Job and ConfigMap.
type CIBuildReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=build.fusion-platform.io,resources=cibuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=build.fusion-platform.io,resources=cibuilds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get

func (r *CIBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ciBuild buildv1alpha1.CIBuild
	if err := r.Get(ctx, req.NamespacedName, &ciBuild); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Capture patch base immediately after Get (before any mutations).
	base := client.MergeFrom(ciBuild.DeepCopy())

	// Terminal builds need no further action.
	if ciBuild.Status.Phase == buildv1alpha1.CIBuildPhaseSucceeded ||
		ciBuild.Status.Phase == buildv1alpha1.CIBuildPhaseFailed {
		return ctrl.Result{}, nil
	}

	// Set TypeMeta for owner references (r.Get zeroes TypeMeta).
	ciBuildWithGVK := ciBuild.DeepCopy()
	ciBuildWithGVK.TypeMeta = metav1.TypeMeta{
		APIVersion: ciBuildGVK.GroupVersion().String(),
		Kind:       ciBuildGVK.Kind,
	}

	cmName := jobbuilder.ConfigMapName(ciBuild.Name)
	jobName := jobbuilder.JobName(ciBuild.Name)

	// Phase: empty or Pending — create ConfigMap + Job.
	if ciBuild.Status.Phase == "" || ciBuild.Status.Phase == buildv1alpha1.CIBuildPhasePending {
		if err := r.ensureConfigMap(ctx, ciBuildWithGVK, cmName); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensure ConfigMap: %w", err)
		}
		if err := r.ensureJob(ctx, ciBuildWithGVK, cmName, jobName); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensure Job: %w", err)
		}

		now := metav1.Now()
		ciBuild.Status.Phase = buildv1alpha1.CIBuildPhaseBuilding
		ciBuild.Status.JobName = jobName
		ciBuild.Status.ConfigMapName = cmName
		ciBuild.Status.StartedAt = &now
		if err := r.Status().Patch(ctx, &ciBuild, base); err != nil {
			return ctrl.Result{}, fmt.Errorf("patch status Building: %w", err)
		}
		logger.Info("build started", "ciBuild", ciBuild.Name, "job", jobName)
		return ctrl.Result{}, nil
	}

	// Phase: Building — check Job status.
	var job batchv1.Job
	if err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: ciBuild.Namespace}, &job); err != nil {
		if errors.IsNotFound(err) {
			// Job disappeared — mark failed.
			return ctrl.Result{}, r.markFailed(ctx, &ciBuild, base, "build Job not found")
		}
		return ctrl.Result{}, err
	}

	if isJobFailed(&job) {
		msg := jobFailureMessage(&job)
		logger.Info("build failed", "ciBuild", ciBuild.Name, "message", msg)
		if err := r.deleteConfigMap(ctx, ciBuild.Namespace, cmName); err != nil {
			return ctrl.Result{}, fmt.Errorf("delete ConfigMap on failure: %w", err)
		}
		return ctrl.Result{}, r.markFailed(ctx, &ciBuild, base, msg)
	}

	if isJobSucceeded(&job) {
		logger.Info("build succeeded", "ciBuild", ciBuild.Name)
		if err := r.deleteConfigMap(ctx, ciBuild.Namespace, cmName); err != nil {
			return ctrl.Result{}, fmt.Errorf("delete ConfigMap on success: %w", err)
		}
		return ctrl.Result{}, r.markSucceeded(ctx, &ciBuild, base)
	}

	// Still running — no action needed; Job change will re-trigger reconcile.
	return ctrl.Result{}, nil
}

// ensureConfigMap creates the ConfigMap if it does not already exist.
func (r *CIBuildReconciler) ensureConfigMap(ctx context.Context, owner *buildv1alpha1.CIBuild, cmName string) error {
	cm := jobbuilder.BuildConfigMap(owner)
	if err := ctrl.SetControllerReference(owner, cm, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, cm); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// ensureJob creates the Job if it does not already exist.
func (r *CIBuildReconciler) ensureJob(ctx context.Context, owner *buildv1alpha1.CIBuild, cmName, jobName string) error {
	job := jobbuilder.BuildJob(owner, cmName)
	if err := ctrl.SetControllerReference(owner, job, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *CIBuildReconciler) markSucceeded(ctx context.Context, ciBuild *buildv1alpha1.CIBuild, base client.Patch) error {
	now := metav1.Now()
	ciBuild.Status.Phase = buildv1alpha1.CIBuildPhaseSucceeded
	ciBuild.Status.ConfigMapName = ""
	ciBuild.Status.CompletedAt = &now
	return r.Status().Patch(ctx, ciBuild, base)
}

func (r *CIBuildReconciler) markFailed(ctx context.Context, ciBuild *buildv1alpha1.CIBuild, base client.Patch, msg string) error {
	now := metav1.Now()
	ciBuild.Status.Phase = buildv1alpha1.CIBuildPhaseFailed
	ciBuild.Status.ConfigMapName = ""
	ciBuild.Status.Message = msg
	ciBuild.Status.CompletedAt = &now
	return r.Status().Patch(ctx, ciBuild, base)
}

// deleteConfigMap deletes the ConfigMap for a terminal build.
// Not-found is treated as success (already cleaned up). Transient errors are returned
// so the reconciler can retry rather than silently leaking the ConfigMap.
func (r *CIBuildReconciler) deleteConfigMap(ctx context.Context, namespace, name string) error {
	err := r.Delete(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	})
	return client.IgnoreNotFound(err)
}

func isJobSucceeded(job *batchv1.Job) bool {
	return job.Status.Succeeded > 0
}

// isJobFailed gates on the authoritative JobFailed condition rather than
// Status.Failed > 0, which can be incremented before the condition is set,
// causing a brief window where failure message is unavailable.
func isJobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == "True" {
			return true
		}
	}
	return false
}

func jobFailureMessage(job *batchv1.Job) string {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == "True" {
			if c.Message != "" {
				return c.Message
			}
		}
	}
	return "job failed"
}

// SetupWithManager registers the controller and sets up watches.
func (r *CIBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&buildv1alpha1.CIBuild{}).
		// Watch owned Jobs — a Job status change re-triggers the owning CIBuild reconcile.
		Watches(&batchv1.Job{}, handler.EnqueueRequestForOwner(
			mgr.GetScheme(), mgr.GetRESTMapper(),
			&buildv1alpha1.CIBuild{},
			handler.OnlyControllerOwner(),
		)).
		Named("cibuild").
		Complete(r)
}

// ensure reconcile.Reconciler is satisfied
var _ reconcile.Reconciler = &CIBuildReconciler{}
