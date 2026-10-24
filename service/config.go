package main

import (
	"errors"
	"flag"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const defaultAPIBaseURL = "https://api.github.com"

type config struct {
	Repos      []string
	Limit      int
	Since      *time.Time
	Out        string
	Trace      string
	CacheDir   string
	APIBaseURL *url.URL
	Token      string
}

func parseConfig(args []string, lookupEnv func(string) (string, bool)) (config, error) {
	var cfg config
	var repos, limit, since string
	flags := flag.NewFlagSet("ghreport", flag.ContinueOnError)
	// Flag errors can contain argument values. Only safe diagnostics are returned.
	flags.SetOutput(io.Discard)
	flags.StringVar(&repos, "repos", "", "репозитории owner/repo через запятую")
	flags.StringVar(&limit, "limit", "100", "максимум issue на репозиторий")
	flags.StringVar(&since, "since", "", "дата создания, начиная с YYYY-MM-DD (UTC)")
	flags.StringVar(&cfg.Out, "out", "-", "файл отчёта или - для stdout")
	flags.StringVar(&cfg.Trace, "trace", "", "файл журнала HTTP")
	flags.StringVar(&cfg.CacheDir, "cache-dir", "./ghreport-cache", "каталог кэша")
	if err := flags.Parse(args); err != nil {
		return config{}, errors.New("неизвестный параметр или отсутствует его значение")
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("позиционные аргументы не поддерживаются")
	}

	var err error
	cfg.Repos, err = parseRepos(repos)
	if err != nil {
		return config{}, err
	}
	cfg.Limit, err = strconv.Atoi(limit)
	if err != nil || cfg.Limit < 1 {
		return config{}, errors.New("--limit должен быть целым числом, не меньшим единицы")
	}
	var sinceSet, traceSet bool
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "since":
			sinceSet = true
		case "trace":
			traceSet = true
		}
	})
	if sinceSet {
		date, err := time.Parse("2006-01-02", since)
		if err != nil {
			return config{}, errors.New("--since должен быть корректной датой YYYY-MM-DD")
		}
		cfg.Since = &date
	}
	if cfg.Out == "" || cfg.CacheDir == "" || (traceSet && cfg.Trace == "") {
		return config{}, errors.New("--out, --trace и --cache-dir требуют непустого пути")
	}

	base, exists := lookupEnv("GITHUB_API_BASE_URL")
	if !exists {
		base = defaultAPIBaseURL
	}
	cfg.APIBaseURL, err = parseAPIBaseURL(base)
	if err != nil {
		return config{}, err
	}
	cfg.Token, _ = lookupEnv("GITHUB_TOKEN")
	return cfg, nil
}

func parseRepos(value string) ([]string, error) {
	var repos []string
	seen := make(map[string]bool)
	for _, entry := range strings.Split(value, ",") {
		repo := strings.TrimSpace(entry)
		parts := strings.Split(repo, "/")
		if len(parts) != 2 || !validRepoPart(parts[0]) || !validRepoPart(parts[1]) {
			return nil, errors.New("--repos требует непустой список репозиториев owner/repo через запятую")
		}
		if !seen[repo] {
			seen[repo] = true
			repos = append(repos, repo)
		}
	}
	return repos, nil
}

func validRepoPart(part string) bool {
	if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `\?#`) {
		return false
	}
	return strings.IndexFunc(part, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) == -1
}

func parseAPIBaseURL(value string) (*url.URL, error) {
	base, err := url.Parse(value)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Hostname() == "" || base.User != nil || base.Opaque != "" ||
		base.RawQuery != "" || base.ForceQuery || strings.Contains(value, "#") {
		return nil, errors.New("GITHUB_API_BASE_URL должен быть абсолютным HTTP(S)-адресом без учётных данных, query и fragment")
	}
	if port := base.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New("GITHUB_API_BASE_URL содержит некорректный порт")
		}
	}
	return base, nil
}
