package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func cachedTestClient(t *testing.T, base string) *apiClient {
	t.Helper()
	address, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(config{APIBaseURL: address})
	t.Cleanup(client.closeIdleConnections)
	client.cache, err = newPageCache(testTempDir(t), "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestCacheReplacementAndCorruption(t *testing.T) {
	client := cachedTestClient(t, "http://example.invalid/api/")
	cache := client.cache
	address := client.issuesURL("stub/alpha")
	if entry, err := cache.load(address); err != nil || entry != nil {
		t.Fatalf("missing entry: %+v, %v", entry, err)
	}
	entry := cacheEntry{URL: address.String(), ETag: `W/"first"`, Body: json.RawMessage("[]"), Links: []string{`</next>; rel=next`}}
	for _, etag := range []string{`W/"first"`, `"replacement"`, ""} {
		entry.ETag = etag
		if err := cache.store(entry); err != nil {
			t.Fatal(err)
		}
		got, err := cache.load(address)
		if err != nil || got == nil || got.ETag != etag || string(got.Body) != "[]" || !reflect.DeepEqual(got.Links, entry.Links) {
			t.Fatalf("replacement: %+v, %v", got, err)
		}
	}
	var diagnostic bytes.Buffer
	cache.diagnostic = &diagnostic
	for _, corrupt := range []string{
		`{`, `null`, `{"version":99}`, // broken JSON, missing/unknown format
		fmt.Sprintf(`{"version":1,"url":%q,"body":null}`, address.String()),
		fmt.Sprintf(`{"version":1,"url":%q,"body":[{}]}`, address.String()),
		fmt.Sprintf(`{"version":1,"url":%q,"body":[],"etag":"bad\r\nheader"}`, address.String()),
		fmt.Sprintf(`{"version":1,"url":%q,"redirect":"file:///private"}`, address.String()),
		`{"version":1,"url":"http://wrong.invalid","body":[]}`,
	} {
		diagnostic.Reset()
		if err := os.WriteFile(cache.path(address), []byte(corrupt), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := cache.load(address); err != nil || got != nil || diagnostic.Len() == 0 {
			t.Fatalf("corruption must be a diagnosed miss: %+v, %v, %s", got, err, &diagnostic)
		}
	}
	files, err := os.ReadDir(cache.dir)
	if err != nil || len(files) != 1 || filepath.Ext(files[0].Name()) != ".json" || len(files[0].Name()) != 69 {
		t.Fatalf("unexpected cache files: %v, %v", files, err)
	}
}

func TestCacheSeparatesAPIAddresses(t *testing.T) {
	client := cachedTestClient(t, "http://example.invalid/api/")
	cache := client.cache
	bases := []string{"http://example.invalid/api/", "http://example.invalid/other/", "http://other.invalid/api/"}
	for i, base := range bases {
		client.base, _ = url.Parse(base)
		address := client.issuesURL("stub/alpha")
		if entry, err := cache.load(address); err != nil || entry != nil {
			t.Fatalf("another API's cache was reused: %+v, %v", entry, err)
		}
		if err := cache.store(cacheEntry{URL: address.String(), ETag: fmt.Sprintf(`"api-%d"`, i), Body: json.RawMessage("[]")}); err != nil {
			t.Fatal(err)
		}
	}
	for i, base := range bases {
		client.base, _ = url.Parse(base)
		entry, err := cache.load(client.issuesURL("stub/alpha"))
		if err != nil || entry == nil || entry.ETag != fmt.Sprintf(`"api-%d"`, i) {
			t.Fatalf("API address collision: %+v, %v", entry, err)
		}
	}
}

func TestConditionalPageUpdates(t *testing.T) {
	var validators []string
	client := cachedTestClient(t, "http://example.invalid")
	firstLink, newLink := `</old>; rel=next`, `</new>; rel=next`
	responses := []struct {
		status int
		header http.Header
		body   string
	}{
		{200, http.Header{"Etag": {`W/"one"`}, "Link": {firstLink}}, testPage(testIssue(1, "2026-01-01T00:00:00Z", "original"))},
		{304, http.Header{"Etag": {`"two"`}, "Link": {newLink}}, ""},
		{304, http.Header{}, ""}, // absent metadata must preserve the last update
		{200, http.Header{}, testPage(testIssue(2, "2026-01-02T00:00:00Z", "new body, no validator"))},
		{200, http.Header{}, "[]"},
		{304, http.Header{}, ""}, // no ETag was sent, despite a saved body
	}
	calls := 0
	client.transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if calls >= len(responses) {
			t.Fatal("unexpected request")
		}
		response := responses[calls]
		calls++
		validators = append(validators, req.Header.Get("If-None-Match"))
		return &http.Response{StatusCode: response.status, Header: response.header, Body: io.NopCloser(strings.NewReader(response.body))}, nil
	})
	var trace bytes.Buffer
	client.trace = json.NewEncoder(&trace)
	for i := range responses {
		page, err := client.fetchPage(client.issuesURL("stub/alpha"))
		if i == len(responses)-1 {
			if !errors.Is(err, repoInvalidResponse) {
				t.Fatalf("unjustified 304: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 && (len(page.issues) != 1 || page.issues[0].Title != "original") {
			t.Fatalf("cached body changed: %+v", page)
		}
		if i == 1 || i == 2 {
			if !reflect.DeepEqual(page.links, []string{newLink}) {
				t.Fatalf("304 metadata not updated/preserved: %v", page.links)
			}
		}
		if i == 3 && (page.issues[0].Number != 2 || len(page.links) != 0) {
			t.Fatalf("200 kept stale data: %+v", page)
		}
	}
	if want := []string{"", `W/"one"`, `"two"`, `"two"`, "", ""}; !reflect.DeepEqual(validators, want) {
		t.Fatalf("validators=%v, want=%v", validators, want)
	}
	records := parseTrace(t, trace.String())
	if len(records) != len(responses) {
		t.Fatal("trace missed requests")
	}
	for i, record := range records {
		if record.Status != responses[i].status || record.FromCache != (i == 1 || i == 2) || record.Attempt != 1 ||
			(i < 5 && record.Error != nil) || (i == 5 && (record.Error == nil || *record.Error != "invalid_response")) {
			t.Fatalf("conditional trace[%d]: %+v", i, record)
		}
	}
}

func TestCacheDoesNotMaskFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   error
		calls  int
	}{
		{"server failure", 503, "", repoRequestFailed, 4},
		{"network failure", 0, "", repoRequestFailed, 4},
		{"unauthorized", 401, "", errUnauthorized, 1},
		{"invalid fresh page", 200, "[", repoInvalidResponse, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := cachedTestClient(t, "http://example.invalid")
			address := client.issuesURL("stub/alpha")
			if err := client.cache.store(cacheEntry{URL: address.String(), ETag: `"saved"`, Body: json.RawMessage("[]")}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			client.sleep = func(time.Duration) {}
			client.transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Header.Get("If-None-Match") != `"saved"` {
					t.Fatal("validator missing on retry")
				}
				if tt.status == 0 {
					return nil, errors.New("network")
				}
				return &http.Response{StatusCode: tt.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})
			if _, err := client.fetchPage(address); !errors.Is(err, tt.want) || calls != tt.calls {
				t.Fatalf("cache masked error: %v, calls=%d", err, calls)
			}
			entry, err := client.cache.load(address)
			if err != nil || entry == nil || entry.ETag != `"saved"` || string(entry.Body) != "[]" {
				t.Fatalf("failed response replaced good cache: %+v, %v", entry, err)
			}
		})
	}
}

