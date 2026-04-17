// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
)

func internalError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func notFoundOrInternal(c *gin.Context, err error, msg string) {
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": msg})
	} else {
		internalError(c, err)
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func pathID(c *gin.Context) (int64, bool) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id: " + raw})
		return 0, false
	}
	return id, true
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// callerUsername extracts the authenticated K8s username set by the auth middleware.
// Returns empty string when auth is disabled.
func callerUsername(c *gin.Context) string {
	v, _ := c.Get("k8s-username")
	s, _ := v.(string)
	return s
}

// syncStatusFromCR reads the CIBuild CR and returns the corresponding DB status string.
// Returns ("", false) when the CR cannot be read or has no useful status.
func syncStatusFromCR(ctx context.Context, crClient client.Client, namespace, ciBuildName string) (string, bool) {
	var ciBuild buildv1alpha1.CIBuild
	if err := crClient.Get(ctx, types.NamespacedName{
		Name:      ciBuildName,
		Namespace: namespace,
	}, &ciBuild); err != nil {
		return "", false
	}
	switch ciBuild.Status.Phase {
	case buildv1alpha1.CIBuildPhaseBuilding:
		return "BUILDING", true
	case buildv1alpha1.CIBuildPhaseSucceeded:
		return "SUCCESS", true
	case buildv1alpha1.CIBuildPhaseFailed:
		return "FAILED", true
	default:
		return "", false
	}
}

// podLogs returns concatenated logs from the first pod belonging to jobName.
// Returns empty string if no pods exist or the pod is in Pending phase.
func podLogs(ctx context.Context, kubeClient kubernetes.Interface, namespace, jobName string) (string, error) {
	pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", nil
	}
	pod := pods.Items[0]
	if pod.Status.Phase == corev1.PodPending {
		return "", nil
	}

	req := kubeClient.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
