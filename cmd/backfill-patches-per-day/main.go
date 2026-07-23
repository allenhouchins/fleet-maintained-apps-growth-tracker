package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// One-time backfill for data/patches_per_day.csv.
//
// data/version_history.json is a rolling window capped at the last 1000
// changes, but every hourly commit of it is a snapshot of that window, so the
// union of the file across all git revisions recovers the complete change
// history. This tool walks those revisions, dedupes the change entries,
// counts version updates per UTC day (new-app additions, which have an empty
// oldVersion, are not patches), and writes the zero-filled daily CSV.
//
// Run from the repo root: go run ./cmd/backfill-patches-per-day
// Ongoing maintenance is handled by updatePatchesPerDay in main.go.

const (
	versionHistoryPath = "data/version_history.json"
	outputCSV          = "data/patches_per_day.csv"
)

type versionChange struct {
	Date       string `json:"date"`
	Slug       string `json:"slug"`
	Platform   string `json:"platform"`
	OldVersion string `json:"oldVersion"`
	NewVersion string `json:"newVersion"`
}

type versionHistory struct {
	Changes []versionChange `json:"changes"`
}

func main() {
	fmt.Println("📚 Backfilling patches-per-day from git history of", versionHistoryPath)

	out, err := exec.Command("git", "log", "--format=%H", "--", versionHistoryPath).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: git log failed: %v\n", err)
		os.Exit(1)
	}
	shas := strings.Fields(string(out))
	if len(shas) == 0 {
		fmt.Fprintln(os.Stderr, "❌ Error: no commits found for", versionHistoryPath)
		os.Exit(1)
	}
	fmt.Printf("✅ Found %d revisions\n", len(shas))

	type dayCounts struct{ mac, windows int }
	counts := make(map[string]dayCounts)
	seen := make(map[string]bool)
	totalPatches := 0

	for i, sha := range shas {
		if i%50 == 0 || i == len(shas)-1 {
			fmt.Printf("📦 Processing revision %d/%d...\n", i+1, len(shas))
		}
		blob, err := exec.Command("git", "show", sha+":"+versionHistoryPath).Output()
		if err != nil {
			continue // file may not exist at this revision
		}
		var history versionHistory
		if err := json.Unmarshal(blob, &history); err != nil {
			continue
		}
		for _, c := range history.Changes {
			key := c.Date + "|" + c.Slug + "|" + c.OldVersion + "|" + c.NewVersion
			if seen[key] || len(c.Date) < 10 {
				continue
			}
			seen[key] = true
			if c.OldVersion == "" {
				continue // new app additions are not patches
			}
			day := c.Date[:10]
			dc := counts[day]
			if c.Platform == "windows" {
				dc.windows++
			} else {
				dc.mac++
			}
			counts[day] = dc
			totalPatches++
		}
	}

	if len(counts) == 0 {
		fmt.Fprintln(os.Stderr, "❌ Error: no patch entries found")
		os.Exit(1)
	}

	days := make([]string, 0, len(counts))
	for day := range counts {
		days = append(days, day)
	}
	sort.Strings(days)

	start, err := time.Parse("2006-01-02", days[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: bad start date %q: %v\n", days[0], err)
		os.Exit(1)
	}
	end, _ := time.Parse("2006-01-02", time.Now().UTC().Format("2006-01-02"))

	file, err := os.Create(outputCSV)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: failed to create %s: %v\n", outputCSV, err)
		os.Exit(1)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()
	if err := writer.Write([]string{"date", "patch_count", "mac_count", "windows_count"}); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: failed to write CSV: %v\n", err)
		os.Exit(1)
	}

	rows := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		dc := counts[day]
		if err := writer.Write([]string{
			day,
			fmt.Sprintf("%d", dc.mac+dc.windows),
			fmt.Sprintf("%d", dc.mac),
			fmt.Sprintf("%d", dc.windows),
		}); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error: failed to write CSV row: %v\n", err)
			os.Exit(1)
		}
		rows++
	}

	fmt.Printf("✅ Wrote %s: %d days (%s to %s), %d patches\n", outputCSV, rows, days[0], end.Format("2006-01-02"), totalPatches)
}
