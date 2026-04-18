// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

// Package jobbuilder constructs Kubernetes Job and ConfigMap manifests from a CIBuild spec.
package jobbuilder

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
)

const (
	labelManagedByKey   = "app.kubernetes.io/managed-by"
	labelManagedByValue = "fusion-forge"
	labelBuildIDKey     = "build.fusion-platform.io/cibuild"
	builderSA           = "fusion-forge-builder"
)

// ConfigMapName returns the deterministic ConfigMap name for a CIBuild.
func ConfigMapName(ciBuildName string) string {
	return "forge-cfg-" + ciBuildName
}

// JobName returns the deterministic Job name for a CIBuild.
func JobName(ciBuildName string) string {
	return "forge-job-" + ciBuildName
}

// BuildConfigMap creates a ConfigMap that holds the CIBuild's configData files.
// The ConfigMap is owned by the CIBuild CR so it is garbage-collected on deletion.
// For git builds configData is empty; the ConfigMap is still created as a no-op placeholder
// so the operator's ownership and cleanup logic remains uniform across build types.
func BuildConfigMap(ciBuild *buildv1alpha1.CIBuild) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName(ciBuild.Name),
			Namespace: ciBuild.Namespace,
			Labels: map[string]string{
				labelManagedByKey: labelManagedByValue,
				labelBuildIDKey:   ciBuild.Name,
			},
		},
		Data: ciBuild.Spec.ConfigData,
	}
}

// BuildJob creates the batch/v1 Job that runs the builder container.
// The Job is owned by the CIBuild CR so it is garbage-collected on deletion.
// For git builds (BuildType=="git") the GitSource fields are injected as env vars and
// the ConfigMap volume/mounts are omitted because there are no workspace files to mount.
func BuildJob(ciBuild *buildv1alpha1.CIBuild, configMapName string) *batchv1.Job {
	backoffLimit := int32(0)
	ttl := int32(86400) // 24 h

	// Build volume mounts — one per key in ConfigData (requirements builds only).
	var mounts []corev1.VolumeMount
	for filename := range ciBuild.Spec.ConfigData {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "build-config",
			MountPath: "/workspace/" + filename,
			SubPath:   filename,
			ReadOnly:  true,
		})
	}

	// Fixed baseline security context — satisfies the Kubernetes "baseline" Pod Security
	// Standard. readOnlyRootFilesystem is intentionally not set because the builder writes
	// venv, wheel, and archive artifacts to /workspace inside the container.
	falseVal := false
	builderSecurityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &falseVal,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}

	// Base env vars injected by the operator; Spec.Env adds build-specific ones.
	baseEnv := []corev1.EnvVar{
		{Name: "INDEX_BACKEND_URL", Value: ciBuild.Spec.IndexBackendURL},
	}
	// For git builds inject GitSource fields as env vars so the builder binary can read them.
	if ciBuild.Spec.BuildType == "git" && ciBuild.Spec.GitSource != nil {
		gs := ciBuild.Spec.GitSource
		baseEnv = append(baseEnv,
			corev1.EnvVar{Name: "GIT_REPO_URL", Value: gs.URL},
			corev1.EnvVar{Name: "GIT_REF", Value: gs.Ref},
			corev1.EnvVar{Name: "ENTRYPOINT_FILE", Value: gs.EntrypointFile},
			corev1.EnvVar{Name: "GIT_PROJECT_DIR", Value: gs.ProjectDir},
		)
	}
	env := append(baseEnv, ciBuild.Spec.Env...)

	// Only declare the ConfigMap volume when there are files to mount.
	var volumes []corev1.Volume
	if len(ciBuild.Spec.ConfigData) > 0 {
		volumes = []corev1.Volume{
			{
				Name: "build-config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: configMapName,
						},
					},
				},
			},
		}
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      JobName(ciBuild.Name),
			Namespace: ciBuild.Namespace,
			Labels: map[string]string{
				labelManagedByKey:             labelManagedByValue,
				"app.kubernetes.io/component": "ci-builder",
				labelBuildIDKey:               ciBuild.Name,
			},
			Annotations: map[string]string{
				"build.fusion-platform.io/artifact": ciBuild.Spec.ArtifactName + ":" + ciBuild.Spec.ArtifactVersion,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelManagedByKey: labelManagedByValue,
						labelBuildIDKey:   ciBuild.Name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: builderSA,
					Containers: []corev1.Container{
						{
							Name:            "builder",
							Image:           ciBuild.Spec.BuilderImage,
							SecurityContext: builderSecurityContext,
							Env:             env,
							VolumeMounts:    mounts,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("2000m"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}
}
