package main

import (
	"regexp"
	"strings"
	"time"
)

var creationTimePattern = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})[Tt]([0-9]{2}:[0-9]{2}:[0-9]{2})(?:\.([0-9]+))?([Zz]|[+-][0-9]{2}:[0-9]{2})$`)

// time.Time retains nanoseconds; the extra decimal digits preserve ordering at
// finer precision. Trimming trailing zeros makes lexical comparison exact.
func parseCreationTime(value string) (time.Time, string, error) {
	parts := creationTimePattern.FindStringSubmatch(value)
	if parts == nil {
		return time.Time{}, "", repoInvalidResponse
	}
	zone := strings.ToUpper(parts[4])
	if len(zone) > 1 && (zone[1:3] > "23" || zone[4:6] > "59") {
		return time.Time{}, "", repoInvalidResponse
	}
	fraction, remainder := parts[3], ""
	if len(fraction) > 9 {
		remainder = strings.TrimRight(fraction[9:], "0")
		fraction = fraction[:9]
	}
	if fraction != "" {
		fraction = "." + fraction
	}
	created, err := time.Parse(time.RFC3339, parts[1]+"T"+parts[2]+fraction+zone)
	if err != nil {
		return time.Time{}, "", repoInvalidResponse
	}
	return created, remainder, nil
}
