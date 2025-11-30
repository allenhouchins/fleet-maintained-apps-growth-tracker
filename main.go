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
	appBaseURL        = "https://raw.githubusercontent.com/fleetdm/fleet/main/ee/maintained-apps/outputs"
	outputDir         = "data"
	outputCSV         = "data/apps_growth.csv"
	versionsJSON      = "data/app_versions.json"
	versionHistoryJSON = "data/version_history.json"
	perPage           = 100 // GitHub API max per page
)

type commitData struct {
	date         string
	count        int
	macCount     int
	windowsCount int
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

type appVersionInfo struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	Version      string `json:"version"`
	InstallerURL string `json:"installerUrl"`
}

type appVersionsData struct {
	LastUpdated string           `json:"lastUpdated"`
	Apps        []appVersionInfo `json:"apps"`
}

type versionChange struct {
	Date         string `json:"date"`
	AppName      string `json:"appName"`
	Slug         string `json:"slug"`
	Platform     string `json:"platform"`
	OldVersion   string `json:"oldVersion"`
	NewVersion   string `json:"newVersion"`
	InstallerURL string `json:"installerUrl"`
}

type versionHistory struct {
	Changes []versionChange `json:"changes"`
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

	// Track app versions
	fmt.Println("\n📦 Tracking app versions...")
	if err := trackAppVersions(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: failed to track app versions: %v\n", err)
		// Don't exit - version tracking is optional
	}

	fmt.Println("\n✅ Data generation completed successfully!")
}

func getGitHubCommits() ([]commitData, error) {
	commits := make(map[string]commitData) // date -> commitData
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
			count, macCount, windowsCount, err := getAppCountAtCommit(gc.Sha)
			if err != nil {
				fmt.Printf("⚠️  Warning: failed to get app count for commit %s: %v\n", gc.Sha[:7], err)
				continue
			}

			commits[dateStr] = commitData{
				date:         dateStr,
				count:        count,
				macCount:     macCount,
				windowsCount: windowsCount,
			}
			fmt.Printf("  ✓ %s: %d apps (%d Mac, %d Windows)\n", dateStr, count, macCount, windowsCount)
		}

		// If we got fewer than perPage results, we're done
		if len(githubCommits) < perPage {
			break
		}

		page++
	}

	// Convert to slice and sort
	result := make([]commitData, 0, len(commits))
	for _, data := range commits {
		result = append(result, data)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].date < result[j].date
	})

	return result, nil
}

