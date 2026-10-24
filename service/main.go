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
	return runWithFiles(args, lookupEnv, stdout, stderr, func(path string) (io.WriteCloser, error) {
		return os.Create(path)
	})
}

func runWithFiles(args []string, lookupEnv func(string) (string, bool), stdout, stderr io.Writer, create outputFileCreator) int {
	cfg, err := parseConfig(args, lookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "ghreport: %s\n", err)
		return exitUsage
	}
	conflict, err := outputPathsConflict(cfg.Out, cfg.Trace)
	if err != nil {
		fmt.Fprintln(stderr, "ghreport: не удалось проверить пути отчёта и журнала")
		return exitFatal
	}
	if conflict {
		fmt.Fprintln(stderr, "ghreport: --out и --trace должны указывать на разные файлы")
		return exitUsage
	}

	client := newAPIClient(cfg)
	defer client.closeIdleConnections()
	client.cache, err = newPageCache(cfg.CacheDir, cfg.Token, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "ghreport: %s\n", errCache)
		return exitFatal
	}
	var traceFile io.WriteCloser
	if cfg.Trace != "" {
		traceFile, err = create(cfg.Trace)
		if err != nil {
			fmt.Fprintln(stderr, "ghreport: не удалось открыть журнал HTTP")
			return exitFatal
		}
		client.trace = json.NewEncoder(traceFile)
	}
	result := report{Repos: make([]repoReport, 0, len(cfg.Repos))}
	failed := 0
	var fatalErr error
	for _, repo := range cfg.Repos {
		entry := repoReport{Repo: repo, Issues: []issue{}}
		issues, err := client.collectIssues(repo, cfg.Limit, cfg.Since)
		if errors.Is(err, errUnauthorized) || errors.Is(err, errTrace) || errors.Is(err, errCache) {
			fatalErr = err
			break
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
	if traceFile != nil {
		if err := traceFile.Close(); err != nil {
			fmt.Fprintln(stderr, "ghreport: не удалось закрыть журнал HTTP")
			return exitFatal
		}
	}
	if errors.Is(fatalErr, errTrace) {
		fmt.Fprintf(stderr, "ghreport: %s\n", errTrace)
		return exitFatal
	}
	if errors.Is(fatalErr, errCache) {
		fmt.Fprintf(stderr, "ghreport: %s\n", errCache)
		return exitFatal
	}
	if errors.Is(fatalErr, errUnauthorized) {
		return writeFatal(fatalUnauthorized, stdout, stderr)
	}
	if failed == len(cfg.Repos) {
		return writeFatal(fatalAllFailed, stdout, stderr)
	}
	result.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	if result.containsToken(cfg.Token) {
		fmt.Fprintln(stderr, "ghreport: отчёт содержит токен; вывод отменён")
		return exitFatal
	}
	if err := writeReport(cfg.Out, stdout, result, create); err != nil {
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
