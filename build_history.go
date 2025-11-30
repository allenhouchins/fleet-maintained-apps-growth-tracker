package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// build_history.go - One-time script to build historical version changes
// Run this separately: go run build_history.go
func main() {
	fmt.Println("📚 Building Historical Version Changes")
	fmt.Println("=====================================")
	fmt.Println("This will process commits to build version history.")
	fmt.Println("This may take several minutes...\n")

	// Get all commits that changed apps.json
	fmt.Println("📥 Fetching commit SHAs for apps.json...")
	commitSHAs, err := getAllCommitSHAs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: failed to get commit SHAs: %v\n", err)
		os.Exit(1)
	}

	if len(commitSHAs) == 0 {
		fmt.Fprintf(os.Stderr, "❌ Error: no commits found\n")
		os.Exit(1)
	}

	// Limit to most recent 50 commits to avoid timeouts
	maxCommits := 50
	if len(commitSHAs) > maxCommits {
		commitSHAs = commitSHAs[len(commitSHAs)-maxCommits:]
		fmt.Printf("⚠️  Limiting to most recent %d commits to avoid timeouts\n", maxCommits)
	}

	fmt.Printf("✅ Processing %d commits...\n\n", len(commitSHAs))

	// Process commits in chronological order (oldest first)
	history, _ := loadVersionHistory()
	previousVersions := make(map[string]appVersionInfo)
	processedCount := 0

	for i, commit := range commitSHAs {
		// Show progress every 5 commits
		if i%5 == 0 || i == len(commitSHAs)-1 {
			fmt.Printf("📦 Processing commit %d/%d (%s)...\n", i+1, len(commitSHAs), commit.Sha[:7])
		}

		// Fetch app versions at this commit
		currentVersions, err := getAppVersionsAtCommit(commit.Sha, commit.Date)
		if err != nil {
			// Skip commits where we can't fetch versions
			continue
		}

		processedCount++

		// Compare with previous versions
		if len(previousVersions) > 0 {
			for slug, currentVersion := range currentVersions {
				previousVersion, exists := previousVersions[slug]

				if !exists && currentVersion.Version != "" {
					// New app added
					change := versionChange{
						Date:         commit.Date,
						AppName:      currentVersion.Name,
						Slug:         slug,
						Platform:     currentVersion.Platform,
						OldVersion:   "",
						NewVersion:   currentVersion.Version,
						InstallerURL: currentVersion.InstallerURL,
					}
					history.Changes = append(history.Changes, change)
					fmt.Printf("  🆕 New app: %s (%s)\n", currentVersion.Name, currentVersion.Version)
				} else if exists && previousVersion.Version != "" && currentVersion.Version != "" && previousVersion.Version != currentVersion.Version {
					// Version changed
					change := versionChange{
						Date:         commit.Date,
						AppName:      currentVersion.Name,
						Slug:         slug,
						Platform:     currentVersion.Platform,
						OldVersion:   previousVersion.Version,
						NewVersion:   currentVersion.Version,
						InstallerURL: currentVersion.InstallerURL,
					}
					history.Changes = append(history.Changes, change)
					fmt.Printf("  📌 %s: %s → %s\n", currentVersion.Name, previousVersion.Version, currentVersion.Version)
				}
			}
		}

		// Update previous versions for next iteration
		previousVersions = currentVersions

		// Add a small delay to avoid rate limiting (every 5 commits)
		if i%5 == 0 && i < len(commitSHAs)-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Sort by date (newest first)
	sort.Slice(history.Changes, func(i, j int) bool {
		return history.Changes[i].Date > history.Changes[j].Date
	})

	// Keep only last 1000 changes
	if len(history.Changes) > 1000 {
		history.Changes = history.Changes[:1000]
	}

	// Save history
	jsonData, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: failed to marshal version history: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(versionHistoryJSON, jsonData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: failed to write version history: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ Built historical version changes: %d entries\n", len(history.Changes))
	fmt.Println("✅ Historical data saved to:", versionHistoryJSON)
	fmt.Println("\nNow run: go run generate_rss.go")
}