func getAppCountAtCommit(sha string) (total int, macCount int, windowsCount int, err error) {
	// Use raw GitHub URL to get file content at specific commit
	url := fmt.Sprintf("%s/%s/%s/%s/%s",
		githubRawBase, repoOwner, repoName, sha, appsJSONPath)

	resp, err := http.Get(url)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to fetch file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, 0, fmt.Errorf("failed to fetch file (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to read response: %w", err)
	}

	var data struct {
		Apps []struct {
			Platform string `json:"platform"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to parse JSON: %w", err)
	}

	total = len(data.Apps)
	for _, app := range data.Apps {
		if app.Platform == "darwin" {
			macCount++
		} else if app.Platform == "windows" {
			windowsCount++
		}
	}

	return total, macCount, windowsCount, nil
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

	// Create maps of commit dates to counts
	commitCounts := make(map[string]int)
	commitMacCounts := make(map[string]int)
	commitWindowsCounts := make(map[string]int)
	for _, commit := range commits {
		commitCounts[commit.date] = commit.count
		commitMacCounts[commit.date] = commit.macCount
		commitWindowsCounts[commit.date] = commit.windowsCount
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
	if err := writer.Write([]string{"date", "app_count", "apps_added_since_previous", "mac_count", "windows_count"}); err != nil {
		return fmt.Errorf("failed to write CSV header: %w", err)
	}

	currentDate := firstDate
	currentCount := 0
	lastKnownCount := 0
	lastWrittenCount := 0
	currentMacCount := 0
	lastKnownMacCount := 0
	currentWindowsCount := 0
	lastKnownWindowsCount := 0
	entryCount := 0

	for !currentDate.After(endDate) {
		dateStr := currentDate.Format("2006-01-02")

		// Check if this date has a commit
		if count, exists := commitCounts[dateStr]; exists {
			currentCount = count
			lastKnownCount = count
		}
		if macCount, exists := commitMacCounts[dateStr]; exists {
			currentMacCount = macCount
			lastKnownMacCount = macCount
		}
		if windowsCount, exists := commitWindowsCounts[dateStr]; exists {
			currentWindowsCount = windowsCount
			lastKnownWindowsCount = windowsCount
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
		displayMacCount := lastKnownMacCount
		if currentMacCount > 0 {
			displayMacCount = currentMacCount
		}
		displayWindowsCount := lastKnownWindowsCount
		if currentWindowsCount > 0 {
			displayWindowsCount = currentWindowsCount
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
			fmt.Sprintf("%d", displayMacCount),
			fmt.Sprintf("%d", displayWindowsCount),
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
		if _, exists := commitMacCounts[dateStr]; !exists {
			currentMacCount = 0
		}
		if _, exists := commitWindowsCounts[dateStr]; !exists {
			currentWindowsCount = 0
		}

		currentDate = currentDate.AddDate(0, 0, 1)
		entryCount++
	}

	fmt.Printf("✅ Generated: %s\n", outputCSV)
	fmt.Printf("📊 Total entries: %d\n", entryCount)
	fmt.Printf("📈 Final app count: %d\n", lastWrittenCount)

	return nil
}

func trackAppVersions() error {
	// Fetch current apps list
	appsJSONURL := fmt.Sprintf("%s/%s/%s/main/%s", githubRawBase, repoOwner, repoName, appsJSONPath)
	resp, err := http.Get(appsJSONURL)
	if err != nil {
		return fmt.Errorf("failed to fetch apps.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch apps.json (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var appsData struct {
		Apps []struct {
			Name     string `json:"name"`
			Slug     string `json:"slug"`
			Platform string `json:"platform"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(body, &appsData); err != nil {
		return fmt.Errorf("failed to parse apps.json: %w", err)
	}

	// Fetch versions for each app
	versions := make([]appVersionInfo, 0, len(appsData.Apps))
	for _, app := range appsData.Apps {
		version, installerURL, err := fetchAppVersionAndURL(app.Slug, app.Platform)
		if err != nil {
			// If version fetch fails, still include the app with empty version
			fmt.Printf("  ⚠️  Warning: failed to get version for %s/%s: %v\n", app.Slug, app.Platform, err)
			versions = append(versions, appVersionInfo{
				Slug:         app.Slug,
				Name:         app.Name,
				Platform:     app.Platform,
				Version:      "",
				InstallerURL: "",
			})
			continue
		}
		versions = append(versions, appVersionInfo{
			Slug:         app.Slug,
			Name:         app.Name,
			Platform:     app.Platform,
			Version:      version,
			InstallerURL: installerURL,
		})
		fmt.Printf("  ✓ %s (%s): %s\n", app.Name, app.Platform, version)
	}

	// Load existing versions to compare
	existingVersions, _ := loadExistingVersions()

	// Check if versions changed
	var existingApps []appVersionInfo
	if existingVersions != nil {
		existingApps = existingVersions.Apps
	}
	versionsChanged := !versionsEqual(existingApps, versions)

	// Save new versions
	versionsData := appVersionsData{
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
		Apps:        versions,
	}

	jsonData, err := json.MarshalIndent(versionsData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal versions: %w", err)
	}

	if err := os.WriteFile(versionsJSON, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write versions file: %w", err)
	}

	if versionsChanged {
		fmt.Printf("✅ Versions updated: %s\n", versionsJSON)
		if existingVersions != nil {
			fmt.Println("   📝 Version changes detected!")
			// Track version changes for RSS feed
			if err := trackVersionChanges(existingApps, versions); err != nil {
				fmt.Printf("⚠️  Warning: failed to track version changes: %v\n", err)
			}
		}
	} else {
		fmt.Printf("✅ Versions checked: %s (no changes)\n", versionsJSON)
	}

	return nil
}

func trackVersionChanges(oldVersions, newVersions []appVersionInfo) error {
	// Load existing history
	history, err := loadVersionHistory()
	if err != nil {
		history = &versionHistory{Changes: []versionChange{}}
	}

	// Create maps for comparison
	oldMap := make(map[string]appVersionInfo)
	for _, v := range oldVersions {
		oldMap[v.Slug] = v
	}

	newMap := make(map[string]appVersionInfo)
	for _, v := range newVersions {
		newMap[v.Slug] = v
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Detect version changes
	for slug, newVersion := range newMap {
		oldVersion, exists := oldMap[slug]
		if exists && oldVersion.Version != "" && newVersion.Version != "" && oldVersion.Version != newVersion.Version {
			// Version changed
			change := versionChange{
				Date:         now,
				AppName:      newVersion.Name,
				Slug:         slug,
				Platform:     newVersion.Platform,
				OldVersion:   oldVersion.Version,
				NewVersion:   newVersion.Version,
				InstallerURL: newVersion.InstallerURL,
			}
			history.Changes = append(history.Changes, change)
			fmt.Printf("   📌 %s: %s → %s\n", newVersion.Name, oldVersion.Version, newVersion.Version)
		} else if !exists && newVersion.Version != "" {
			// New app added
			change := versionChange{
				Date:         now,
				AppName:      newVersion.Name,
				Slug:         slug,
				Platform:     newVersion.Platform,
				OldVersion:   "",
				NewVersion:   newVersion.Version,
				InstallerURL: newVersion.InstallerURL,
			}
			history.Changes = append(history.Changes, change)
			fmt.Printf("   🆕 New app: %s (%s)\n", newVersion.Name, newVersion.Version)
		}
	}

	// Keep only last 1000 changes to prevent file from growing too large
	if len(history.Changes) > 1000 {
		history.Changes = history.Changes[len(history.Changes)-1000:]
	}

	// Save history
	jsonData, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal version history: %w", err)
	}

	if err := os.WriteFile(versionHistoryJSON, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write version history: %w", err)
	}

	return nil
}

func loadVersionHistory() (*versionHistory, error) {
	data, err := os.ReadFile(versionHistoryJSON)
	if err != nil {
		if os.IsNotExist(err) {
			return &versionHistory{Changes: []versionChange{}}, nil
		}
		return nil, err
	}

	var history versionHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, err
	}

	return &history, nil
}

