package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPIClientBodyTimeout(t *testing.T) {
	headersSent := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 1 {
			fmt.Fprint(w, "[]")
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(headersSent)
		<-r.Context().Done()
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := newAPIClient(config{APIBaseURL: base})
	defer client.closeIdleConnections()
	if client.timeout != 30*time.Second {
		t.Fatalf("request timeout = %s, want 30s", client.timeout)
	}
	client.timeout = 250 * time.Millisecond
	var waits []time.Duration
	client.sleep = func(delay time.Duration) { waits = append(waits, delay) }
	if _, err := client.fetchPage(client.issuesURL("cli/cli")); err != nil {
		t.Fatalf("body timeout should recover on retry: %v", err)
	}
	if requests.Load() != 2 || !reflect.DeepEqual(waits, []time.Duration{time.Second}) {
		t.Fatalf("timeout retry: requests=%d, waits=%v", requests.Load(), waits)
	}
	select {
	case <-headersSent:
	default:
		t.Fatal("timeout occurred before response headers, not during body reading")
	}
}

func TestAPIClientErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"server failure", 500, "private error body", repoRequestFailed},
		{"unconditional 304", 304, "", repoInvalidResponse},
		{"wrong structure", 200, "null", repoInvalidResponse},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			base, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := newAPIClient(config{APIBaseURL: base})
			client.sleep = func(time.Duration) {}
			defer client.closeIdleConnections()
			if _, err := client.fetchPage(client.issuesURL("cli/cli")); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}
