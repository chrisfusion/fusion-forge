// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 fusion-platform contributors

package indexclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetTag(t *testing.T) {
	var method, path string
	var body assignTagRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New(srv.URL).SetTag(context.Background(), 42, "stable", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut || path != "/api/v1/artifacts/42/tags/stable" || body.Version != "1.2.3" {
		t.Fatalf("got %s %s %+v", method, path, body)
	}
}

func TestSetTagError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"version not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	err := New(srv.URL).SetTag(context.Background(), 42, "stable", "9.9.9")
	if err == nil || !strings.Contains(err.Error(), "PUT") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}
