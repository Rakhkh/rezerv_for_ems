package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testIssue(number int64, created, title string) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"user":{"login":"author"},"labels":[],"created_at":%q,"html_url":"https://example.com/issue/%d"}`,
		number, title, created, number)
}

func testPage(items ...string) string {
	return "[" + strings.Join(items, ",") + "]"
}

func TestCollectIssuesPaginationAndSelection(t *testing.T) {
	requests := make(chan string, 10)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.RequestURI()
		switch r.URL.Path {
		case "/repos/stub/alpha/issues":
			w.Header().Set("Link", `</repositories/123/issues?cursor=a%2Cb&custom=keep>; rel=next`)
			fmt.Fprint(w, testPage(
				testIssue(99, "2025-12-31T23:59:59Z", "too old"),
				testIssue(3, "2026-01-02T00:00:00Z", "first version"),
				testIssue(2, "2026-01-01T03:00:00+03:00", "boundary"),
				`{"pull_request":null}`, `{"pull_request":{},"number":"ignored"}`,
			))
		case "/repositories/123/issues":
			w.Header().Set("Link", fmt.Sprintf(`<%s/pages/three>; rel="next"`, server.URL))
			fmt.Fprint(w, "[]")
		case "/pages/three":
			w.Header().Set("Link", `</must-not-be-requested>; rel=next`)
			fmt.Fprint(w, testPage(
				testIssue(3, "2026-01-02T00:00:00Z", "duplicate version"),
				testIssue(1, "2026-01-01T00:00:00Z", "same instant, smaller number"),
				testIssue(4, "2026-01-03T00:00:00Z", "last selected"),
				testIssue(5, "2026-01-04T00:00:00Z", "beyond limit"),
			))
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cfg, err := parseConfig([]string{"--repos", "stub/alpha", "--limit", "4", "--since", "2026-01-01"},
		envFrom(map[string]string{"GITHUB_API_BASE_URL": server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(cfg)
	defer client.http.CloseIdleConnections()
	issues, err := client.collectIssues("stub/alpha", cfg.Limit, cfg.Since)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int64
	for _, item := range issues {
		numbers = append(numbers, item.Number)
	}
	if !reflect.DeepEqual(numbers, []int64{1, 2, 3, 4}) || issues[2].Title != "first version" ||
		issues[1].CreatedAt != "2026-01-01T03:00:00+03:00" {
		t.Fatalf("incorrect selection, sorting or deduplication: %+v", issues)
	}
	var gotRequests []string
	for len(requests) > 0 {
		gotRequests = append(gotRequests, <-requests)
	}
	wantRequests := []string{
		"/repos/stub/alpha/issues?direction=asc&per_page=100&sort=created&state=open",
		"/repositories/123/issues?cursor=a%2Cb&custom=keep", "/pages/three",
	}
	if !reflect.DeepEqual(gotRequests, wantRequests) {
		t.Fatalf("requests = %v, want %v", gotRequests, wantRequests)
	}
}

func TestCollectIssuesCycleAndEarlyStop(t *testing.T) {
	for _, limit := range []int{1, 100} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) > 1 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Link", fmt.Sprintf(`<%s>; rel=next`, r.URL.RequestURI()))
				fmt.Fprint(w, testPage(testIssue(1, "2026-01-01T00:00:00Z", "issue")))
			}))
			defer server.Close()
			cfg, err := parseConfig([]string{"--repos", "stub/alpha"}, envFrom(map[string]string{"GITHUB_API_BASE_URL": server.URL}))
			if err != nil {
				t.Fatal(err)
			}
			client := newAPIClient(cfg)
			defer client.http.CloseIdleConnections()
			issues, err := client.collectIssues("stub/alpha", limit, nil)
			if limit == 1 {
				if err != nil || len(issues) != 1 {
					t.Fatalf("limit must stop before following next: %v, %v", issues, err)
				}
			} else if !errors.Is(err, repoInvalidResponse) || issues != nil {
				t.Fatalf("cycle must discard collected issues: %v, %v", issues, err)
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d, want 1", requests.Load())
			}
		})
	}
}

func TestPaginationDoesNotForwardTokenToAnotherOrigin(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetRequests.Add(1)
		if _, exists := r.Header["Authorization"]; exists {
			t.Error("token was forwarded to another origin")
		}
		fmt.Fprint(w, "[]")
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-secret" {
			t.Error("origin did not receive configured token")
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/page>; rel=next`, target.URL))
		fmt.Fprint(w, "[]")
	}))
	defer origin.Close()
	cfg, err := parseConfig([]string{"--repos", "stub/alpha"}, envFrom(map[string]string{
		"GITHUB_API_BASE_URL": origin.URL, "GITHUB_TOKEN": "local-secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(cfg)
	defer client.http.CloseIdleConnections()
	issues, err := client.collectIssues("stub/alpha", 100, nil)
	if err != nil || issues == nil || len(issues) != 0 || targetRequests.Load() != 1 {
		t.Fatalf("unexpected result: %v, %v", issues, err)
	}
}

func TestDecodeIssuesValidation(t *testing.T) {
	valid := testIssue(1, "2026-01-01T00:00:00Z", "issue")
	for _, body := range []string{
		`[{}]`, `[null]`, testPage(strings.Replace(valid, `"number":1`, `"number":"one"`, 1)),
		testPage(testIssue(1, "invalid-date", "issue")),
		testPage(strings.Replace(valid, `"labels":[]`, `"labels":[{}]`, 1)),
		"[\"" + string([]byte{255}) + "\"]",
	} {
		if _, err := decodeIssues([]byte(body)); !errors.Is(err, repoInvalidResponse) {
			t.Fatalf("expected invalid_response for %q, got %v", body, err)
		}
	}
	issues, err := decodeIssues([]byte(`[{"pull_request":null},{"pull_request":{},"number":"ignored"}]`))
	if err != nil || issues == nil || len(issues) != 0 {
		t.Fatalf("PRs must be skipped before issue field validation: %v, %v", issues, err)
	}
	// The parsed instant, rather than the original RFC3339 string, drives selection.
	issues, err = decodeIssues([]byte(testPage(valid)))
	if err != nil || !issues[0].created.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("creation time was not parsed: %v, %v", issues, err)
	}
}
