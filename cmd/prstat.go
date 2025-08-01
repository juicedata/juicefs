/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
)

type PullRequest struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	MergedAt  string `json:"merged_at"`
	User      User   `json:"user"`
	MergedBy  *User  `json:"merged_by"`
	HTMLURL   string `json:"html_url"`
	CreatedAt string `json:"created_at"`
}

type User struct {
	Login string `json:"login"`
}

type GitHubAPIResponse struct {
	TotalCount   int           `json:"total_count"`
	PullRequests []PullRequest `json:"items"`
}

func cmdPRStat() *cli.Command {
	return &cli.Command{
		Name:     "prstat",
		Category: "ADMIN",
		Action:   prstat,
		Usage:    "Show pull request statistics",
		Description: `
Show pull request statistics for a given user or repository.
This command fetches data from GitHub API to show merged PR count.

Examples:
$ juicefs prstat --user username
$ juicefs prstat --user username --repo juicedata/juicefs
$ juicefs prstat --merged-by username --repo juicedata/juicefs`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "user",
				Usage: "GitHub username to check PRs for",
			},
			&cli.StringFlag{
				Name:  "merged-by",
				Usage: "GitHub username who merged the PRs",
			},
			&cli.StringFlag{
				Name:  "repo",
				Usage: "GitHub repository in format owner/repo",
				Value: "juicedata/juicefs",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "GitHub API token (or set GITHUB_TOKEN environment variable)",
			},
			&cli.BoolFlag{
				Name:  "detailed",
				Usage: "Show detailed information about each PR",
			},
			&cli.BoolFlag{
				Name:  "mock",
				Usage: "Use mock data for testing (when GitHub API is not accessible)",
			},
		},
	}
}

func prstat(c *cli.Context) error {
	setup(c, 0)

	user := c.String("user")
	mergedBy := c.String("merged-by")
	repo := c.String("repo")
	token := c.String("token")
	detailed := c.Bool("detailed")
	mock := c.Bool("mock")

	if user == "" && mergedBy == "" {
		return fmt.Errorf("please specify either --user or --merged-by flag")
	}

	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	var prs []PullRequest
	var err error

	if mock {
		// Use mock data for testing
		prs = getMockPRs(user, mergedBy)
		fmt.Printf("🧪 Using mock data for testing\n")
	} else {
		if token == "" {
			fmt.Printf("Warning: No GitHub token provided. API rate limit will be lower.\n")
			fmt.Printf("Set GITHUB_TOKEN environment variable or use --token flag for higher rate limits.\n\n")
		}

		// Determine the search query
		var query string
		if user != "" {
			query = fmt.Sprintf("repo:%s author:%s is:pr is:merged", repo, user)
		} else {
			query = fmt.Sprintf("repo:%s is:pr is:merged", repo)
		}

		prs, err = fetchPullRequests(query, token, mergedBy)
		if err != nil {
			return fmt.Errorf("failed to fetch pull requests: %v\nHint: If GitHub API is not accessible, try using --mock flag for testing", err)
		}
	}

	// Filter by merged-by if specified and not using mock data
	var filteredPRs []PullRequest
	if !mock && mergedBy != "" {
		for _, pr := range prs {
			if pr.MergedBy != nil && pr.MergedBy.Login == mergedBy {
				filteredPRs = append(filteredPRs, pr)
			}
		}
	} else {
		filteredPRs = prs
	}

	// Display results
	if user != "" {
		fmt.Printf("📊 Pull Request Statistics for user: %s\n", user)
	} else {
		fmt.Printf("📊 Pull Request Statistics merged by: %s\n", mergedBy)
	}
	fmt.Printf("📁 Repository: %s\n", repo)
	fmt.Printf("🔢 Total merged PRs: %d\n\n", len(filteredPRs))

	if detailed && len(filteredPRs) > 0 {
		fmt.Printf("📋 Detailed PR List:\n")
		fmt.Printf("%-8s %-80s %-20s %-20s\n", "Number", "Title", "Merged At", "Merged By")
		fmt.Printf("%s\n", strings.Repeat("-", 130))

		for _, pr := range filteredPRs {
			title := pr.Title
			if len(title) > 77 {
				title = title[:77] + "..."
			}

			mergedAt := "Unknown"
			if pr.MergedAt != "" {
				if t, err := time.Parse(time.RFC3339, pr.MergedAt); err == nil {
					mergedAt = t.Format("2006-01-02 15:04")
				}
			}

			mergedByUser := "Unknown"
			if pr.MergedBy != nil {
				mergedByUser = pr.MergedBy.Login
			}

			fmt.Printf("#%-7d %-80s %-20s %-20s\n", pr.Number, title, mergedAt, mergedByUser)
		}
	}

	return nil
}

