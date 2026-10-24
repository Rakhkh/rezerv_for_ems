package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestRedirectsAndRelativePagination(t *testing.T) {
	for _, status := range []int{301, 302, 307} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var active atomic.Int32
			requests := make(chan string, 10)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if active.Add(1) != 1 {
					t.Error("HTTP requests overlapped")
				}
				defer active.Add(-1)
				requests <- r.URL.Path + "?" + r.URL.RawQuery
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-token" ||
					r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
					t.Error("redirect request lost method or headers")
				}
				if r.URL.Path == "/repos/stub/alpha/issues" {
					w.Header().Set("Location", "/moved/issues?cursor=start")
					w.WriteHeader(status)
					return
				}
				if r.URL.Path != "/moved/issues" {
					t.Errorf("relative Link used wrong base: %s", r.URL)
					w.WriteHeader(404)
					return
				}
				if r.URL.Query().Get("cursor") == "start" {
					w.Header().Set("Link", `<?cursor=next>; rel=next`)
					fmt.Fprint(w, testPage(testIssue(1, "2026-01-01T00:00:00Z", "first")))
				} else {
					fmt.Fprint(w, testPage(testIssue(2, "2026-01-02T00:00:00Z", "second")))
				}
			}))
			defer server.Close()
			cfg, err := parseConfig([]string{"--repos", "stub/alpha"}, envFrom(map[string]string{
				"GITHUB_API_BASE_URL": server.URL, "GITHUB_TOKEN": "test-token",
			}))
			if err != nil {
				t.Fatal(err)
			}
			client := newAPIClient(cfg)
			defer client.closeIdleConnections()
			var trace bytes.Buffer
			client.trace = json.NewEncoder(&trace)
			for i := 0; i < 2; i++ {
				issues, err := client.collectIssues("stub/alpha", 10, nil)
				if err != nil || len(issues) != 2 || issues[0].Number != 1 || issues[1].Number != 2 {
					t.Fatalf("redirected collection: %v, %v", issues, err)
				}
			}
			first := "/repos/stub/alpha/issues?direction=asc&per_page=100&sort=created&state=open"
			want := []string{first, "/moved/issues?cursor=start", "/moved/issues?cursor=next"}
			if status != 301 {
				want = append(want, first)
			}
			want = append(want, "/moved/issues?cursor=start", "/moved/issues?cursor=next")
			var got []string
			for len(requests) > 0 {
				got = append(got, <-requests)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("requests=%v, want=%v", got, want)
			}
			records := parseTrace(t, trace.String())
			if len(records) != len(want) {
				t.Fatal("not every redirect hop was traced")
			}
			for i, record := range records {
				wantStatus := 200
				if want[i] == first {
					wantStatus = status
				}
				if record.Status != wantStatus || record.Attempt != 1 || record.Error != nil || record.URL != server.URL+want[i] {
					t.Fatalf("redirect trace: %+v", record)
				}
			}
		})
	}
}

func TestRedirectFailures(t *testing.T) {
	for _, tt := range []struct {
		name     string
		location string
		calls    int32
	}{
		{"cycle", "cycle", 1}, {"limit", "chain", 11},
		{"missing Location", "", 1}, {"malformed Location", "%zz", 1},
		{"invalid port", "http://localhost:65536/path", 1},
		{"credentials", "http://user:secret@localhost/path", 1}, {"scheme", "file:///tmp/page", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := calls.Add(1)
				if count > 12 {
					w.WriteHeader(404)
					return
				}
				location := tt.location
				if location == "cycle" {
					location = r.URL.RequestURI()
				} else if location == "chain" {
					location = fmt.Sprintf("/hop/%d", count)
				}
				w.Header().Set("Location", location)
				w.WriteHeader(302)
			}))
			defer server.Close()
			cfg, err := parseConfig([]string{"--repos", "stub/alpha"}, envFrom(map[string]string{"GITHUB_API_BASE_URL": server.URL}))
			if err != nil {
				t.Fatal(err)
			}
			client := newAPIClient(cfg)
			defer client.closeIdleConnections()
			if _, err := client.fetchPage(client.issuesURL("stub/alpha")); !errors.Is(err, repoRequestFailed) || calls.Load() != tt.calls {
				t.Fatalf("error=%v, requests=%d, want request_failed and %d requests", err, calls.Load(), tt.calls)
			}
		})
	}
}

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
