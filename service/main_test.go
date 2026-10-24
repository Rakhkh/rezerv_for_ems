package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestCLI(t *testing.T) {
	binary := buildBinary(t)
	t.Run("InvalidArguments", func(t *testing.T) {
		testCLIInvalidArguments(t, binary)
	})
	t.Run("Report", func(t *testing.T) {
		testCLIReport(t, binary)
	})
	t.Run("CollectionAndErrors", func(t *testing.T) {
		testCLICollectionAndErrors(t, binary)
	})
	t.Run("ResilienceAndTrace", func(t *testing.T) {
		testCLIResilienceAndTrace(t, binary)
	})
	t.Run("Cache", func(t *testing.T) {
		testCLICache(t, binary)
	})
	t.Run("FilesAndCombined", func(t *testing.T) {
		testCLIFilesAndCombined(t, binary)
	})
}

func testCLIInvalidArguments(t *testing.T, binary string) {
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
			dir := testTempDir(t)
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

func testCLIReport(t *testing.T, binary string) {
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
			dir := testTempDir(t)
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

// Windows may temporarily report a directory as nonempty after deleting its
// files. testing.TempDir retries access/sharing errors, but not error 145.
// Retry cleanup only; HTTP scenarios and application errors are never retried here.
func testTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Cleanup(func() {
			deadline := time.Now().Add(2 * time.Second)
			for {
				err := os.RemoveAll(dir)
				if err == nil {
					return
				}
				var code syscall.Errno
				if !errors.As(err, &code) || (code != 5 && code != 32 && code != 145) || !time.Now().Before(deadline) {
					t.Errorf("temporary directory cleanup: %v", err)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
	return dir
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

type testOutputFile struct {
	bytes.Buffer
	writeErr error
	closeErr error
	closes   int
}

func (f *testOutputFile) Write(data []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.Buffer.Write(data)
}

func (f *testOutputFile) Close() error {
	f.closes++
	return f.closeErr
}

func TestOutputFailures(t *testing.T) {
	for _, tt := range []struct {
		name     string
		target   string
		stage    string
		status   int
		requests int32
	}{
		{"trace open", "trace", "open", 200, 0},
		{"trace write", "trace", "write", 200, 1},
		{"trace close", "trace", "close", 200, 2},
		{"report open", "report", "open", 200, 2},
		{"report write", "report", "write", 200, 2},
		{"report close", "report", "close", 200, 2},
		{"stdout report write", "stdout", "write", 200, 2},
		{"stdout fatal write", "stdout", "write", 401, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(tt.status)
				fmt.Fprint(w, "[]")
			}))
			defer server.Close()
			dir := testTempDir(t)
			tracePath, reportPath := filepath.Join(dir, "trace"), filepath.Join(dir, "report")
			privateErr := errors.New("private-file-secret")
			opened := make(map[string]*testOutputFile)
			create := func(path string) (io.WriteCloser, error) {
				kind := "report"
				if path == tracePath {
					kind = "trace"
				}
				if kind == tt.target && tt.stage == "open" {
					return nil, privateErr
				}
				file := &testOutputFile{}
				if kind == tt.target {
					if tt.stage == "write" {
						file.writeErr = privateErr
					} else if tt.stage == "close" {
						file.closeErr = privateErr
					}
				}
				opened[kind] = file
				return file, nil
			}
			stdout := &testOutputFile{}
			if tt.target == "stdout" {
				reportPath = "-"
				stdout.writeErr = privateErr
			}
			var stderr bytes.Buffer
			code := runWithFiles([]string{"--repos", "stub/alpha,stub/never", "--out", reportPath,
				"--trace", tracePath, "--cache-dir", filepath.Join(dir, "cache")},
				envFrom(map[string]string{"GITHUB_API_BASE_URL": server.URL, "GITHUB_TOKEN": "private-file-secret"}), stdout, &stderr, create)
			if code != 3 || stdout.Len() != 0 || stderr.Len() == 0 || strings.Contains(stderr.String(), "private-file-secret") || requests.Load() != tt.requests {
				t.Fatalf("I/O failure: code=%d stdout=%s stderr=%s requests=%d", code, stdout, &stderr, requests.Load())
			}
			for name, file := range opened {
				if file.closes != 1 {
					t.Fatalf("%s closed %d times", name, file.closes)
				}
			}
			if tt.target == "trace" && opened["report"] != nil {
				t.Fatal("trace failure allowed writing a report")
			}
			if stdout.closes != 0 {
				t.Fatal("program must not close caller's stdout")
			}
		})
	}
}
