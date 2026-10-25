package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

func TestValidETag(t *testing.T) {
	for _, value := range []string{"", `""`, `"strong"`, `W/"weak"`, `"!#$~\"`, "\"\xc3\xa9\""} {
		if !validETag(value) {
			t.Fatalf("valid ETag rejected: %q", value)
		}
	}
	for _, value := range []string{"unquoted", "*", `w/"lowercase"`, `W/`, `"unterminated`, `"a"b"`, `"has space"`} {
		if validETag(value) {
			t.Fatalf("invalid ETag accepted: %q", value)
		}
	}
	for code := 0; code < 128; code++ {
		if code >= 0x20 && code != 0x7f {
			continue
		}
		if value := "\"bad" + string(rune(code)) + "\""; validETag(value) {
			t.Fatalf("control character accepted in ETag: %q", value)
		}
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

func TestCacheDoesNotMaskNetworkFailure(t *testing.T) {
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
		return nil, errors.New("network")
	})
	if _, err := client.fetchPage(address); !errors.Is(err, repoRequestFailed) || calls != 4 {
		t.Fatalf("cache masked error: %v, calls=%d", err, calls)
	}
	entry, err := client.cache.load(address)
	if err != nil || entry == nil || entry.ETag != `"saved"` || string(entry.Body) != "[]" {
		t.Fatalf("failed response replaced good cache: %+v, %v", entry, err)
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

func TestCacheTokenCheckPreservesUnknownNumbers(t *testing.T) {
	client := cachedTestClient(t, "http://example.invalid")
	client.cache.token = "synthetic-review-token"
	address := client.issuesURL("stub/alpha")
	body := strings.TrimSuffix(testIssue(1, "2026-01-01T00:00:00Z", "safe"), "}") + `,"metadata":1e1000}`
	entry := cacheEntry{URL: address.String(), Body: json.RawMessage(testPage(body))}
	if err := client.cache.store(entry); err != nil {
		t.Fatal(err)
	}
	if got, err := client.cache.load(address); err != nil || got == nil || !bytes.Equal(got.Body, entry.Body) {
		t.Fatalf("unknown numeric value was changed or rejected: %+v, %v", got, err)
	}
}
