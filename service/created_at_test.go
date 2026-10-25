package main

import (
	"testing"
	"time"
)

func TestCreationTimeForms(t *testing.T) {
	for _, tt := range []struct {
		input      string
		normalized string
		remainder  string
	}{
		{"2026-01-01t00:00:00z", "2026-01-01T00:00:00Z", ""},
		{"2026-01-01T00:00:00.123456789000z", "2026-01-01T00:00:00.123456789Z", ""},
		{"2026-01-01t03:00:00.123456789001200+03:00", "2026-01-01T00:00:00.123456789Z", "0012"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			created, remainder, err := parseCreationTime(tt.input)
			if err != nil || created.UTC().Format(time.RFC3339Nano) != tt.normalized || remainder != tt.remainder {
				t.Fatalf("created=%v remainder=%q error=%v", created, remainder, err)
			}
		})
	}
	for _, input := range []string{
		"2026-01-01T00:00:00,1Z", "2026-01-01T00:00:00+24:00", "2026-01-01T00:00:00+00:60",
	} {
		if _, _, err := parseCreationTime(input); err == nil {
			t.Fatalf("invalid timestamp accepted: %q", input)
		}
	}
}
