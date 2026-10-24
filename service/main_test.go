package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCLIInvalidArguments(t *testing.T) {
	binary := buildBinary(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tests := []struct {
		name string
		args []string
		base string
	}{
		{"missing repos", nil, server.URL},
		{"invalid repo", []string{"--repos", "owner"}, server.URL},
		{"invalid limit", []string{"--repos", "o/r", "--limit", "0"}, server.URL},
		{"invalid date", []string{"--repos", "o/r", "--since", "2026-02-30"}, server.URL},
		{"unknown flag", []string{"--repos", "o/r", "--unknown=process-test-secret"}, server.URL},
		{"missing value", []string{"--repos", "o/r", "--limit"}, server.URL},
		{"positional argument", []string{"--repos", "o/r", "extra"}, server.URL},
		{"empty API base", []string{"--repos", "o/r"}, ""},
		{"invalid API base", []string{"--repos", "o/r"}, "https://user:process-test-secret@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"report.json", "trace.jsonl"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("preserve existing contents"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{"--out", "report.json", "--trace", "trace.jsonl", "--cache-dir", "cache"}, tt.args...)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Dir = dir
			cmd.Env = cliEnvironment(tt.base)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("process exceeded deadline: %v", ctx.Err())
			}
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
				t.Fatalf("expected exit code 2, got %v; stderr: %s", err, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout must be empty, got %q", stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), "ghreport: ") {
				t.Fatalf("missing diagnostic: %q", stderr.String())
			}
			if strings.Contains(stderr.String(), "process-test-secret") {
				t.Fatal("diagnostic exposed secret")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 2 {
				t.Fatalf("unexpected files or cache directory: %v, %v", entries, err)
			}
			for _, name := range []string{"report.json", "trace.jsonl"} {
				contents, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(contents) != "preserve existing contents" {
					t.Fatalf("existing %s was modified: %v", name, err)
				}
			}
			if requests.Load() != 0 {
				t.Fatal("invalid configuration caused an HTTP request")
			}
		})
	}
}

func TestCLIReport(t *testing.T) {
	binary := buildBinary(t)
	for _, tt := range []struct {
		name   string
		body   string
		toFile bool
	}{
		{"stdout", testIssuePage, false},
		{"empty page", "[]", false},
		{"file", testIssuePage, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			dir := t.TempDir()
			out := "-"
			if tt.toFile {
				out = "отчёт.json"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--repos", "cli/cli", "--out", out)
			cmd.Dir, cmd.Env = dir, cliEnvironment(server.URL)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			before := time.Now().UTC().Truncate(time.Second)
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v; stderr: %s", err, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", stderr.String())
			}
			data := stdout.Bytes()
			if tt.toFile {
				if stdout.Len() != 0 {
					t.Fatal("file output must leave stdout empty")
				}
				var err error
				data, err = os.ReadFile(filepath.Join(dir, out))
				if err != nil {
					t.Fatal(err)
				}
			}
			var result report
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatalf("not a single JSON report: %v", err)
			}
			generated, err := time.Parse(time.RFC3339, result.GeneratedAt)
			if err != nil || !strings.HasSuffix(result.GeneratedAt, "Z") || generated.Before(before) || generated.After(time.Now().UTC()) {
				t.Fatalf("invalid generated_at: %q", result.GeneratedAt)
			}
			wantIssues, count := `[]`, 0
			if tt.body != "[]" {
				count = 2
				wantIssues = testReportIssues
			}
			assertJSON(t, json.RawMessage(data), fmt.Sprintf(`{
				"generated_at":%q,"repos":[{"repo":"cli/cli","error":null,"count":%d,"issues":%s}]
			}`, result.GeneratedAt, count, wantIssues))
		})
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	name := "ghreport"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}
	return binary
}

func cliEnvironment(base string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "GITHUB_API_BASE_URL") && !strings.EqualFold(key, "GITHUB_TOKEN") {
			env = append(env, entry)
		}
	}
	return append(env, "GITHUB_API_BASE_URL="+base, "GITHUB_TOKEN=process-test-secret")
}
