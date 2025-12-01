package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

const (
	versionsJSON       = "data/app_versions.json"
	versionHistoryJSON = "data/version_history.json"
	outputRSS          = "feed.xml"
	siteURL            = "https://fmalibrary.com"
)

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

func generateRSS() error {
	fmt.Println("📡 Generating RSS feed...")

	// Load current versions
	currentVersions, err := loadVersions()
	if err != nil {
		return fmt.Errorf("failed to load current versions: %w", err)
	}

	// Load version history
	history, err := loadVersionHistory()
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to load version history: %v\n", err)
		history = &versionHistory{Changes: []versionChange{}}
	}

	// Sort changes by date (newest first)
	changes := history.Changes
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Date > changes[j].Date
	})

	// Limit to last 50 changes for RSS feed
	if len(changes) > 50 {
		changes = changes[:50]
	}

	// Generate RSS feed
	rssContent := generateRSSContent(currentVersions, changes)

	if err := os.WriteFile(outputRSS, []byte(rssContent), 0644); err != nil {
		return fmt.Errorf("failed to write RSS file: %w", err)
	}

	fmt.Printf("✅ Generated: %s\n", outputRSS)
	fmt.Printf("   📝 %d version updates in feed\n", len(changes))

	return nil
}

func loadVersions() (*appVersionsData, error) {
	data, err := os.ReadFile(versionsJSON)
	if err != nil {
		return nil, err
	}

	var versions appVersionsData
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, err
	}

	return &versions, nil
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

func generateRSSContent(currentVersions *appVersionsData, changes []versionChange) string {
	lastBuildDate := time.Now().UTC().Format(time.RFC1123Z)
	if currentVersions != nil && currentVersions.LastUpdated != "" {
		if t, err := time.Parse(time.RFC3339, currentVersions.LastUpdated); err == nil {
			lastBuildDate = t.UTC().Format(time.RFC1123Z)
		}
	}

	rss := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
  <channel>
    <title>Fleet-maintained apps</title>
    <link>` + siteURL + `</link>
    <description>Track version updates and new app additions for Fleet-maintained apps. Get notified when apps are updated with new versions or when new apps are added to the library.</description>
    <language>en-us</language>
    <lastBuildDate>` + lastBuildDate + `</lastBuildDate>
    <atom:link href="` + siteURL + `/feed.xml" rel="self" type="application/rss+xml"/>
    <image>
      <url>` + siteURL + `/cloud-city.png</url>
      <title>Fleet-maintained apps</title>
      <link>` + siteURL + `</link>
    </image>
`

	// Add items for each version change
	for _, change := range changes {
		var title, description string
		if change.OldVersion == "" {
			// New app added
			title = fmt.Sprintf("New App: %s %s (%s)", change.AppName, change.NewVersion, getPlatformLabel(change.Platform))
			description = fmt.Sprintf("%s has been added to the Fleet-maintained apps library with version %s on %s.", change.AppName, change.NewVersion, formatDate(change.Date))
		} else {
			// Version update
			title = fmt.Sprintf("%s %s → %s (%s)", change.AppName, change.OldVersion, change.NewVersion, getPlatformLabel(change.Platform))
			description = fmt.Sprintf("%s has been updated from version %s to %s on %s.", change.AppName, change.OldVersion, change.NewVersion, formatDate(change.Date))
		}

		if change.InstallerURL != "" {
			description += fmt.Sprintf(" <a href=\"%s\">Download installer</a>", escapeXML(change.InstallerURL))
		}

		// Parse date for pubDate
		pubDate := lastBuildDate
		if t, err := time.Parse(time.RFC3339, change.Date); err == nil {
			pubDate = t.UTC().Format(time.RFC1123Z)
		}

		guid := fmt.Sprintf("%s-%s-%s", change.Slug, change.OldVersion, change.NewVersion)

		rss += `    <item>
      <title>` + escapeXML(title) + `</title>
      <link>` + siteURL + `</link>
      <description>` + escapeXML(description) + `</description>
      <pubDate>` + pubDate + `</pubDate>
      <guid isPermaLink="false">` + escapeXML(guid) + `</guid>
    </item>
`
	}

	rss += `  </channel>
</rss>`

	return rss
}

func getPlatformLabel(platform string) string {
	if platform == "darwin" {
		return "Mac"
	}
	return "Windows"
}

func formatDate(dateStr string) string {
	if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
		return t.Format("January 2, 2006")
	}
	return dateStr
}

func escapeXML(s string) string {
	result := ""
	for _, r := range s {
		switch r {
		case '<':
			result += "&lt;"
		case '>':
			result += "&gt;"
		case '&':
			result += "&amp;"
		case '"':
			result += "&quot;"
		case '\'':
			result += "&apos;"
		default:
			result += string(r)
		}
	}
	return result
}

func main() {
	if err := generateRSS(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
}