func TestCacheIOFailures(t *testing.T) {
	client := cachedTestClient(t, "http://example.invalid")
	address := client.issuesURL("stub/alpha")
	if err := os.WriteFile(filepath.Join(client.cache.dir, "file"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newPageCache(filepath.Join(client.cache.dir, "file", "child"), "", io.Discard); !errors.Is(err, errCache) {
		t.Fatalf("cache creation error: %v", err)
	}
	// A directory at the hashed file path fails both reads and replacement,
	// without relying on permission semantics of root or Windows chmod.
	if err := os.Mkdir(client.cache.path(address), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.cache.load(address); !errors.Is(err, errCache) {
		t.Fatalf("cache read error: %v", err)
	}
	if err := client.cache.store(cacheEntry{URL: address.String(), Body: json.RawMessage("[]")}); !errors.Is(err, errCache) {
		t.Fatalf("cache replacement error: %v", err)
	}
	files, err := os.ReadDir(client.cache.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".tmp") {
			t.Fatal("failed replacement left temporary file")
		}
	}
}

func TestCacheAvoidsReflectedToken(t *testing.T) {
	client := cachedTestClient(t, "http://example.invalid")
	client.cache.token = "private-test-token"
	var diagnostic bytes.Buffer
	client.cache.diagnostic = &diagnostic
	address := client.issuesURL("stub/alpha")
	entry := cacheEntry{URL: address.String(), ETag: `"old"`, Body: json.RawMessage("[]")}
	if err := client.cache.store(entry); err != nil {
		t.Fatal(err)
	}
	entry.Links = []string{`</page?cursor=private-test-token>; rel=next`}
	if err := client.cache.store(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(client.cache.path(address)); !errors.Is(err, os.ErrNotExist) ||
		diagnostic.Len() == 0 || strings.Contains(diagnostic.String(), client.cache.token) {
		t.Fatalf("reflected token persisted or leaked: %v, %s", err, &diagnostic)
	}
}

func TestCachedRedirectKeepsValidatorsAtTheirURL(t *testing.T) {
	requests := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path + ":" + r.Header.Get("If-None-Match")
		if r.URL.Path == "/repos/stub/alpha/issues" {
			w.Header().Set("Location", "/moved")
			w.WriteHeader(301)
		} else {
			w.Header().Set("ETag", `"target"`)
			fmt.Fprint(w, "[]")
		}
	}))
	defer server.Close()
	client := cachedTestClient(t, server.URL)
	address := client.issuesURL("stub/alpha")
	if err := client.cache.store(cacheEntry{URL: address.String(), ETag: `"source"`, Body: json.RawMessage("[]")}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.fetchPage(address); err != nil {
		t.Fatal(err)
	}
	// Simulate another process with no in-memory permanent redirect map.
	client.permanent = make(map[string]*url.URL)
	if _, err := client.fetchPage(address); err != nil {
		t.Fatal(err)
	}
	var got []string
	for len(requests) > 0 {
		got = append(got, <-requests)
	}
	want := []string{`/repos/stub/alpha/issues:"source"`, "/moved:", `/moved:"target"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redirect validators=%v, want=%v", got, want)
	}
}
