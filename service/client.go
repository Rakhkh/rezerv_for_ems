package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errUnauthorized = errors.New(fatalUnauthorized)

type apiClient struct {
	base  *url.URL
	token string
	http  *http.Client
}

func newAPIClient(cfg config) *apiClient {
	return &apiClient{
		base: cfg.APIBaseURL, token: cfg.Token,
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Explicit redirects and their trace records are added in stage 4.
				return http.ErrUseLastResponse
			},
		},
	}
}

// listIssues currently reads only the first page; pagination is added in stage 3.
func (c *apiClient) listIssues(repo string) ([]issue, error) {
	owner, name, _ := strings.Cut(repo, "/")
	address := c.base.JoinPath("repos", url.PathEscape(owner), url.PathEscape(name), "issues")
	address.RawQuery = url.Values{
		"state": {"open"}, "sort": {"created"},
		"direction": {"asc"}, "per_page": {"100"},
	}.Encode()
	req, err := http.NewRequest(http.MethodGet, address.String(), nil)
	if err != nil {
		return nil, repoRequestFailed
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghreport/1.0")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, repoRequestFailed
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errUnauthorized
	case http.StatusNotFound:
		return nil, repoNotFound
	default:
		return nil, repoRequestFailed
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, repoRequestFailed
	}
	return decodeIssues(body)
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
	var entries []apiIssue
	if err := json.Unmarshal(body, &entries); err != nil || entries == nil {
		return nil, repoInvalidResponse
	}
	issues := make([]issue, 0, len(entries))
	for _, entry := range entries {
		if entry.Number == nil || entry.Title == nil || entry.User == nil ||
			entry.User.Login == nil || entry.Labels == nil || entry.CreatedAt == nil || entry.HTMLURL == nil {
			return nil, repoInvalidResponse
		}
		if _, err := time.Parse(time.RFC3339, *entry.CreatedAt); err != nil {
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
		})
	}
	return issues, nil
}
