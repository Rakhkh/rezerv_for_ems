package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type failingTraceWriter struct{}

func (failingTraceWriter) Write([]byte) (int, error) {
	return 0, errors.New("private writer error")
}

func TestTraceFailureStopsRequests(t *testing.T) {
	base, err := url.Parse("http://test.invalid")
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(config{APIBaseURL: base})
	client.trace = json.NewEncoder(failingTraceWriter{})
	calls := 0
	client.transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	client.sleep = func(time.Duration) { t.Fatal("trace failure must stop before waiting") }
	if _, err := client.fetchPage(client.issuesURL("stub/alpha")); !errors.Is(err, errTrace) || calls != 1 {
		t.Fatalf("trace error=%v, requests=%d", err, calls)
	}
}

func TestTraceRedactsTokenInURL(t *testing.T) {
	address, err := url.Parse("http://test.invalid/private-token?cursor=private-token")
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(config{APIBaseURL: address, Token: "private-token"})
	var trace bytes.Buffer
	client.trace = json.NewEncoder(&trace)
	if err := client.recordAttempt(time.Now(), address, 200, 1, false, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(trace.String(), "private-token") {
		t.Fatal("token reflected in URL leaked into trace")
	}
	if records := parseTrace(t, trace.String()); len(records) != 1 || !strings.Contains(records[0].URL, "[REDACTED]") {
		t.Fatal("missing redacted URL trace")
	}
}
