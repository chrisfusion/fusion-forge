// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	buildv1alpha1 "fusion-platform.io/fusion-forge/api/v1alpha1"
)

type fakeTagger struct {
	calls []string
	err   error
}

func (f *fakeTagger) SetTag(_ context.Context, artifactID int64, tag, version string) error {
	f.calls = append(f.calls, tag+"@"+version)
	return f.err
}

func autoTagWatcher(autoTag, built string, artifactID int64) *buildv1alpha1.GitWatcher {
	return &buildv1alpha1.GitWatcher{
		Spec:   buildv1alpha1.GitWatcherSpec{AutoTag: autoTag},
		Status: buildv1alpha1.GitWatcherStatus{LastBuiltVersion: built, ArtifactID: artifactID},
	}
}

func TestSyncAutoTagMovesTagOnce(t *testing.T) {
	idx := &fakeTagger{}
	w := autoTagWatcher("stable", "1.2.0", 7)

	if !syncAutoTag(context.Background(), idx, w) {
		t.Fatal("first sync should change status")
	}
	if w.Status.LastAutoTag != "stable:1.2.0" {
		t.Fatalf("LastAutoTag = %q", w.Status.LastAutoTag)
	}
	if syncAutoTag(context.Background(), idx, w) {
		t.Fatal("second sync for the same version should be a no-op")
	}
	if len(idx.calls) != 1 || idx.calls[0] != "stable@1.2.0" {
		t.Fatalf("calls = %v", idx.calls)
	}
}

func TestSyncAutoTagFollowsNewVersionAndTagRename(t *testing.T) {
	idx := &fakeTagger{}
	w := autoTagWatcher("stable", "1.2.0", 7)
	syncAutoTag(context.Background(), idx, w)

	w.Status.LastBuiltVersion = "1.3.0"
	syncAutoTag(context.Background(), idx, w)
	w.Spec.AutoTag = "prod"
	syncAutoTag(context.Background(), idx, w)

	want := []string{"stable@1.2.0", "stable@1.3.0", "prod@1.3.0"}
	if strings.Join(idx.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", idx.calls, want)
	}
}

func TestSyncAutoTagReappliesAfterRebuild(t *testing.T) {
	idx := &fakeTagger{}
	w := autoTagWatcher("stable", "1.2.0", 7)
	syncAutoTag(context.Background(), idx, w)

	w.Status.LastAutoTag = "" // what a successful (re)build does
	if !syncAutoTag(context.Background(), idx, w) || len(idx.calls) != 2 {
		t.Fatalf("rebuilt version should be tagged again, calls = %v", idx.calls)
	}
}

func TestSyncAutoTagRetriesAfterError(t *testing.T) {
	idx := &fakeTagger{err: errors.New("index down")}
	w := autoTagWatcher("stable", "1.2.0", 7)

	if !syncAutoTag(context.Background(), idx, w) {
		t.Fatal("first failure should record LastError")
	}
	if w.Status.LastAutoTag != "" || !strings.Contains(w.Status.LastError, "index down") {
		t.Fatalf("status = %+v", w.Status)
	}
	if syncAutoTag(context.Background(), idx, w) {
		t.Fatal("the same failure again should not report a status change")
	}

	idx.err = nil
	if !syncAutoTag(context.Background(), idx, w) || w.Status.LastAutoTag != "stable:1.2.0" {
		t.Fatalf("retry should tag, status = %+v", w.Status)
	}
	if len(idx.calls) != 3 {
		t.Fatalf("calls = %v", idx.calls)
	}
}

func TestSyncAutoTagSkipsWithoutPrerequisites(t *testing.T) {
	for name, w := range map[string]*buildv1alpha1.GitWatcher{
		"autoTag unset":    autoTagWatcher("", "1.2.0", 7),
		"nothing built":    autoTagWatcher("stable", "", 7),
		"artifact unknown": autoTagWatcher("stable", "1.2.0", 0),
	} {
		idx := &fakeTagger{}
		if syncAutoTag(context.Background(), idx, w) || len(idx.calls) != 0 {
			t.Errorf("%s: expected no tag call, calls = %v", name, idx.calls)
		}
	}
}
