package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxTransientRetries = 3
	maxRateRetries      = 2
)

func (c *apiClient) requestWithRetries(address *url.URL, cached *cacheEntry) (apiResponse, error) {
	etag := ""
	if cached != nil {
		etag = cached.ETag
	}
	transientRetries, rateRetries := 0, 0
	for attempt := 1; ; attempt++ {
		started := c.now()
		response, requestErr := c.requestOnce(address, etag)
		var resultErr error
		traceError := ""
		transient, limited := false, false
		if response.status == http.StatusUnauthorized {
			resultErr, traceError = errUnauthorized, "http_401"
		} else if requestErr != nil {
			resultErr, transient, traceError = repoRequestFailed, true, "network"
			var networkErr net.Error
			if errors.As(requestErr, &networkErr) && networkErr.Timeout() {
				traceError = "timeout"
			}
		} else {
			switch response.status {
			case http.StatusOK, http.StatusNotModified:
				resultErr = c.acceptPage(address, &response, cached)
				if resultErr != nil {
					traceError = "invalid_response"
				}
			case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect:
				location := response.header.Get("Location")
				if location != "" {
					response.redirect, resultErr = resolveAPIReference(location, address)
				}
				if location == "" || resultErr != nil {
					resultErr, traceError = repoRequestFailed, "redirect"
				}
			case http.StatusNotFound:
				resultErr, traceError = repoNotFound, "http_404"
			default:
				limited = isRateLimited(response.status, response.header, response.body)
				if limited {
					resultErr, traceError = repoRateLimited, "rate_limited"
				} else {
					resultErr, traceError = repoRequestFailed, fmt.Sprintf("http_%d", response.status)
					transient = response.status >= 500 && response.status <= 599
				}
			}
		}
		if errors.Is(resultErr, errCache) {
			traceError = "cache"
		}
		if err := c.recordAttempt(started, address, response.status, attempt, response.fromCache, traceError); err != nil {
			return apiResponse{}, err
		}
		if transient && transientRetries < maxTransientRetries {
			c.sleep(time.Second << transientRetries)
			transientRetries++
			continue
		}
		if limited && rateRetries < maxRateRetries {
			c.sleep(rateDelay(response.header, c.now(), rateRetries))
			rateRetries++
			continue
		}
		return response, resultErr
	}
}

func isRateLimited(status int, header http.Header, body []byte) bool {
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		return false
	}
	if len(header.Values("Retry-After")) > 0 || strings.TrimSpace(header.Get("X-RateLimit-Remaining")) == "0" {
		return true
	}
	message := string(body)
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil {
		message = payload.Message
	}
	message = strings.ToLower(message)
	return strings.Contains(message, "rate limit") || strings.Contains(message, "rate_limit") || strings.Contains(message, "rate-limit")
}

func rateDelay(header http.Header, now time.Time, retries int) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/time.Second) {
		return time.Duration(seconds) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil {
		return max(time.Duration(0), until.Sub(now))
	}
	if seconds, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64); err == nil {
		return max(time.Duration(0), time.Unix(seconds, 0).Sub(now))
	}
	return time.Minute << retries
}
