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
	"reflect"
	"strings"
	"testing"
	"time"
)

func testCLICollectionAndErrors(t *testing.T, binary string) {
	const good = `{"repo":"stub/good","error":null,"count":1,"issues":[
		{"number":1,"title":"good","author":"author","labels":[],"created_at":"2026-01-01T00:00:00Z","url":"https://example.com/issue/1"}]}`
	const empty = `{"repo":"stub/empty","error":null,"count":0,"issues":[]}`
	const missing = `{"repo":"stub/missing","error":"not_found","count":0,"issues":[]}`
	const broken = `{"repo":"stub/broken","error":"invalid_response","count":0,"issues":[]}`
	const denied = `{"repo":"stub/denied","error":"request_failed","count":0,"issues":[]}`
	const invalid = `{"repo":"stub/invalid","error":"request_failed","count":0,"issues":[]}`
	const late404 = `{"repo":"stub/late404","error":"not_found","count":0,"issues":[]}`
	const paged = `{"repo":"stub/paged","error":null,"count":2,"issues":[
		{"number":1,"title":"first","author":"author","labels":[],"created_at":"2026-01-01T00:00:00Z","url":"https://example.com/issue/1"},
		{"number":2,"title":"boundary","author":"author","labels":[],"created_at":"2026-01-01T03:00:00+03:00","url":"https://example.com/issue/2"}]}`
	for _, tt := range []struct {
		name      string
		repos     string
		code      int
		wantRepos string
		fatal     string
		paths     []string
	}{
		{"paginated success", "stub/paged,stub/empty,stub/paged", 0, "[" + paged + "," + empty + "]", "",
			[]string{"/repos/stub/paged/issues", "/pages/two", "/repos/stub/empty/issues"}},
		{"partial failure", "stub/good,stub/missing,stub/broken,stub/denied,stub/invalid", 1,
			"[" + strings.Join([]string{good, missing, broken, denied, invalid}, ",") + "]", "",
			[]string{"/repos/stub/good/issues", "/repos/stub/missing/issues", "/repos/stub/broken/issues", "/pages/broken", "/repos/stub/denied/issues", "/repos/stub/invalid/issues"}},
		{"late 404 discards issues", "stub/good,stub/late404", 1, "[" + good + "," + late404 + "]", "",
			[]string{"/repos/stub/good/issues", "/repos/stub/late404/issues", "/pages/missing"}},
		{"all failed", "stub/missing,stub/broken", 3, "", "all_failed",
			[]string{"/repos/stub/missing/issues", "/repos/stub/broken/issues", "/pages/broken"}},
		{"unauthorized on next page", "stub/good,stub/auth,stub/never", 3, "", "unauthorized",
			[]string{"/repos/stub/good/issues", "/repos/stub/auth/issues", "/pages/auth"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan string, 32)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.URL.Path
				switch r.URL.Path {
				case "/repos/stub/paged/issues":
					w.Header().Set("Link", `</pages/two?cursor=opaque>; rel=next`)
					fmt.Fprint(w, testPage(`{"pull_request":null}`,
						testIssue(99, "2025-12-31T23:59:59Z", "too old"),
						testIssue(2, "2026-01-01T03:00:00+03:00", "boundary")))
				case "/pages/two":
					w.Header().Set("Link", `</must-not-be-requested>; rel=next`)
					fmt.Fprint(w, testPage(testIssue(2, "2026-01-01T03:00:00+03:00", "duplicate"),
						testIssue(1, "2026-01-01T00:00:00Z", "first")))
				case "/repos/stub/good/issues":
					fmt.Fprint(w, testPage(testIssue(1, "2026-01-01T00:00:00Z", "good")))
				case "/repos/stub/empty/issues":
					fmt.Fprint(w, "[]")
				case "/repos/stub/broken/issues", "/repos/stub/auth/issues", "/repos/stub/late404/issues":
					next := "/pages/broken"
					if strings.Contains(r.URL.Path, "/auth/") {
						next = "/pages/auth"
					} else if strings.Contains(r.URL.Path, "/late404/") {
						next = "/pages/missing"
					}
					w.Header().Set("Link", fmt.Sprintf(`<%s>; rel=next`, next))
					fmt.Fprint(w, testPage(testIssue(99, "2026-01-01T00:00:00Z", "partial")))
				case "/repos/stub/missing/issues", "/pages/missing":
					w.WriteHeader(http.StatusNotFound)
				case "/repos/stub/denied/issues":
					w.WriteHeader(http.StatusForbidden)
				case "/repos/stub/invalid/issues":
					w.WriteHeader(http.StatusUnprocessableEntity)
				case "/pages/broken":
					fmt.Fprint(w, "[")
				case "/pages/auth":
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, "private body: process-test-secret")
				default:
					t.Errorf("unexpected request: %s", r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			dir := testTempDir(t)
			args := []string{"--repos", tt.repos, "--limit", "2", "--since", "2026-01-01", "--out", "-"}
			if tt.fatal != "" {
				args[len(args)-1] = "report.json"
				args = append(args, "--trace", "trace.jsonl")
				if err := os.WriteFile(filepath.Join(dir, "report.json"), []byte("existing report"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Dir, cmd.Env = dir, cliEnvironment(server.URL)
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
			if code != tt.code || stderr.Len() != 0 || strings.Contains(stdout.String(), "process-test-secret") {
				t.Fatalf("code = %d, want %d; stdout=%s; stderr=%s", code, tt.code, stdout.String(), stderr.String())
			}
			if tt.fatal != "" {
				assertJSON(t, json.RawMessage(stdout.Bytes()), fmt.Sprintf(`{"error":%q}`, tt.fatal))
				contents, err := os.ReadFile(filepath.Join(dir, "report.json"))
				if err != nil || string(contents) != "existing report" {
					t.Fatal("fatal result modified the output file")
				}
				trace, err := os.ReadFile(filepath.Join(dir, "trace.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				records := parseTrace(t, string(trace))
				if len(records) != len(tt.paths) || strings.Contains(string(trace), "process-test-secret") {
					t.Fatal("fatal result lost trace or exposed token")
				}
			} else {
				var result report
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				assertJSON(t, json.RawMessage(stdout.Bytes()), fmt.Sprintf(`{"generated_at":%q,"repos":%s}`,
					result.GeneratedAt, tt.wantRepos))
			}
			var paths []string
			for len(requests) > 0 {
				paths = append(paths, <-requests)
			}
			if !reflect.DeepEqual(paths, tt.paths) {
				t.Fatalf("request order = %v, want %v", paths, tt.paths)
			}
		})
	}
}
