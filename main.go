package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

const (
	githubAPIBase     = "https://api.github.com"
	githubRawBase     = "https://raw.githubusercontent.com"
	repoOwner         = "fleetdm"
	repoName          = "fleet"
	appsJSONPath      = "ee/maintained-apps/outputs/apps.json"
	outputDir         = "data"
	outputCSV         = "data/apps_growth.csv"
	perPage           = 100 // GitHub API max per page
)

type commitData struct {
	date  string
	count int
}

type githubCommit struct {
	Sha    string `json:"sha"`
	Commit struct {
		Author struct {
			Date string `json:"date"`
		} `json:"author"`
		Message string `json:"message"`
	} `json:"commit"`
}

func main() {
	fmt.Println("🚀 Fleet Apps Growth Tracker - Data Generator")
	fmt.Println("=============================================\n")

	// Get commits from GitHub API
	fmt.Println("📡 Fetching commit history from GitHub API...")
	commits, err := getGitHubCommits()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error getting commits: %v\n", err)
		os.Exit(1)
	}

	if len(commits) == 0 {
		fmt.Println("❌ No commits found!")
		os.Exit(1)
	}

	fmt.Printf("✅ Found %d commits\n\n", len(commits))

	// Generate continuous data
	if err := generateContinuousData(commits); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error generating data: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n✅ Data generation completed successfully!")
}

func getGitHubCommits() ([]commitData, error) {
	commits := make(map[string]int) // date -> count
	page := 1

	for {
		url := fmt.Sprintf("%s/repos/%s/%s/commits?path=%s&per_page=%d&page=%d",
			githubAPIBase, repoOwner, repoName, appsJSONPath, perPage, page)

		fmt.Printf("📥 Fetching page %d...\n", page)

		resp, err := http.Get(url)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch commits: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("GitHub API error (status %d): %s", resp.StatusCode, string(body))
		}

		var githubCommits []githubCommit
		if err := json.NewDecoder(resp.Body).Decode(&githubCommits); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		if len(githubCommits) == 0 {
			break // No more commits
		}

		// Process each commit
		for _, gc := range githubCommits {
			// Parse commit date
			commitTime, err := time.Parse(time.RFC3339, gc.Commit.Author.Date)
			if err != nil {
				continue
			}
			dateStr := commitTime.Format("2006-01-02")

			// Skip if we already have data for this date (deduplicate)
			if _, exists := commits[dateStr]; exists {
				continue
			}

			// Fetch file content at this commit
			count, err := getAppCountAtCommit(gc.Sha)
			if err != nil {
				fmt.Printf("⚠️  Warning: failed to get app count for commit %s: %v\n", gc.Sha[:7], err)
				continue
			}

			commits[dateStr] = count
			fmt.Printf("  ✓ %s: %d apps\n", dateStr, count)
		}

		// If we got fewer than perPage results, we're done
		if len(githubCommits) < perPage {
			break
		}

		page++
	}

	// Convert to slice and sort
	result := make([]commitData, 0, len(commits))
	for date, count := range commits {
		result = append(result, commitData{date: date, count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].date < result[j].date
	})

	return result, nil
}

func getAppCountAtCommit(sha string) (int, error) {
	// Use raw GitHub URL to get file content at specific commit
	url := fmt.Sprintf("%s/%s/%s/%s/%s",
		githubRawBase, repoOwner, repoName, sha, appsJSONPath)

	resp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to fetch file (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	var data struct {
		Apps []interface{} `json:"apps"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return len(data.Apps), nil
}

func generateContinuousData(commits []commitData) error {
	if len(commits) == 0 {
		return fmt.Errorf("no commits provided")
	}

	firstDateStr := commits[0].date
	lastDateStr := commits[len(commits)-1].date
	todayStr := time.Now().Format("2006-01-02")

	// Use today as end date if it's later than last commit
	endDateStr := lastDateStr
	if todayStr > lastDateStr {
		endDateStr = todayStr
	}

	fmt.Printf("📅 Date range: %s to %s\n", firstDateStr, endDateStr)

	// Parse dates
	firstDate, err := time.Parse("2006-01-02", firstDateStr)
	if err != nil {
		return fmt.Errorf("failed to parse first date: %w", err)
	}

	endDate, err := time.Parse("2006-01-02", endDateStr)
	if err != nil {
		return fmt.Errorf("failed to parse end date: %w", err)
	}

	// Create a map of commit dates to counts
	commitCounts := make(map[string]int)
	for _, commit := range commits {
		commitCounts[commit.date] = commit.count
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Generate CSV
	file, err := os.Create(outputCSV)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	if err := writer.Write([]string{"date", "app_count", "apps_added_since_previous"}); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	currentDate := firstDate
	currentCount := 0
	lastKnownCount := 0
	lastWrittenCount := 0
	entryCount := 0

	for !currentDate.After(endDate) {
		dateStr := currentDate.Format("2006-01-02")

		// Check if this date has a commit
		if count, exists := commitCounts[dateStr]; exists {
			currentCount = count
			lastKnownCount = count
		}

		// Use last known count (carry forward if no commit on this date)
		if currentCount == 0 && lastKnownCount == 0 {
			currentDate = currentDate.AddDate(0, 0, 1)
			continue
		}

		// Use last known count for days without commits
		displayCount := lastKnownCount
		if currentCount > 0 {
			displayCount = currentCount
		}

		// Calculate additions (only positive changes)
		var added int
		if lastWrittenCount == 0 {
			added = displayCount // First entry
		} else {
			added = displayCount - lastWrittenCount
			if added < 0 {
				added = 0
			}
		}

		// Write entry for every day
		if err := writer.Write([]string{
			dateStr,
			fmt.Sprintf("%d", displayCount),
			fmt.Sprintf("%d", added),
		}); err != nil {
			return fmt.Errorf("failed to write CSV row: %w", err)
		}

		if displayCount > lastWrittenCount {
			lastWrittenCount = displayCount
		}

		// Reset currentCount for next iteration
		if _, exists := commitCounts[dateStr]; !exists {
			currentCount = 0
		}

		currentDate = currentDate.AddDate(0, 0, 1)
		entryCount++
	}

	fmt.Printf("✅ Generated: %s\n", outputCSV)
	fmt.Printf("📊 Total entries: %d\n", entryCount)
	fmt.Printf("📈 Final app count: %d\n", lastWrittenCount)

	return nil
}
