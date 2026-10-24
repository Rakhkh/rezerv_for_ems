package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	exitSuccess = 0
	exitPartial = 1
	exitUsage   = 2
	exitFatal   = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}

func run(args []string, lookupEnv func(string) (string, bool), stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args, lookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "ghreport: %s\n", err)
		return exitUsage
	}

	client := newAPIClient(cfg)
	defer client.http.CloseIdleConnections()
	result := report{Repos: make([]repoReport, 0, len(cfg.Repos))}
	failed := 0
	for _, repo := range cfg.Repos {
		entry := repoReport{Repo: repo, Issues: []issue{}}
		issues, err := client.listIssues(repo)
		if errors.Is(err, errUnauthorized) {
			return writeFatal(fatalUnauthorized, stdout, stderr)
		}
		if err != nil {
			var code repoError
			if !errors.As(err, &code) {
				code = repoRequestFailed
			}
			entry.Error = &code
			failed++
		} else {
			entry.Issues = issues
			entry.Count = len(issues)
		}
		result.Repos = append(result.Repos, entry)
	}
	if failed == len(cfg.Repos) {
		return writeFatal(fatalAllFailed, stdout, stderr)
	}
	result.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeReport(cfg.Out, stdout, result); err != nil {
		fmt.Fprintln(stderr, "ghreport: не удалось записать отчёт")
		return exitFatal
	}
	if failed > 0 {
		return exitPartial
	}
	return exitSuccess
}

func writeFatal(code string, stdout, stderr io.Writer) int {
	if err := json.NewEncoder(stdout).Encode(fatalReport{Error: code}); err != nil {
		fmt.Fprintln(stderr, "ghreport: не удалось записать сообщение об ошибке")
	}
	return exitFatal
}
