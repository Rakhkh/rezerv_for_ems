package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRedirectDoesNotForwardToken(t *testing.T) {
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if _, exists := r.Header["Authorization"]; exists {
			t.Error("redirect leaked Authorization to another origin")
		}
		fmt.Fprint(w, "[]")
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing token at origin")
		}
		w.Header().Set("Location", target.URL+"/issues")
		w.WriteHeader(307)
	}))
	defer origin.Close()
	cfg, err := parseConfig([]string{"--repos", "stub/alpha"}, envFrom(map[string]string{
		"GITHUB_API_BASE_URL": origin.URL, "GITHUB_TOKEN": "test-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(cfg)
	defer client.closeIdleConnections()
	if _, err := client.fetchPage(client.issuesURL("stub/alpha")); err != nil || calls.Load() != 1 {
		t.Fatalf("redirect failed: %v, requests=%d", err, calls.Load())
	}
}
