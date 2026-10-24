package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func testCLIFilesAndCombined(t *testing.T, binary string) {
	t.Run("conflicting outputs preserve files", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			fmt.Fprint(w, "[]")
		}))
		defer server.Close()
		for _, kind := range []string{"new", "relative alias", "hard link", "symlink parent"} {
			t.Run(kind, func(t *testing.T) {
				dir := testTempDir(t)
				out, trace := "result.json", "result.json"
				if kind == "symlink parent" {
					if runtime.GOOS == "windows" {
						t.Skip("POSIX symlink/.. path semantics")
					}
					for _, path := range []string{"base", "other/child"} {
						if err := os.MkdirAll(filepath.Join(dir, path), 0700); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink("../other/child", filepath.Join(dir, "base", "link")); err != nil {
						t.Fatal(err)
					}
					out, trace = "base/link/../result.json", "other/result.json"
				}
				if kind != "new" {
					if err := os.WriteFile(dir+string(os.PathSeparator)+out, []byte("existing contents"), 0600); err != nil {
						t.Fatal(err)
					}
					if kind == "relative alias" {
						trace = "./result.json"
					}
					if kind == "hard link" {
						trace = "alias.json"
						if err := os.Link(filepath.Join(dir, out), filepath.Join(dir, trace)); err != nil {
							t.Skipf("hard links unavailable: %v", err)
						}
					}
				}
				code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL,
					"--repos", "stub/alpha", "--out", out, "--trace", trace)
				if code != 2 || len(stdout) != 0 || !strings.Contains(stderr, "разные файлы") || requests.Load() != 0 {
					t.Fatalf("conflict: code=%d stdout=%s stderr=%s requests=%d", code, stdout, stderr, requests.Load())
				}
				if _, err := os.Stat(filepath.Join(dir, "ghreport-cache")); !os.IsNotExist(err) {
					t.Fatalf("conflict created cache: %v", err)
				}
				for _, path := range []string{out, trace} {
					data, err := os.ReadFile(dir + string(os.PathSeparator) + path)
					if kind == "new" {
						if !os.IsNotExist(err) {
							t.Fatalf("conflict created output: %v", err)
						}
					} else if err != nil || string(data) != "existing contents" {
						t.Fatalf("conflict changed existing output: %s, %v", data, err)
					}
				}
			})
		}
	})

	t.Run("reflected token cancels report", func(t *testing.T) {
		for _, tt := range []struct {
			name   string
			toFile bool
			cached bool
		}{
			{"stdout", false, false}, {"file", true, false}, {"cached file", true, true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				body := testPage(testIssue(1, "2026-01-01T00:00:00Z", "contains process-test-secret"))
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Header.Get("Authorization") != "Bearer process-test-secret" {
						t.Error("missing configured token")
					}
					if tt.cached {
						if r.Header.Get("If-None-Match") != `"saved"` {
							t.Error("304 regression did not make a conditional request")
						}
						w.WriteHeader(304)
					} else {
						w.Header().Set("ETag", `"saved"`)
						fmt.Fprint(w, body)
					}
				}))
				defer server.Close()
				dir := testTempDir(t)
				cacheDir := filepath.Join(dir, "cache")
				outPath, tracePath := filepath.Join(dir, "report.json"), filepath.Join(dir, "trace.jsonl")
				if err := os.WriteFile(outPath, []byte("existing report"), 0600); err != nil {
					t.Fatal(err)
				}
				if tt.cached {
					// Simulate a page saved previously with another credential.
					cfg, err := parseConfig([]string{"--repos", "stub/alpha"}, envFrom(map[string]string{"GITHUB_API_BASE_URL": server.URL}))
					if err != nil {
						t.Fatal(err)
					}
					client := newAPIClient(cfg)
					t.Cleanup(client.closeIdleConnections)
					cache, err := newPageCache(cacheDir, "", nil)
					if err != nil {
						t.Fatal(err)
					}
					if err := cache.store(cacheEntry{URL: client.issuesURL("stub/alpha").String(), ETag: `"saved"`, Body: json.RawMessage(body)}); err != nil {
						t.Fatal(err)
					}
				}
				out := "-"
				if tt.toFile {
					out = outPath
				}
				code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL, "--repos", "stub/alpha",
					"--out", out, "--trace", tracePath, "--cache-dir", cacheDir)
				if code != 3 || len(stdout) != 0 || !strings.Contains(stderr, "вывод отменён") ||
					strings.Contains(stderr, "process-test-secret") || requests.Load() != 1 {
					t.Fatalf("unsafe report: code=%d stdout=%s stderr=%s requests=%d", code, stdout, stderr, requests.Load())
				}
				data, err := os.ReadFile(outPath)
				if err != nil || string(data) != "existing report" {
					t.Fatal("unsafe result changed the existing report file")
				}
				trace, err := os.ReadFile(tracePath)
				if err != nil {
					t.Fatal(err)
				}
				records := parseTrace(t, string(trace))
				status := 200
				if tt.cached {
					status = 304
				}
				if len(records) != 1 || records[0].Status != status || records[0].FromCache != tt.cached ||
					strings.Contains(string(trace), "process-test-secret") {
					t.Fatalf("unsafe trace: %+v", records)
				}
				files, err := os.ReadDir(cacheDir)
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					data, err := os.ReadFile(filepath.Join(cacheDir, file.Name()))
					if err != nil || strings.Contains(string(data), "process-test-secret") {
						t.Fatal("reflected token remained in cache")
					}
				}
			})
		}
	})

	t.Run("file report with cache retries and partial failure", func(t *testing.T) {
		var firstCalls atomic.Int32
		requests := make(chan string, 16)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- r.URL.Path + ":" + r.Header.Get("If-None-Match")
			switch r.URL.Path {
			case "/repos/stub/good/issues":
				if firstCalls.Add(1) == 1 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(429)
					fmt.Fprint(w, `{"message":"process-test-secret"}`)
					return
				}
				if r.Header.Get("If-None-Match") == `W/"first"` {
					w.WriteHeader(304)
					return
				}
				w.Header().Set("ETag", `W/"first"`)
				w.Header().Set("Link", `</page/two?cursor=opaque>; rel=next`)
				fmt.Fprint(w, testPage(`{"pull_request":null}`,
					testIssue(99, "2025-12-31T23:59:59Z", "old"),
					testIssue(2, "2026-01-02T00:00:00Z", "first version")))
			case "/page/two":
				if r.Header.Get("If-None-Match") == `"last"` {
					w.WriteHeader(304)
					return
				}
				w.Header().Set("ETag", `"last"`)
				w.Header().Set("Link", `</must-not-be-requested>; rel=next`)
				fmt.Fprint(w, testPage(testIssue(2, "2026-01-02T00:00:00Z", "duplicate"),
					testIssue(1, "2026-01-02T03:00:00+03:00", "same instant")))
			case "/repos/stub/missing/issues":
				w.WriteHeader(404)
				fmt.Fprint(w, "private body process-test-secret")
			default:
				t.Errorf("unexpected request: %s", r.URL)
				w.WriteHeader(404)
			}
		}))
		defer server.Close()
		dir := testTempDir(t)
		var previous []repoReport
		for run := 0; run < 2; run++ {
			tracePath := filepath.Join(dir, "trace.jsonl")
			outPath := filepath.Join(dir, "report.json")
			code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL,
				"--repos", "stub/good,stub/missing", "--limit", "2", "--since", "2026-01-01",
				"--out", outPath, "--trace", tracePath)
			if code != 1 || len(stdout) != 0 || stderr != "" {
				t.Fatalf("combined: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			data, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatal(err)
			}
			var result report
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			assertJSON(t, result.Repos, `[
				{"repo":"stub/good","error":null,"count":2,"issues":[
					{"number":1,"title":"same instant","author":"author","labels":[],"created_at":"2026-01-02T03:00:00+03:00","url":"https://example.com/issue/1"},
					{"number":2,"title":"first version","author":"author","labels":[],"created_at":"2026-01-02T00:00:00Z","url":"https://example.com/issue/2"}]},
				{"repo":"stub/missing","error":"not_found","count":0,"issues":[]}
			]`)
			if run == 1 && !reflect.DeepEqual(previous, result.Repos) {
				t.Fatal("cached partial report differs")
			}
			previous = result.Repos
			trace, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatal(err)
			}
			records := parseTrace(t, string(trace))
			statuses, attempts := []int{429, 200, 200, 404}, []int{1, 2, 1, 1}
			if run == 1 {
				statuses, attempts = []int{304, 304, 404}, []int{1, 1, 1}
			}
			if len(records) != len(statuses) {
				t.Fatalf("trace: %+v", records)
			}
			for i, record := range records {
				if record.Status != statuses[i] || record.Attempt != attempts[i] || record.FromCache != (statuses[i] == 304) ||
					(record.Error != nil) != (statuses[i] == 429 || statuses[i] == 404) {
					t.Fatalf("combined trace: %+v", record)
				}
			}
			if strings.Contains(string(data)+string(trace)+stderr, "process-test-secret") {
				t.Fatal("secret leaked into output")
			}
		}
		var got []string
		for len(requests) > 0 {
			got = append(got, <-requests)
		}
		want := []string{"/repos/stub/good/issues:", "/repos/stub/good/issues:", "/page/two:", "/repos/stub/missing/issues:",
			`/repos/stub/good/issues:W/"first"`, `/page/two:"last"`, "/repos/stub/missing/issues:"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("combined request order: %v", got)
		}
	})

	t.Run("report open failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "[]")
		}))
		defer server.Close()
		dir := testTempDir(t)
		tracePath := filepath.Join(dir, "trace.jsonl")
		code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL,
			"--repos", "stub/good", "--out", dir, "--trace", tracePath)
		if code != 3 || len(stdout) != 0 || stderr == "" || strings.Contains(stderr, "process-test-secret") {
			t.Fatalf("output failure: code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
		trace, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		if records := parseTrace(t, string(trace)); len(records) != 1 || records[0].Status != 200 || records[0].Error != nil {
			t.Fatalf("completed HTTP trace lost: %+v", records)
		}
	})
}
