package main

import (
	"encoding/json"
	"io"
	"os"
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
}

type fatalReport struct {
	Error string `json:"error"`
}

func writeReport(path string, stdout io.Writer, result report) error {
	if path == "-" {
		return json.NewEncoder(stdout).Encode(result)
	}
	file, err := os.Create(path)
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
