package main

import (
	"net/url"
	"strings"
)

// nextPage reads Link values without splitting commas inside URIs or quotes.
func nextPage(headers []string, current *url.URL) (*url.URL, error) {
	var next *url.URL
	for _, header := range headers {
		rest := strings.TrimLeft(header, " \t,")
		for rest != "" {
			if rest[0] != '<' {
				return nil, repoInvalidResponse
			}
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				return nil, repoInvalidResponse
			}
			target := rest[1:end]
			rest = strings.TrimLeft(rest[end+1:], " \t")
			rel, relSeen := "", false
			for strings.HasPrefix(rest, ";") {
				rest = strings.TrimLeft(rest[1:], " \t")
				name, tail := linkToken(rest)
				if name == "" {
					return nil, repoInvalidResponse
				}
				rest = strings.TrimLeft(tail, " \t")
				value := ""
				if strings.HasPrefix(rest, "=") {
					var err error
					value, rest, err = linkValue(strings.TrimLeft(rest[1:], " \t"))
					if err != nil {
						return nil, err
					}
					rest = strings.TrimLeft(rest, " \t")
				}
				// RFC 8288: only the first rel parameter of a link is used.
				if strings.EqualFold(name, "rel") && !relSeen {
					rel, relSeen = value, true
				}
			}
			if rest != "" && rest[0] != ',' {
				return nil, repoInvalidResponse
			}
			for _, relation := range strings.Fields(rel) {
				if !strings.EqualFold(relation, "next") {
					continue
				}
				address, err := resolveAPIReference(target, current)
				if err != nil {
					return nil, err
				}
				if next != nil && next.String() != address.String() {
					return nil, repoInvalidResponse
				}
				next = address
			}
			rest = strings.TrimLeft(rest, " \t,")
		}
	}
	return next, nil
}

func linkToken(value string) (string, string) {
	i := 0
	for i < len(value) {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			i++
		} else {
			break
		}
	}
	return value[:i], value[i:]
}

func linkValue(value string) (string, string, error) {
	if !strings.HasPrefix(value, `"`) {
		token, rest := linkToken(value)
		if token == "" {
			return "", "", repoInvalidResponse
		}
		return token, rest, nil
	}
	var decoded strings.Builder
	for i := 1; i < len(value); i++ {
		switch value[i] {
		case '"':
			return decoded.String(), value[i+1:], nil
		case '\\':
			i++
			if i == len(value) {
				return "", "", repoInvalidResponse
			}
		}
		if value[i] < ' ' && value[i] != '\t' || value[i] == 127 {
			return "", "", repoInvalidResponse
		}
		decoded.WriteByte(value[i])
	}
	return "", "", repoInvalidResponse
}
