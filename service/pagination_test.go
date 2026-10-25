package main

import (
	"errors"
	"net/url"
	"testing"
)

func TestNextPage(t *testing.T) {
	current, err := url.Parse("https://api.example/api/issues?old=1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		headers []string
		want    string
		invalid bool
	}{
		{"absent", nil, "", false},
		{"no next", []string{`<?page=1>; rel="prev", <?page=7>; rel="last"`}, "", false},
		{"query reference", []string{`<?cursor=a%2Cb&custom=keep>; rel="next"`}, "https://api.example/api/issues?cursor=a%2Cb&custom=keep", false},
		{"relative path", []string{`<../pages/two>; rel=next`}, "https://api.example/pages/two", false},
		{"multiple headers and quoted comma", []string{
			`<?page=1>; rel=prev`, `<https://api.example/pages?cursor=a,b>; title="a,b; \"quoted\""; custom; REL="prev NEXT"`,
		}, "https://api.example/pages?cursor=a,b", false},
		{"first rel wins", []string{`<?page=1>; rel=prev; rel=next`}, "", false},
		{"missing angle brackets", []string{`?page=2; rel=next`}, "", true},
		{"unclosed quote", []string{`<?page=2>; rel="next`}, "", true},
		{"invalid URL", []string{`<%zz>; rel=next`}, "", true},
		{"non HTTP URL", []string{`<file:///tmp/page>; rel=next`}, "", true},
		{"conflicting next", []string{`<?page=2>; rel=next, <?page=3>; rel=next`}, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			next, err := nextPage(tt.headers, current)
			if tt.invalid {
				if !errors.Is(err, repoInvalidResponse) {
					t.Fatalf("error = %v, want invalid_response", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if next != nil {
				got = next.String()
			}
			if got != tt.want {
				t.Fatalf("next = %q, want %q", got, tt.want)
			}
		})
	}
}
