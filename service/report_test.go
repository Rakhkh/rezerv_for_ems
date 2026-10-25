package main

import "testing"

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
