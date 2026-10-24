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
