package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOutputPathAliases(t *testing.T) {
	dir := testTempDir(t)
	first := filepath.Join(dir, "output.json")
	other := filepath.Join(dir, "other.json")
	for _, tt := range []struct {
		out, trace string
		want       bool
	}{
		{first, first, true}, {first, other, false}, {"-", first, false}, {first, "", false},
		{filepath.Join(dir, "missing", "out"), filepath.Join(dir, "missing", "out"), true},
		{first, filepath.Join(dir, "OUTPUT.JSON"), runtime.GOOS == "windows"},
	} {
		if got, err := outputPathsConflict(tt.out, tt.trace); err != nil || got != tt.want {
			t.Fatalf("paths %q / %q: conflict=%v, error=%v", tt.out, tt.trace, got, err)
		}
	}
	t.Run("symbolic links", func(t *testing.T) {
		alias := filepath.Join(dir, "link.json")
		if err := os.Symlink("output.json", alias); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		if got, err := outputPathsConflict(first, alias); err != nil || !got {
			t.Fatalf("dangling symlink alias: %v, %v", got, err)
		}
		parentAlias := filepath.Join(dir, "parent-link")
		if err := os.Symlink(dir, parentAlias); err != nil {
			t.Fatal(err)
		}
		if got, err := outputPathsConflict(first, filepath.Join(parentAlias, "output.json")); err != nil || !got {
			t.Fatalf("symlink parent alias: %v, %v", got, err)
		}
	})
}

func envFrom(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, exists := values[key]
		return value, exists
	}
}

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig([]string{"--repos", "cli/cli"}, envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Repos, []string{"cli/cli"}) || cfg.Limit != 100 ||
		cfg.Since != nil || cfg.Out != "-" || cfg.Trace != "" ||
		cfg.CacheDir != "./ghreport-cache" || cfg.Token != "" ||
		cfg.APIBaseURL.String() != "https://api.github.com" {
		t.Fatal("configuration does not match specified defaults")
	}
}

