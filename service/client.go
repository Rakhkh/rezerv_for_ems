package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var errUnauthorized = errors.New(fatalUnauthorized)

type apiClient struct {
	base      *url.URL
	token     string
	transport http.RoundTripper
	timeout   time.Duration
	now       func() time.Time
	sleep     func(time.Duration)
	trace     *json.Encoder
	permanent map[string]*url.URL
}

func newAPIClient(cfg config) *apiClient {
	return &apiClient{
		base: cfg.APIBaseURL, token: cfg.Token,
		now: time.Now, sleep: time.Sleep,
		permanent: make(map[string]*url.URL),
		transport: http.DefaultTransport.(*http.Transport).Clone(),
		timeout:   30 * time.Second,
	}
}

func (c *apiClient) closeIdleConnections() {
	if transport, ok := c.transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}

func (c *apiClient) issuesURL(repo string) *url.URL {
	owner, name, _ := strings.Cut(repo, "/")
	address := c.base.JoinPath("repos", url.PathEscape(owner), url.PathEscape(name), "issues")
	address.RawQuery = url.Values{
		"state": {"open"}, "sort": {"created"},
		"direction": {"asc"}, "per_page": {"100"},
	}.Encode()
	return address
}

type issuePage struct {
	issues  []issue
	links   []string
	address *url.URL
}

func (c *apiClient) fetchPage(address *url.URL) (issuePage, error) {
	visited := make(map[string]bool)
	redirects := 0
	for {
		key := address.String()
		if visited[key] {
			return issuePage{}, repoRequestFailed
		}
		visited[key] = true
		target := c.permanent[key]
		if target == nil {
			response, err := c.requestWithRetries(address)
			if err != nil {
				return issuePage{}, err
			}
			target = response.redirect
			if target == nil {
				return issuePage{issues: response.issues, links: response.header.Values("Link"), address: address}, nil
			}
			if response.status == http.StatusMovedPermanently && !visited[target.String()] && redirects < 10 {
				c.permanent[key] = target
			}
		}
		if redirects == 10 {
			return issuePage{}, repoRequestFailed
		}
		redirects++
		address = target
	}
}

type apiResponse struct {
	status   int
	header   http.Header
	body     []byte
	issues   []issue
	redirect *url.URL
}

// requestOnce closes the response body before any retry or wait.
func (c *apiClient) requestOnce(address *url.URL) (apiResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return apiResponse{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghreport/1.0")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	if c.token != "" && sameOrigin(address, c.base) {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	// Client.Do parses Location before CheckRedirect; RoundTrip leaves it intact.
	resp, err := c.transport.RoundTrip(req)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{status: resp.StatusCode, header: resp.Header}
	// Only successful pages and rate-limit messages need a response body.
	readBody := resp.StatusCode == http.StatusOK ||
		(resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
			!isRateLimited(resp.StatusCode, resp.Header, nil)
	if !readBody {
		resp.Body.Close()
		return result, nil
	}
	result.body, err = io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err == nil {
		err = closeErr
	}
	return result, err
}

func resolveAPIReference(value string, current *url.URL) (*url.URL, error) {
	reference, err := url.Parse(value)
	if err != nil {
		return nil, repoInvalidResponse
	}
	address := current.ResolveReference(reference)
	if (address.Scheme != "http" && address.Scheme != "https") ||
		address.Hostname() == "" || address.User != nil || address.Opaque != "" {
		return nil, repoInvalidResponse
	}
	if port := address.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, repoInvalidResponse
		}
	}
	address.Fragment, address.RawFragment = "", ""
	return address, nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) && originPort(a) == originPort(b)
}

func originPort(address *url.URL) string {
	if port := address.Port(); port != "" {
		return port
	}
	if address.Scheme == "https" {
		return "443"
	}
	return "80"
}

// Pointers distinguish absent/null fields from valid zero values in JSON.
type apiIssue struct {
	Number *int64  `json:"number"`
	Title  *string `json:"title"`
	User   *struct {
		Login *string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name *string `json:"name"`
	} `json:"labels"`
	CreatedAt *string `json:"created_at"`
	HTMLURL   *string `json:"html_url"`
}

func decodeIssues(body []byte) ([]issue, error) {
	var entries []json.RawMessage
	if !utf8.Valid(body) {
		return nil, repoInvalidResponse
	}
	if err := json.Unmarshal(body, &entries); err != nil || entries == nil {
		return nil, repoInvalidResponse
	}
	issues := make([]issue, 0, len(entries))
	for _, raw := range entries {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return nil, repoInvalidResponse
		}
		if _, isPR := fields["pull_request"]; isPR {
			continue
		}
		var entry apiIssue
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, repoInvalidResponse
		}
		if entry.Number == nil || entry.Title == nil || entry.User == nil ||
			entry.User.Login == nil || entry.Labels == nil || entry.CreatedAt == nil || entry.HTMLURL == nil {
			return nil, repoInvalidResponse
		}
		created, err := time.Parse(time.RFC3339, *entry.CreatedAt)
		if err != nil {
			return nil, repoInvalidResponse
		}
		labels := make([]string, 0, len(entry.Labels))
		for _, label := range entry.Labels {
			if label.Name == nil {
				return nil, repoInvalidResponse
			}
			labels = append(labels, *label.Name)
		}
		issues = append(issues, issue{
			Number: *entry.Number, Title: *entry.Title, Author: *entry.User.Login,
			Labels: labels, CreatedAt: *entry.CreatedAt, URL: *entry.HTMLURL,
			created: created,
		})
	}
	return issues, nil
}
