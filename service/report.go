package main

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

type repoError string

func (e repoError) Error() string {
	return string(e)
}

const (
	repoNotFound        repoError = "not_found"
	repoRateLimited     repoError = "rate_limited"
	repoRequestFailed   repoError = "request_failed"
	repoInvalidResponse repoError = "invalid_response"
)

const (
	fatalUnauthorized = "unauthorized"
	fatalAllFailed    = "all_failed"
)

type report struct {
	GeneratedAt string       `json:"generated_at"`
	Repos       []repoReport `json:"repos"`
}

type repoReport struct {
	Repo   string     `json:"repo"`
	Error  *repoError `json:"error"`
	Count  int        `json:"count"`
	Issues []issue    `json:"issues"`
}

type issue struct {
	Number    int64    `json:"number"`
	Title     string   `json:"title"`
	Author    string   `json:"author"`
	Labels    []string `json:"labels"`
	CreatedAt string   `json:"created_at"`
	URL       string   `json:"url"`
	created   time.Time
}

type fatalReport struct {
	Error string `json:"error"`
}

type outputFileCreator func(string) (io.WriteCloser, error)

func (r report) containsToken(token string) bool {
	if token == "" {
		return false
	}
	contains := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(value, token) {
				return true
			}
		}
		return false
	}
	if contains(r.GeneratedAt) {
		return true
	}
	for _, repo := range r.Repos {
		if contains(repo.Repo) || repo.Error != nil && contains(string(*repo.Error)) {
			return true
		}
		for _, item := range repo.Issues {
			if contains(item.Title, item.Author, item.CreatedAt, item.URL) || contains(item.Labels...) {
				return true
			}
		}
	}
	return false
}

func writeReport(path string, stdout io.Writer, result report, create outputFileCreator) error {
	if path == "-" {
		return json.NewEncoder(stdout).Encode(result)
	}
	file, err := create(path)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(result)
	closeErr := file.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}
