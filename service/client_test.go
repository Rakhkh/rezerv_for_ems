package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testIssuePage = `[
	{"number": 1, "title": "Первый выпуск", "user": {"login": "octocat"},
	 "labels": [{"name": "bug"}, {"name": "help wanted"}],
	 "created_at": "2026-01-05T10:00:00.123+03:00", "html_url": "https://github.com/cli/cli/issues/1",
	 "unknown_field": "ignored"},
	{"number": 2, "title": "Второй выпуск", "user": {"login": "hubot"}, "labels": [],
	 "created_at": "2026-01-06T11:00:00Z", "html_url": "https://github.com/cli/cli/issues/2"}
]`

const testReportIssues = `[
	{"number":1,"title":"Первый выпуск","author":"octocat","labels":["bug","help wanted"],
	 "created_at":"2026-01-05T10:00:00.123+03:00","url":"https://github.com/cli/cli/issues/1"},
	{"number":2,"title":"Второй выпуск","author":"hubot","labels":[],
	 "created_at":"2026-01-06T11:00:00Z","url":"https://github.com/cli/cli/issues/2"}
]`

func TestAPIClientRequestAndPage(t *testing.T) {
	for _, tt := range []struct {
		prefix string
		token  string
	}{
		{"", ""}, {"/api/v3", "test-token"}, {"/api/v3/", ""},
	} {
		t.Run(tt.prefix+tt.token, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				wantPath := strings.TrimRight(tt.prefix, "/") + "/repos/cli/cli/issues"
				if r.Method != http.MethodGet || r.URL.Path != wantPath {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				wantQuery := url.Values{"state": {"open"}, "sort": {"created"}, "direction": {"asc"}, "per_page": {"100"}}
				if !reflect.DeepEqual(r.URL.Query(), wantQuery) {
					t.Errorf("query = %v, want %v", r.URL.Query(), wantQuery)
				}
				wantAuth := ""
				if tt.token != "" {
					wantAuth = "Bearer " + tt.token
				}
				if r.Header.Get("Authorization") != wantAuth ||
					r.Header.Get("Accept") != "application/vnd.github+json" ||
					r.Header.Get("User-Agent") != "ghreport/1.0" ||
					r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
					t.Error("required request headers do not match contract")
				}
				fmt.Fprint(w, testIssuePage)
			}))
			defer server.Close()
			cfg, err := parseConfig([]string{"--repos", "cli/cli"}, envFrom(map[string]string{
				"GITHUB_API_BASE_URL": server.URL + tt.prefix, "GITHUB_TOKEN": tt.token,
			}))
			if err != nil {
				t.Fatal(err)
			}
			client := newAPIClient(cfg)
			defer client.http.CloseIdleConnections()
			issues, err := client.listIssues("cli/cli")
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d, want 1", requests.Load())
			}
			assertJSON(t, issues, testReportIssues)
		})
	}
}

func TestAPIClientBodyTimeout(t *testing.T) {
	headersSent := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(headersSent)
		<-r.Context().Done()
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(config{APIBaseURL: base})
	defer client.http.CloseIdleConnections()
	if client.http.Timeout != 30*time.Second {
		t.Fatalf("request timeout = %s, want 30s", client.http.Timeout)
	}
	client.http.Timeout = 250 * time.Millisecond
	if _, err := client.listIssues("cli/cli"); !errors.Is(err, repoRequestFailed) {
		t.Fatalf("body timeout error = %v, want request_failed", err)
	}
	select {
	case <-headersSent:
	default:
		t.Fatal("timeout occurred before response headers, not during body reading")
	}
}

func TestAPIClientErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unauthorized", 401, "private error body", errUnauthorized},
		{"not found", 404, "private error body", repoNotFound},
		{"server failure", 500, "private error body", repoRequestFailed},
		{"broken JSON", 200, "[", repoInvalidResponse},
		{"wrong structure", 200, "null", repoInvalidResponse},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			base, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := newAPIClient(config{APIBaseURL: base})
			defer client.http.CloseIdleConnections()
			if _, err := client.listIssues("cli/cli"); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}
