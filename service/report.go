package main

type repoError string

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
