package main

import (
	"sort"
	"time"
)

func (c *apiClient) collectIssues(repo string, limit int, since *time.Time) ([]issue, error) {
	address := c.issuesURL(repo)
	visited := make(map[string]bool)
	seen := make(map[int64]bool)
	issues := make([]issue, 0)
	for {
		key := address.String()
		if visited[key] {
			return nil, repoInvalidResponse
		}
		visited[key] = true
		page, err := c.fetchPage(address)
		if err != nil {
			return nil, err
		}
		for _, item := range page.issues {
			if seen[item.Number] || since != nil && item.created.Before(*since) {
				continue
			}
			seen[item.Number] = true
			issues = append(issues, item)
			if len(issues) == limit {
				break
			}
		}
		if len(issues) == limit {
			break
		}
		address, err = nextPage(page.links, address)
		if err != nil {
			return nil, err
		}
		if address == nil {
			break
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].created.Equal(issues[j].created) {
			return issues[i].Number < issues[j].Number
		}
		return issues[i].created.Before(issues[j].created)
	})
	return issues, nil
}
