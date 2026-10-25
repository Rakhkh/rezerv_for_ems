package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errTrace = errors.New("не удалось записать журнал HTTP")

type traceRecord struct {
	TS        string  `json:"ts"`
	Method    string  `json:"method"`
	URL       string  `json:"url"`
	Status    int     `json:"status"`
	Attempt   int     `json:"attempt"`
	FromCache bool    `json:"from_cache"`
	Error     *string `json:"error"`
}

func (c *apiClient) recordAttempt(started time.Time, address *url.URL, status, attempt int, fromCache bool, description string) error {
	if c.trace == nil {
		return nil
	}
	record := traceRecord{
		TS: started.UTC().Format(time.RFC3339), Method: http.MethodGet,
		URL: address.String(), Status: status, Attempt: attempt,
		FromCache: fromCache,
	}
	if c.token != "" {
		record.URL = strings.ReplaceAll(record.URL, c.token, "[REDACTED]")
	}
	if description != "" {
		record.Error = &description
	}
	if err := c.trace.Encode(record); err != nil {
		return errTrace
	}
	return nil
}
