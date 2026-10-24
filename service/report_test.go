package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestReportJSONContract(t *testing.T) {
	notFound := repoNotFound
	value := report{
		GeneratedAt: "2026-10-14T09:00:00Z",
		Repos: []repoReport{
			{
				Repo: "cli/cli", Count: 2,
				Issues: []issue{
					{
						Number: 1, Title: "Первый выпуск", Author: "octocat", Labels: []string{"bug", "help wanted"},
						CreatedAt: "2026-01-05T10:00:00Z", URL: "https://github.com/cli/cli/issues/1",
					},
					{
						Number: 2, Title: "Второй выпуск", Author: "hubot", Labels: []string{},
						CreatedAt: "2026-01-06T11:00:00Z", URL: "https://github.com/cli/cli/issues/2",
					},
				},
			},
			{Repo: "example/missing", Error: &notFound, Count: 0, Issues: []issue{}},
		},
	}
	assertJSON(t, value, `{
		"generated_at": "2026-10-14T09:00:00Z",
		"repos": [
			{"repo": "cli/cli", "error": null, "count": 2, "issues": [
				{"number": 1, "title": "Первый выпуск", "author": "octocat", "labels": ["bug", "help wanted"],
				 "created_at": "2026-01-05T10:00:00Z", "url": "https://github.com/cli/cli/issues/1"},
				{"number": 2, "title": "Второй выпуск", "author": "hubot", "labels": [],
				 "created_at": "2026-01-06T11:00:00Z", "url": "https://github.com/cli/cli/issues/2"}
			]},
			{"repo": "example/missing", "error": "not_found", "count": 0, "issues": []}
		]
	}`)
}

func TestFatalReportJSONContract(t *testing.T) {
	assertJSON(t, fatalReport{Error: fatalUnauthorized}, `{"error":"unauthorized"}`)
	assertJSON(t, fatalReport{Error: fatalAllFailed}, `{"error":"all_failed"}`)
}

func TestReportContainsToken(t *testing.T) {
	const token = "private<&\"-token"
	for _, tt := range []struct {
		name string
		set  func(*report, string)
	}{
		{"generated_at", func(r *report, s string) { r.GeneratedAt = s }},
		{"repo", func(r *report, s string) { r.Repos[0].Repo = s }},
		{"error", func(r *report, s string) { code := repoError(s); r.Repos[0].Error = &code }},
		{"title", func(r *report, s string) { r.Repos[0].Issues[0].Title = s }},
		{"author", func(r *report, s string) { r.Repos[0].Issues[0].Author = s }},
		{"labels", func(r *report, s string) { r.Repos[0].Issues[0].Labels = []string{"safe", s} }},
		{"created_at", func(r *report, s string) { r.Repos[0].Issues[0].CreatedAt = s }},
		{"url", func(r *report, s string) { r.Repos[0].Issues[0].URL = s }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			value := report{Repos: []repoReport{{Repo: "stub/alpha", Issues: []issue{{Title: "safe"}}}}}
			if value.containsToken(token) || value.containsToken("") {
				t.Fatal("safe report or absent token rejected")
			}
			tt.set(&value, "prefix "+token+" suffix")
			if !value.containsToken(token) {
				t.Fatal("token in report value was not detected")
			}
		})
	}
}

func assertJSON(t *testing.T, value any, want string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var gotJSON, wantJSON any
	if err := json.Unmarshal(data, &gotJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("JSON does not match contract:\ngot: %s\nwant: %s", data, want)
	}
}
