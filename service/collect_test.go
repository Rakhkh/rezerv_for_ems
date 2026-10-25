package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	defer client.closeIdleConnections()
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

func TestCollectIssuesExactCreationTime(t *testing.T) {
	values := []struct {
		number int64
		date   string
	}{
		{2, "2026-01-01T00:00:00.0000000001Z"},
		{1, "2026-01-01t00:00:00.0000000002z"},
		{4, "2026-01-01T03:00:00.000000000200+03:00"},
		{3, "2026-01-01T00:00:00.000000000201Z"},
		{6, "2026-01-01T00:00:00.1234567890000000000001Z"},
		{5, "2026-01-01T00:00:00.1234567890000000000002Z"},
		{7, "2025-12-31t23:59:59.999999999999999999999z"},
	}
	var items []string
	for _, value := range values {
		items = append(items, testIssue(value.number, value.date, "issue"))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, testPage(items...))
	}))
	defer server.Close()
	cfg, err := parseConfig([]string{"--repos", "stub/alpha", "--since", "2026-01-01"},
		envFrom(map[string]string{"GITHUB_API_BASE_URL": server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(cfg)
	defer client.closeIdleConnections()
	issues, err := client.collectIssues("stub/alpha", cfg.Limit, cfg.Since)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{2, 1, 4, 3, 6, 5}
	if len(issues) != len(want) {
		t.Fatalf("selected %d issues, want %d", len(issues), len(want))
	}
	for i, number := range want {
		if issues[i].Number != number {
			t.Fatalf("issue %d: number=%d, want=%d", i, issues[i].Number, number)
		}
		for _, value := range values {
			if value.number == number && issues[i].CreatedAt != value.date {
				t.Fatal("original created_at was changed")
			}
		}
	}
}