func fetchAppVersionAndURL(slug, platform string) (version string, installerURL string, err error) {
	// Construct URL: slug format is "app-name/platform", we need "app-name/platform.json"
	url := fmt.Sprintf("%s/%s.json", appBaseURL, slug)

	resp, err := http.Get(url)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch version file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("failed to fetch version file (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read response: %w", err)
	}

	var versionData struct {
		Versions []struct {
			Version      string `json:"version"`
			InstallerURL string `json:"installer_url"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &versionData); err != nil {
		return "", "", fmt.Errorf("failed to parse version JSON: %w", err)
	}

	if len(versionData.Versions) == 0 {
		return "", "", fmt.Errorf("no versions found")
	}

	// Return the first (latest) version and installer URL
	return versionData.Versions[0].Version, versionData.Versions[0].InstallerURL, nil
}

func loadExistingVersions() (*appVersionsData, error) {
	data, err := os.ReadFile(versionsJSON)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // File doesn't exist yet, that's okay
		}
		return nil, err
	}

	var versions appVersionsData
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, err
	}

	return &versions, nil
}

func versionsEqual(old, new []appVersionInfo) bool {
	if old == nil {
		return false // First time, consider it changed
	}

	if len(old) != len(new) {
		return false
	}

	// Create maps for easier comparison
	oldMap := make(map[string]appVersionInfo)
	for _, v := range old {
		oldMap[v.Slug] = v
	}

	newMap := make(map[string]appVersionInfo)
	for _, v := range new {
		newMap[v.Slug] = v
	}

	// Check if all slugs match
	for slug, newVersion := range newMap {
		oldVersion, exists := oldMap[slug]
		if !exists {
			return false // New app added
		}
		if oldVersion.Version != newVersion.Version {
			return false // Version changed
		}
	}

	// Check if any apps were removed
	for slug := range oldMap {
		if _, exists := newMap[slug]; !exists {
			return false // App removed
		}
	}

	return true
}