func TestParseConfigExplicitValues(t *testing.T) {
	args := []string{
		"--repos", " cli/cli ,stub/alpha,cli/cli,CLI/cli ",
		"--limit", "7", "--since", "2024-02-29",
		"--out", "отчёт.json", "--trace", "HTTP журнал.jsonl", "--cache-dir", "кэш",
	}
	cfg, err := parseConfig(args, envFrom(map[string]string{
		"GITHUB_API_BASE_URL": "http://127.0.0.1:8080/api/v3/",
		"GITHUB_TOKEN":        "test-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Repos, []string{"cli/cli", "stub/alpha", "CLI/cli"}) {
		t.Fatalf("repos = %v", cfg.Repos)
	}
	if cfg.Limit != 7 || cfg.Out != "отчёт.json" || cfg.Trace != "HTTP журнал.jsonl" ||
		cfg.CacheDir != "кэш" || cfg.Token != "test-token" ||
		cfg.APIBaseURL.String() != "http://127.0.0.1:8080/api/v3/" {
		t.Fatal("explicit configuration values were not preserved")
	}
	want := time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)
	if cfg.Since == nil || !cfg.Since.Equal(want) || cfg.Since.Location() != time.UTC {
		t.Fatalf("since = %v, want midnight UTC", cfg.Since)
	}
}

func TestParseConfigInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing repos", nil},
		{"empty repos", []string{"--repos", ""}},
		{"empty list element", []string{"--repos", "owner/repo,"}},
		{"missing owner", []string{"--repos", "/repo"}},
		{"missing repo", []string{"--repos", "owner/"}},
		{"extra slash", []string{"--repos", "owner/repo/issues"}},
		{"repo URL", []string{"--repos", "https://example.com/owner/repo"}},
		{"internal whitespace", []string{"--repos", "owner/re po"}},
		{"path traversal", []string{"--repos", "owner/.."}},
		{"query in repo", []string{"--repos", "owner/repo?x=1"}},
		{"backslash in repo", []string{"--repos", `owner/repo\name`}},
		{"zero limit", []string{"--repos", "o/r", "--limit", "0"}},
		{"negative limit", []string{"--repos", "o/r", "--limit", "-1"}},
		{"noninteger limit", []string{"--repos", "o/r", "--limit", "1.5"}},
		{"overflow limit", []string{"--repos", "o/r", "--limit", "999999999999999999999999"}},
		{"empty since", []string{"--repos", "o/r", "--since", ""}},
		{"impossible date", []string{"--repos", "o/r", "--since", "2025-02-29"}},
		{"short date", []string{"--repos", "o/r", "--since", "2026-1-02"}},
		{"short day", []string{"--repos", "o/r", "--since", "2026-01-2"}},
		{"date with whitespace", []string{"--repos", "o/r", "--since", "2026-01-02 "}},
		{"timestamp instead of date", []string{"--repos", "o/r", "--since", "2026-01-02T00:00:00Z"}},
		{"unknown flag", []string{"--repos", "o/r", "--unknown"}},
		{"missing flag value", []string{"--repos"}},
		{"extra argument", []string{"--repos", "o/r", "extra"}},
		{"positional before flags", []string{"extra", "--repos", "o/r"}},
		{"empty out", []string{"--repos", "o/r", "--out", ""}},
		{"empty trace", []string{"--repos", "o/r", "--trace", ""}},
		{"empty cache dir", []string{"--repos", "o/r", "--cache-dir", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseConfig(tt.args, envFrom(nil)); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestParseConfigLimitAndDateBoundaries(t *testing.T) {
	cfg, err := parseConfig([]string{"--repos=o/r", "--limit=1", "--since=2026-01-02"}, envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limit != 1 || cfg.Since == nil || cfg.Since.Format(time.RFC3339) != "2026-01-02T00:00:00Z" {
		t.Fatal("minimum limit or inclusive UTC date boundary was not parsed correctly")
	}
}

func TestParseConfigAPIBaseURL(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"https://api.github.com", true},
		{"https://api.github.com/", true},
		{"https://example.com/api/v3", true},
		{"http://localhost:8080/api/v3/", true},
		{"http://[::1]:8080/", true},
		{"", false},
		{"localhost:8080", false},
		{"/api/v3", false},
		{"ftp://example.com", false},
		{"https:///api/v3", false},
		{"https://user:password@example.com", false},
		{"https://example.com?token=secret", false},
		{"https://example.com?", false},
		{"https://example.com#fragment", false},
		{"https://example.com#", false},
		{"https://example.com:invalid", false},
		{"https://example.com:65536", false},
		{"https://example.com:0", false},
		{"https://example.com/%zz", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			cfg, err := parseConfig([]string{"--repos", "o/r"}, envFrom(map[string]string{
				"GITHUB_API_BASE_URL": tt.value,
			}))
			if tt.valid {
				if err != nil {
					t.Fatal(err)
				}
				if cfg.APIBaseURL.String() != tt.value {
					t.Fatalf("base URL = %q, want %q", cfg.APIBaseURL.String(), tt.value)
				}
			} else if err == nil {
				t.Fatal("expected configuration error, not default API fallback")
			}
		})
	}
}

func TestParseConfigToken(t *testing.T) {
	for _, token := range []string{"", "test-token"} {
		t.Run(token, func(t *testing.T) {
			cfg, err := parseConfig([]string{"--repos", "o/r"}, envFrom(map[string]string{
				"GITHUB_TOKEN": token,
			}))
			if err != nil || cfg.Token != token {
				t.Fatal("token from environment was not preserved")
			}
		})
	}
}

func TestConfigErrorsDoNotExposeValues(t *testing.T) {
	const secret = "do-not-print-this-value"
	for _, args := range [][]string{
		{"--repos", "o/r", "--unknown=" + secret},
		{"--repos", secret},
		{"--repos", "o/r", "--limit", secret},
		{"--repos", "o/r", "--since", secret},
		{"--repos", "o/r"},
	} {
		_, err := parseConfig(args, envFrom(map[string]string{
			"GITHUB_TOKEN":        secret,
			"GITHUB_API_BASE_URL": "https://user:" + secret + "@example.com",
		}))
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("expected a safe error without the supplied secret")
		}
	}
}
