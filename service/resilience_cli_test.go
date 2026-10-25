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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runCLIProcess(t *testing.T, binary, dir, base string, args ...string) (int, []byte, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir, cmd.Env = dir, cliEnvironment(base)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return code, stdout.Bytes(), stderr.String()
}

func testCLIResilienceAndTrace(t *testing.T, binary string) {
	t.Run("rate limit and redirect recovery", func(t *testing.T) {
		var starts, active atomic.Int32
		var limitedAt atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if active.Add(1) != 1 {
				t.Error("HTTP requests overlapped")
			}
			defer active.Add(-1)
			if r.Header.Get("Authorization") != "Bearer process-test-secret" {
				t.Error("configured token was lost")
			}
			if r.URL.Path == "/repos/stub/alpha/issues" {
				w.Header().Set("Location", "/moved/issues?cursor=start")
				w.WriteHeader(302)
				return
			}
			if r.URL.Path != "/moved/issues" {
				t.Errorf("wrong pagination base: %s", r.URL)
				w.WriteHeader(404)
				return
			}
			if r.URL.Query().Get("cursor") == "start" {
				attempt := starts.Add(1)
				if attempt == 1 {
					limitedAt.Store(time.Now().UnixNano())
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(429)
					return
				}
				if attempt == 2 && time.Since(time.Unix(0, limitedAt.Load())) < time.Second {
					t.Error("retry arrived before Retry-After elapsed")
				}
				w.Header().Set("Link", `<?cursor=next>; rel=next`)
				fmt.Fprint(w, testPage(testIssue(2, "2026-01-02T00:00:00Z", "second")))
			} else {
				fmt.Fprint(w, testPage(testIssue(1, "2026-01-01T00:00:00Z", "first")))
			}
		}))
		defer server.Close()
		dir := testTempDir(t)
		tracePath := filepath.Join(dir, "trace.jsonl")
		if err := os.WriteFile(tracePath, []byte("old trace contents\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for run := 0; run < 2; run++ {
			code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL,
				"--repos", "stub/alpha", "--trace", tracePath)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d, stdout=%s, stderr=%s", code, stdout, stderr)
			}
			var result report
			if err := json.Unmarshal(stdout, &result); err != nil || len(result.Repos) != 1 ||
				result.Repos[0].Count != 2 || len(result.Repos[0].Issues) != 2 || result.Repos[0].Issues[0].Number != 1 || result.Repos[0].Issues[1].Number != 2 {
				t.Fatalf("invalid recovered report: %s, %v", stdout, err)
			}
			trace, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(trace)+string(stdout)+stderr, "process-test-secret") || strings.Contains(string(trace), "Authorization") {
				t.Fatal("secret leaked into output")
			}
			records := parseTrace(t, string(trace))
			statuses, attempts := []int{302, 429, 200, 200}, []int{1, 1, 2, 1}
			paths := []string{
				"/repos/stub/alpha/issues?direction=asc&per_page=100&sort=created&state=open",
				"/moved/issues?cursor=start", "/moved/issues?cursor=start", "/moved/issues?cursor=next",
			}
			if run == 1 {
				statuses, attempts = []int{302, 200, 200}, []int{1, 1, 1}
				paths = append(paths[:1], paths[2:]...)
			}
			if len(records) != len(statuses) {
				t.Fatalf("trace was appended or missed attempts: %+v", records)
			}
			for i, record := range records {
				if record.Status != statuses[i] || record.Attempt != attempts[i] || record.URL != server.URL+paths[i] || record.FromCache {
					t.Fatalf("trace record: %+v", record)
				}
				if statuses[i] == 429 {
					if record.Error == nil || *record.Error != "rate_limited" {
						t.Fatal("missing rate-limit trace error")
					}
				} else if record.Error != nil {
					t.Fatalf("unexpected trace error: %s", *record.Error)
				}
			}
		}
	})

	t.Run("rate exhaustion continues next repo", func(t *testing.T) {
		var limited atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/stub/limited/issues" {
				limited.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(429)
			} else {
				fmt.Fprint(w, "[]")
			}
		}))
		defer server.Close()
		dir := testTempDir(t)
		tracePath := filepath.Join(dir, "trace.jsonl")
		code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL,
			"--repos", "stub/limited,stub/good", "--trace", tracePath)
		if code != 1 || stderr != "" || limited.Load() != 3 {
			t.Fatalf("rate exhaustion: code=%d, stdout=%s, stderr=%s, attempts=%d", code, stdout, stderr, limited.Load())
		}
		var result report
		if err := json.Unmarshal(stdout, &result); err != nil {
			t.Fatal(err)
		}
		assertJSON(t, result.Repos, `[
			{"repo":"stub/limited","error":"rate_limited","count":0,"issues":[]},
			{"repo":"stub/good","error":null,"count":0,"issues":[]}
		]`)
		trace, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		records := parseTrace(t, string(trace))
		if len(records) != 4 {
			t.Fatalf("trace records=%d, want 4", len(records))
		}
		for i, record := range records {
			if i < 3 {
				if record.Status != 429 || record.Attempt != i+1 || record.Error == nil || *record.Error != "rate_limited" {
					t.Fatalf("rate-limit trace: %+v", record)
				}
			} else if record.Status != 200 || record.Attempt != 1 || record.Error != nil {
				t.Fatalf("next repo trace: %+v", record)
			}
		}
	})

	t.Run("trace open failure prevents HTTP", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			fmt.Fprint(w, "[]")
		}))
		defer server.Close()
		dir := testTempDir(t)
		code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL,
			"--repos", "stub/alpha", "--trace", dir)
		if code != 3 || len(stdout) != 0 || !strings.HasPrefix(stderr, "ghreport: ") || requests.Load() != 0 {
			t.Fatalf("trace I/O failure: code=%d, stdout=%s, stderr=%s, requests=%d", code, stdout, stderr, requests.Load())
		}
	})
}