func fetchPullRequests(query, token, mergedBy string) ([]PullRequest, error) {
	var allPRs []PullRequest
	page := 1
	perPage := 100

	for {
		url := fmt.Sprintf("https://api.github.com/search/issues?q=%s&type=pr&page=%d&per_page=%d", query, page, perPage)

		req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Accept", "application/vnd.github.v3+json")
		if token != "" {
			req.Header.Set("Authorization", "token "+token)
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("GitHub API request failed with status %d: %s", resp.StatusCode, string(body))
		}

		var response GitHubAPIResponse
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return nil, err
		}

		// If we're filtering by merged-by, we need to fetch individual PR details
		// because the search API doesn't return merged_by information
		if mergedBy != "" {
			for _, pr := range response.PullRequests {
				detailedPR, err := fetchPRDetails(pr.Number, strings.Split(query, " ")[0][5:], token)
				if err != nil {
					logger.Debugf("Failed to fetch details for PR #%d: %v", pr.Number, err)
					continue
				}
				allPRs = append(allPRs, *detailedPR)
			}
		} else {
			allPRs = append(allPRs, response.PullRequests...)
		}

		// Check if we have more pages
		if len(response.PullRequests) < perPage {
			break
		}
		page++

		// Rate limiting: sleep a bit between requests
		time.Sleep(100 * time.Millisecond)
	}

	return allPRs, nil
}

func fetchPRDetails(prNumber int, repo, token string) (*PullRequest, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", repo, prNumber)

	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}

	return &pr, nil
}

func getMockPRs(user, mergedBy string) []PullRequest {
	// Mock data for testing when GitHub API is not accessible
	mockPRs := []PullRequest{
		{
			Number:    5001,
			Title:     "Add new feature for better performance",
			State:     "closed",
			MergedAt:  "2024-01-15T10:30:00Z",
			User:      User{Login: "davies"},
			MergedBy:  &User{Login: "admin"},
			HTMLURL:   "https://github.com/juicedata/juicefs/pull/5001",
			CreatedAt: "2024-01-10T09:00:00Z",
		},
		{
			Number:    4998,
			Title:     "Fix critical bug in file system operations",
			State:     "closed",
			MergedAt:  "2024-01-14T16:20:00Z",
			User:      User{Login: "davies"},
			MergedBy:  &User{Login: "davies"},
			HTMLURL:   "https://github.com/juicedata/juicefs/pull/4998",
			CreatedAt: "2024-01-12T14:00:00Z",
		},
		{
			Number:    4995,
			Title:     "Update documentation for new CLI commands",
			State:     "closed",
			MergedAt:  "2024-01-13T11:45:00Z",
			User:      User{Login: "testuser"},
			MergedBy:  &User{Login: "davies"},
			HTMLURL:   "https://github.com/juicedata/juicefs/pull/4995",
			CreatedAt: "2024-01-11T08:30:00Z",
		},
		{
			Number:    4990,
			Title:     "Implement new synchronization algorithm",
			State:     "closed",
			MergedAt:  "2024-01-12T13:15:00Z",
			User:      User{Login: "davies"},
			MergedBy:  &User{Login: "admin"},
			HTMLURL:   "https://github.com/juicedata/juicefs/pull/4990",
			CreatedAt: "2024-01-08T10:00:00Z",
		},
		{
			Number:    4987,
			Title:     "Refactor metadata handling code",
			State:     "closed",
			MergedAt:  "2024-01-11T15:30:00Z",
			User:      User{Login: "otheruser"},
			MergedBy:  &User{Login: "davies"},
			HTMLURL:   "https://github.com/juicedata/juicefs/pull/4987",
			CreatedAt: "2024-01-07T12:00:00Z",
		},
	}

	// Filter mock data based on criteria
	var filtered []PullRequest
	for _, pr := range mockPRs {
		if user != "" && pr.User.Login == user {
			filtered = append(filtered, pr)
		} else if mergedBy != "" && pr.MergedBy != nil && pr.MergedBy.Login == mergedBy {
			filtered = append(filtered, pr)
		}
	}

	// If no specific filter, return sample that matches the requested criteria
	if len(filtered) == 0 {
		// Return a subset based on the query type
		if user != "" {
			// Return PRs authored by the specified user
			for _, pr := range mockPRs {
				if pr.User.Login == "davies" { // assuming davies is a common user
					pr.User.Login = user
					filtered = append(filtered, pr)
				}
			}
		} else if mergedBy != "" {
			// Return PRs merged by the specified user
			for _, pr := range mockPRs {
				if pr.MergedBy != nil && pr.MergedBy.Login == "davies" {
					pr.MergedBy.Login = mergedBy
					filtered = append(filtered, pr)
				}
			}
		}
	}

	return filtered
}