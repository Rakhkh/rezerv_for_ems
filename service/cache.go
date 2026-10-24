package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var errCache = errors.New("ошибка доступа к кэшу")

const cacheVersion = 1

type cacheEntry struct {
	Version  int             `json:"version"`
	URL      string          `json:"url"`
	ETag     string          `json:"etag,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
	Links    []string        `json:"links,omitempty"`
	Redirect string          `json:"redirect,omitempty"`
}

type pageCache struct {
	dir        string
	token      string
	diagnostic io.Writer
}

func newPageCache(dir, token string, diagnostic io.Writer) (*pageCache, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errCache
	}
	return &pageCache{dir: dir, token: token, diagnostic: diagnostic}, nil
}

func (c *pageCache) path(address *url.URL) string {
	return filepath.Join(c.dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(address.String()))))
}

func (c *pageCache) warn(message string) {
	if c.diagnostic != nil {
		fmt.Fprintln(c.diagnostic, "ghreport: "+message)
	}
}

func (c *pageCache) load(address *url.URL) (*cacheEntry, error) {
	if c == nil {
		return nil, nil
	}
	data, err := os.ReadFile(c.path(address))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errCache
	}
	var entry cacheEntry
	valid := utf8.Valid(data) && json.Unmarshal(data, &entry) == nil &&
		entry.Version == cacheVersion && entry.URL == address.String() &&
		!strings.ContainsAny(entry.ETag, "\r\n\x00")
	if valid && entry.Redirect != "" {
		target, err := resolveAPIReference(entry.Redirect, address)
		valid = err == nil && target.String() == entry.Redirect && len(entry.Body) == 0 &&
			entry.ETag == "" && len(entry.Links) == 0
	} else if valid {
		_, err := decodeIssues(entry.Body)
		valid = err == nil
	}
	if !valid {
		c.warn("повреждённая запись кэша пропущена")
		return nil, nil
	}
	return &entry, nil
}

func (c *pageCache) store(entry cacheEntry) error {
	if c == nil {
		return nil
	}
	entry.Version = cacheVersion
	data, err := json.Marshal(entry)
	if err != nil {
		return errCache
	}
	address, err := url.Parse(entry.URL)
	if err != nil {
		return errCache
	}
	// A compatible API may reflect the credential in a URL or response metadata.
	// Do not persist it, even though request headers are never stored here.
	if c.token != "" {
		encodedToken, _ := json.Marshal(c.token)
		if bytes.Contains(data, encodedToken[1:len(encodedToken)-1]) {
			if err := os.Remove(c.path(address)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return errCache
			}
			c.warn("ответ с отражённым токеном не сохранён в кэш")
			return nil
		}
	}
	file, err := os.CreateTemp(c.dir, ".ghreport-*.tmp")
	if err != nil {
		return errCache
	}
	name := file.Name()
	defer os.Remove(name)
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errCache
	}
	// Close first: Windows cannot replace an open cache file.
	// Rename replaces an existing file; its atomicity is platform-dependent.
	if err := os.Rename(name, c.path(address)); err != nil {
		return errCache
	}
	return nil
}
