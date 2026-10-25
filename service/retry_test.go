package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type testResponseBody struct {
	io.Reader
	readErr error
	onClose func()
}

func (b *testResponseBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.Reader.Read(p)
}

func (b *testResponseBody) Close() error {
	b.onClose()
	return nil
}

func parseTrace(t *testing.T, text string) []traceRecord {
	t.Helper()
	var records []traceRecord
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &fields); err != nil || len(fields) != 7 {
			t.Fatalf("invalid trace schema: %s, %v", scanner.Text(), err)
		}
		for _, key := range []string{"ts", "method", "url", "status", "attempt", "from_cache", "error"} {
			if _, exists := fields[key]; !exists {
				t.Fatalf("missing trace field %s", key)
			}
		}
		var record traceRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if _, err := time.Parse(time.RFC3339, record.TS); err != nil || !strings.HasSuffix(record.TS, "Z") || record.Method != "GET" {
			t.Fatalf("invalid trace timestamp or method: %+v", record)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestRequestRetriesAndTrace(t *testing.T) {
	type response struct {
		status  int
		header  http.Header
		body    string
		network error
		readErr error
	}
	ok := response{status: 200, body: "[]"}
	failure := response{status: 503, body: "private-test-secret"}
	network := response{network: errors.New("private-test-secret")}
	rate := response{status: 429, header: http.Header{"Retry-After": {"2"}}}
	rateFallback := response{status: 403, body: `{"message":"API rate limit exceeded"}`}
	for _, tt := range []struct {
		name       string
		responses  []response
		waits      []time.Duration
		want       error
		traceError []string
	}{
		{"transient recovery", []response{network, failure, failure, ok}, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, nil,
			[]string{"network", "http_503", "http_503", ""}},
		{"transient exhaustion", []response{network, network, network, network}, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, repoRequestFailed,
			[]string{"network", "network", "network", "network"}},
		{"body timeout keeps HTTP status", []response{{status: 200, readErr: context.DeadlineExceeded}, ok}, []time.Duration{time.Second}, nil,
			[]string{"timeout", ""}},
		{"rate recovery", []response{rate, ok}, []time.Duration{2 * time.Second}, nil,
			[]string{"rate_limited", ""}},
		{"rate headers do not depend on body", []response{{status: 429, header: rate.header, readErr: io.ErrUnexpectedEOF}, ok}, []time.Duration{2 * time.Second}, nil,
			[]string{"rate_limited", ""}},
		{"rate exhaustion", []response{rateFallback, rateFallback, rateFallback}, []time.Duration{time.Minute, 2 * time.Minute}, repoRateLimited,
			[]string{"rate_limited", "rate_limited", "rate_limited"}},
		{"mixed counters stay finite", []response{failure, rate, failure, rate, failure, failure},
			[]time.Duration{time.Second, 2 * time.Second, 2 * time.Second, 2 * time.Second, 4 * time.Second}, repoRequestFailed,
			[]string{"http_503", "rate_limited", "http_503", "rate_limited", "http_503", "http_503"}},
		{"mixed rate counter stays finite", []response{rate, failure, rate, failure, rate},
			[]time.Duration{2 * time.Second, time.Second, 2 * time.Second, 2 * time.Second}, repoRateLimited,
			[]string{"rate_limited", "http_503", "rate_limited", "http_503", "rate_limited"}},
		{"401 is immediate", []response{{status: 401, body: "private-test-secret", readErr: io.ErrUnexpectedEOF}}, nil, errUnauthorized,
			[]string{"http_401"}},
		{"404 is immediate", []response{{status: 404, readErr: io.ErrUnexpectedEOF}}, nil, repoNotFound, []string{"http_404"}},
		{"plain 403 is not retried", []response{{status: 403}}, nil, repoRequestFailed, []string{"http_403"}},
		{"JSON error is not retried", []response{{status: 200, body: "["}}, nil, repoInvalidResponse, []string{"invalid_response"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base, err := url.Parse("http://test.invalid")
			if err != nil {
				t.Fatal(err)
			}
			client := newAPIClient(config{APIBaseURL: base, Token: "private-test-secret"})
			clock := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
			client.now = func() time.Time { return clock }
			var waits []time.Duration
			openBodies, calls := 0, 0
			client.sleep = func(delay time.Duration) {
				if openBodies != 0 {
					t.Fatal("response body remained open during wait")
				}
				waits = append(waits, delay)
				clock = clock.Add(delay)
			}
			client.transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if openBodies != 0 || calls == len(tt.responses) {
					t.Fatal("unclosed body or retry budget exceeded")
				}
				fixture := tt.responses[calls]
				calls++
				if fixture.network != nil {
					return nil, fixture.network
				}
				openBodies++
				return &http.Response{
					StatusCode: fixture.status, Header: fixture.header,
					Body: &testResponseBody{Reader: strings.NewReader(fixture.body), readErr: fixture.readErr, onClose: func() { openBodies-- }},
				}, nil
			})
			var trace bytes.Buffer
			client.trace = json.NewEncoder(&trace)
			_, err = client.fetchPage(client.issuesURL("stub/alpha"))
			if !errors.Is(err, tt.want) || calls != len(tt.responses) || openBodies != 0 || !reflect.DeepEqual(waits, tt.waits) {
				t.Fatalf("error=%v, calls=%d, open bodies=%d, waits=%v; want error=%v, waits=%v", err, calls, openBodies, waits, tt.want, tt.waits)
			}
			if strings.Contains(trace.String(), "private-test-secret") || strings.Contains(trace.String(), "Authorization") {
				t.Fatal("secret leaked into trace")
			}
			records := parseTrace(t, trace.String())
			if len(records) != calls {
				t.Fatalf("trace records=%d, requests=%d", len(records), calls)
			}
			for i, record := range records {
				description := ""
				if record.Error != nil {
					description = *record.Error
				}
				if record.Status != tt.responses[i].status || record.Attempt != i+1 || record.FromCache ||
					record.URL != client.issuesURL("stub/alpha").String() || description != tt.traceError[i] {
					t.Fatalf("unexpected trace record: %+v", record)
				}
			}
		})
	}
}

