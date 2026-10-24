package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func testCLICache(t *testing.T, binary string) {
	t.Run("persistent pages and redirect", func(t *testing.T) {
		requests := make(chan string, 10)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- r.URL.RequestURI() + ":" + r.Header.Get("If-None-Match")
			if r.Header.Get("Authorization") != "Bearer process-test-secret" {
				t.Error("missing authorization")
			}
			if r.URL.Path == "/api/repos/stub/alpha/issues" {
				w.Header().Set("Location", "/api/moved?cursor=first")
				w.WriteHeader(301)
				return
			}
			first := r.URL.RequestURI() == "/api/moved?cursor=first"
			if !first && r.URL.RequestURI() != "/api/moved?cursor=a%2Cb&keep=yes" {
				t.Errorf("wrong opaque pagination URL: %s", r.URL)
				w.WriteHeader(404)
				return
			}
			etag := `"last"`
			if first {
				etag = `W/"first"`
			}
			if r.Header.Get("If-None-Match") == etag {
				// Deliberately omit ETag and Link: both must survive in the cache.
				w.WriteHeader(304)
				return
			}
			if r.Header.Get("If-None-Match") != "" {
				t.Errorf("validator crossed URLs: %s", r.Header.Get("If-None-Match"))
			}
			w.Header().Set("ETag", etag)
			if first {
				w.Header().Set("Link", `<?cursor=a%2Cb&keep=yes>; rel=next`)
				fmt.Fprint(w, testPage(
					testIssue(1, "2026-01-01T00:00:00Z", "first"),
					testIssue(2, "2026-01-02T00:00:00Z", "second"),
					`{"pull_request":null}`))
			} else {
				fmt.Fprint(w, testPage(testIssue(3, "2026-01-03T00:00:00Z", "third")))
			}
		}))
		defer server.Close()
		dir := testTempDir(t)
		cacheDir := filepath.Join(dir, "общий кэш")
		tracePath := filepath.Join(dir, "trace.jsonl")
		var firstReport report
		for run := 0; run < 3; run++ {
			args := []string{"--repos", "stub/alpha", "--cache-dir", cacheDir, "--trace", tracePath}
			if run == 2 {
				args = append(args, "--since", "2026-01-02", "--limit", "1")
			}
			code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL+"/api/", args...)
			if code != 0 || stderr != "" {
				t.Fatalf("run %d: code=%d, stdout=%s, stderr=%s", run, code, stdout, stderr)
			}
			var result report
			if err := json.Unmarshal(stdout, &result); err != nil || len(result.Repos) != 1 {
				t.Fatalf("report: %s, %v", stdout, err)
			}
			if run == 0 {
				firstReport = result
				if result.Repos[0].Count != 3 {
					t.Fatalf("first report: %+v", result)
				}
			} else if run == 1 {
				if !reflect.DeepEqual(firstReport.Repos, result.Repos) {
					t.Fatalf("cached report differs: %+v", result)
				}
			} else if result.Repos[0].Count != 1 || result.Repos[0].Issues[0].Number != 2 {
				t.Fatalf("cache contains filtered data: %+v", result)
			}
			trace, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatal(err)
			}
			records := parseTrace(t, string(trace))
			wantStatuses := []int{301, 200, 200}
			if run == 1 {
				wantStatuses = []int{304, 304}
			} else if run == 2 {
				wantStatuses = []int{304}
			}
			if len(records) != len(wantStatuses) {
				t.Fatalf("trace: %+v", records)
			}
			for i, record := range records {
				if record.Status != wantStatuses[i] || record.FromCache != (run > 0) || record.Error != nil || record.Attempt != 1 {
					t.Fatalf("trace: %+v", record)
				}
			}
			if strings.Contains(string(stdout)+stderr+string(trace), "process-test-secret") {
				t.Fatal("credential in output")
			}
		}
		var got []string
		for len(requests) > 0 {
			got = append(got, <-requests)
		}
		want := []string{
			"/api/repos/stub/alpha/issues?direction=asc&per_page=100&sort=created&state=open:",
			"/api/moved?cursor=first:", "/api/moved?cursor=a%2Cb&keep=yes:",
			`/api/moved?cursor=first:W/"first"`, `/api/moved?cursor=a%2Cb&keep=yes:"last"`,
			`/api/moved?cursor=first:W/"first"`,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("requests=%v, want=%v", got, want)
		}
		files, err := os.ReadDir(cacheDir)
		if err != nil || len(files) != 3 {
			t.Fatalf("cache files=%v, %v", files, err)
		}
		for _, file := range files {
			data, err := os.ReadFile(filepath.Join(cacheDir, file.Name()))
			if err != nil || strings.Contains(string(data), "process-test-secret") || strings.Contains(string(data), "Authorization") || !json.Valid(data) {
				t.Fatalf("invalid or unsafe cache file: %s, %v", file.Name(), err)
			}
		}
	})

	t.Run("corrupted entry is refreshed", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.Header.Get("If-None-Match") != "" {
				t.Error("corrupt cache produced a conditional request")
			}
			w.Header().Set("ETag", `"good"`)
			fmt.Fprint(w, "[]")
		}))
		defer server.Close()
		dir := testTempDir(t)
		cacheDir := filepath.Join(dir, "cache")
		for run := 0; run < 2; run++ {
			code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL,
				"--repos", "stub/alpha", "--cache-dir", cacheDir)
			if code != 0 || (run == 0 && stderr != "") || (run == 1 && !strings.Contains(stderr, "повреждённая запись кэша")) {
				t.Fatalf("run %d: code=%d, stdout=%s, stderr=%s", run, code, stdout, stderr)
			}
			files, err := os.ReadDir(cacheDir)
			if err != nil || len(files) != 1 {
				t.Fatalf("cache files=%v, %v", files, err)
			}
			path := filepath.Join(cacheDir, files[0].Name())
			if run == 0 {
				if err := os.WriteFile(path, []byte("[private-test-secret"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil || !json.Valid(data) || strings.Contains(stderr, "private-test-secret") {
					t.Fatalf("cache not repaired or leaked: %v", err)
				}
			}
		}
		if requests.Load() != 2 {
			t.Fatalf("requests=%d", requests.Load())
		}
	})

	t.Run("cache I/O failure is fatal", func(t *testing.T) {
		dir := testTempDir(t)
		cacheDir := filepath.Join(dir, "cache")
		if err := os.WriteFile(cacheDir, []byte("not a directory"), 0600); err != nil {
			t.Fatal(err)
		}
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			// Remove the empty cache directory while the response is in flight.
			if err := os.Remove(cacheDir); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(cacheDir, []byte("preserve"), 0600); err != nil {
				t.Error(err)
			}
			fmt.Fprint(w, "[]")
		}))
		defer server.Close()
		args := []string{"--repos", "stub/alpha,stub/next", "--cache-dir", cacheDir}
		code, stdout, stderr := runCLIProcess(t, binary, dir, server.URL, args...)
		if code != 3 || len(stdout) != 0 || stderr == "" || requests.Load() != 0 {
			t.Fatalf("open failure: code=%d stdout=%s stderr=%s requests=%d", code, stdout, stderr, requests.Load())
		}
		if err := os.Remove(cacheDir); err != nil {
			t.Fatal(err)
		}
		tracePath := filepath.Join(dir, "trace.jsonl")
		args = append(args, "--trace", tracePath)
		code, stdout, stderr = runCLIProcess(t, binary, dir, server.URL, args...)
		if code != 3 || len(stdout) != 0 || stderr == "" || requests.Load() != 1 || strings.Contains(stderr, "process-test-secret") {
			t.Fatalf("write failure: code=%d stdout=%s stderr=%s requests=%d", code, stdout, stderr, requests.Load())
		}
		trace, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		records := parseTrace(t, string(trace))
		if len(records) != 1 || records[0].Status != 200 || records[0].Error == nil || *records[0].Error != "cache" {
			t.Fatalf("cache failure trace: %+v", records)
		}
	})
}