func TestRateLimitDetection(t *testing.T) {
	for _, tt := range []struct {
		status int
		header http.Header
		body   string
		want   bool
	}{
		{403, http.Header{"Retry-After": {""}}, "", true},
		{429, http.Header{"Retry-After": {"1"}}, "", true},
		{403, http.Header{"X-Ratelimit-Remaining": {"0"}}, "", true},
		{403, nil, `{"message":"You have exceeded a secondary rate limit"}`, true},
		{429, nil, "API rate limit exceeded", true},
		{403, nil, `{"message":"Forbidden","documentation_url":"https://example.com/rate-limits"}`, false},
		{429, nil, "Forbidden", false},
		{500, http.Header{"Retry-After": {"1"}}, "rate limit exceeded", false},
	} {
		if got := isRateLimited(tt.status, tt.header, []byte(tt.body)); got != tt.want {
			t.Fatalf("status=%d, headers=%v, body=%s: limited=%v, want %v", tt.status, tt.header, tt.body, got, tt.want)
		}
	}
}

func TestRateDelay(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	reset := strconv.FormatInt(now.Add(7*time.Second).Unix(), 10)
	for _, tt := range []struct {
		name    string
		retry   string
		reset   string
		retries int
		want    time.Duration
	}{
		{"seconds take precedence", "3", "9999999999", 0, 3 * time.Second},
		{"HTTP date", now.Add(5 * time.Second).Format(http.TimeFormat), "", 0, 5 * time.Second},
		{"expired date", now.Add(-time.Second).Format(http.TimeFormat), "", 0, 0},
		{"zero retry", "0", "9999999999", 0, 0},
		{"reset epoch", "invalid", reset, 0, 7 * time.Second},
		{"negative retry uses reset", "-1", reset, 0, 7 * time.Second},
		{"past reset", "", "0", 0, 0},
		{"first fallback", "invalid", "invalid", 0, time.Minute},
		{"second fallback", "", "", 1, 2 * time.Minute},
		{"overflow retry", "9223372036854775807", reset, 0, 7 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{"Retry-After": {tt.retry}, "X-Ratelimit-Reset": {tt.reset}}
			if got := rateDelay(header, now, tt.retries); got != tt.want {
				t.Fatalf("delay=%s, want %s", got, tt.want)
			}
		})
	}
}
