package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	csvFile          = "data/apps_growth.csv"
	patchesCSVFile   = "data/patches_per_day.csv"
	outputHTML       = "index.html"
	appsJSONURL      = "https://raw.githubusercontent.com/fleetdm/fleet/main/ee/maintained-apps/outputs/apps.json"
	appBaseURL       = "https://raw.githubusercontent.com/fleetdm/fleet/main/ee/maintained-apps/outputs"
	iconsBaseURL     = "https://raw.githubusercontent.com/fleetdm/fleet/main/website/assets/images"
	securityInfoJSON = "data/app_security_info.json"

	// The app list is no longer inlined into index.html. It is split into a
	// small index (what the grid needs to paint) and a detail file (what the
	// modal needs), both fetched after first paint.
	appsIndexJSON  = "data/apps_index.json"
	appsDetailJSON = "data/apps_detail.json"
)

type csvData struct {
	Dates           []string `json:"dates"`
	Counts          []int    `json:"counts"`
	Additions       []int    `json:"additions"`
	MacCounts       []int    `json:"macCounts"`
	WindowsCounts   []int    `json:"windowsCounts"`
	GrowthDates     []string `json:"growthDates"`
	GrowthCounts    []int    `json:"growthCounts"`
	GrowthAdditions []int    `json:"growthAdditions"`
}

type patchesCSVData struct {
	Dates         []string `json:"dates"`
	MacCounts     []int    `json:"macCounts"`
	WindowsCounts []int    `json:"windowsCounts"`
}

type appData struct {
	Name         string               `json:"name"`
	Slug         string               `json:"slug"`
	Platform     string               `json:"platform"`
	Description  string               `json:"description"`
	Version      string               `json:"version"`
	InstallerURL string               `json:"installerUrl"`
	SecurityInfo *appSecurityInfoData `json:"securityInfo,omitempty"`
}

type appSecurityInfoData struct {
	Name         string                `json:"name,omitempty"`
	Sha256       string                `json:"sha256,omitempty"`
	Cdhash       string                `json:"cdhash,omitempty"`
	SigningID    string                `json:"signingId,omitempty"`
	TeamID       string                `json:"teamId,omitempty"`
	Publisher    string                `json:"publisher,omitempty"`    // Windows: Certificate subject
	Issuer       string                `json:"issuer,omitempty"`       // Windows: Certificate authority
	SerialNumber string                `json:"serialNumber,omitempty"` // Windows: Certificate serial
	Thumbprint   string                `json:"thumbprint,omitempty"`   // Windows: Certificate thumbprint
	Timestamp    string                `json:"timestamp,omitempty"`    // Windows: Signing timestamp
	LastUpdated  string                `json:"lastUpdated,omitempty"`
	Apps         []appSecurityInfoData `json:"apps,omitempty"` // For suites with multiple apps
}

type appsJSON struct {
	Apps []appData `json:"apps"`
}

type securityInfoItem struct {
	Slug         string             `json:"slug"`
	Name         string             `json:"name,omitempty"`
	Sha256       string             `json:"sha256,omitempty"`
	Cdhash       string             `json:"cdhash,omitempty"`
	SigningID    string             `json:"signingId,omitempty"`
	TeamID       string             `json:"teamId,omitempty"`
	Publisher    string             `json:"publisher,omitempty"`
	Issuer       string             `json:"issuer,omitempty"`
	SerialNumber string             `json:"serialNumber,omitempty"`
	Thumbprint   string             `json:"thumbprint,omitempty"`
	Timestamp    string             `json:"timestamp,omitempty"`
	LastUpdated  string             `json:"lastUpdated"`
	Apps         []securityInfoItem `json:"apps,omitempty"` // For suites with multiple apps
}

type securityInfoData struct {
	Apps []securityInfoItem `json:"apps"`
}

// appIndexEntry is the per-app record the grid paints from. Keys are short
// because this file ships 1,400+ of them.
type appIndexEntry struct {
	N string `json:"n"` // name
	S string `json:"s"` // slug
	P string `json:"p"` // platform
	V string `json:"v"` // version
}

type appIndexFile struct {
	LastUpdated string          `json:"lastUpdated"`
	Apps        []appIndexEntry `json:"apps"`
}

// appDetailEntry is everything the modal needs and the grid does not.
type appDetailEntry struct {
	D   string               `json:"d,omitempty"`   // description
	U   string               `json:"u,omitempty"`   // installer URL
	Sec *appSecurityInfoData `json:"sec,omitempty"` // signing / hash info
}

type appDetailFile struct {
	Apps map[string]appDetailEntry `json:"apps"`
}

// chartPayload is inlined into the page: it is small, and the charts are
// above the fold, so a round trip for it would delay first paint.
// Dates are emitted only when the series is not a contiguous daily run.
type chartPayload struct {
	Start string   `json:"start"`
	Dates []string `json:"dates,omitempty"`
	Total []int    `json:"total,omitempty"`
	Mac   []int    `json:"mac"`
	Win   []int    `json:"win"`
	Added []int    `json:"added,omitempty"`
}

func generateHTML() error {
	fmt.Println("🎨 Generating HTML visualization...")

	data, err := loadCSVData()
	if err != nil {
		return fmt.Errorf("failed to load CSV data: %w", err)
	}

	patches, err := loadPatchesData()
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to load patches data: %v\n", err)
		patches = &patchesCSVData{Dates: []string{}, MacCounts: []int{}, WindowsCounts: []int{}}
	}

	apps, err := fetchAppsData()
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to fetch apps data: %v\n", err)
		apps = &appsJSON{Apps: []appData{}}
	} else {
		fmt.Printf("✅ Fetched %d apps\n", len(apps.Apps))
	}

	// Load security info and merge with apps
	securityInfo, _ := loadSecurityInfo()
	mergeSecurityInfo(apps, securityInfo)

	reconcileLatest(data, apps)

	if err := writeAppDataFiles(apps); err != nil {
		return fmt.Errorf("failed to write app data files: %w", err)
	}

	htmlContent := generateHTMLContent(data, patches, apps)

	if err := os.WriteFile(outputHTML, []byte(htmlContent), 0644); err != nil {
		return fmt.Errorf("failed to write HTML file: %w", err)
	}

	fmt.Printf("✅ Generated %s\n", outputHTML)
	fmt.Printf("   Total days: %d\n", len(data.Dates))
	fmt.Printf("   Growth events: %d\n", len(data.GrowthDates))
	fmt.Printf("   Patch days: %d\n", len(patches.Dates))

	return nil
}

func loadCSVData() (*csvData, error) {
	file, err := os.Open(csvFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("CSV file is empty or has no data rows")
	}

	data := &csvData{
		Dates:           make([]string, 0),
		Counts:          make([]int, 0),
		Additions:       make([]int, 0),
		MacCounts:       make([]int, 0),
		WindowsCounts:   make([]int, 0),
		GrowthDates:     make([]string, 0),
		GrowthCounts:    make([]int, 0),
		GrowthAdditions: make([]int, 0),
	}

	for i := 1; i < len(records); i++ {
		row := records[i]
		if len(row) < 3 {
			continue
		}

		dateStr := row[0]
		var count, added, macCount, windowsCount int
		fmt.Sscanf(row[1], "%d", &count)
		fmt.Sscanf(row[2], "%d", &added)
		if len(row) >= 4 {
			fmt.Sscanf(row[3], "%d", &macCount)
		}
		if len(row) >= 5 {
			fmt.Sscanf(row[4], "%d", &windowsCount)
		}

		data.Dates = append(data.Dates, dateStr)
		data.Counts = append(data.Counts, count)
		data.Additions = append(data.Additions, added)
		data.MacCounts = append(data.MacCounts, macCount)
		data.WindowsCounts = append(data.WindowsCounts, windowsCount)

		if added > 0 {
			data.GrowthDates = append(data.GrowthDates, dateStr)
			data.GrowthCounts = append(data.GrowthCounts, count)
			data.GrowthAdditions = append(data.GrowthAdditions, added)
		}
	}

	return data, nil
}

func loadPatchesData() (*patchesCSVData, error) {
	data := &patchesCSVData{Dates: []string{}, MacCounts: []int{}, WindowsCounts: []int{}}

	file, err := os.Open(patchesCSVFile)
	if err != nil {
		if os.IsNotExist(err) {
			return data, nil
		}
		return nil, err
	}
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(records); i++ {
		row := records[i]
		if len(row) < 4 {
			continue
		}
		var mac, windows int
		fmt.Sscanf(row[2], "%d", &mac)
		fmt.Sscanf(row[3], "%d", &windows)
		data.Dates = append(data.Dates, row[0])
		data.MacCounts = append(data.MacCounts, mac)
		data.WindowsCounts = append(data.WindowsCounts, windows)
	}

	return data, nil
}

func fetchAppsData() (*appsJSON, error) {
	resp, err := http.Get(appsJSONURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch apps.json: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch apps.json (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var apps appsJSON
	if err := json.Unmarshal(body, &apps); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Fetch version and installer URL information for each app
	for i := range apps.Apps {
		version, installerURL, err := fetchAppVersionAndURL(apps.Apps[i].Slug, apps.Apps[i].Platform)
		if err != nil {
			// If version fetch fails, continue with empty version
			apps.Apps[i].Version = ""
			apps.Apps[i].InstallerURL = ""
			continue
		}
		apps.Apps[i].Version = version
		apps.Apps[i].InstallerURL = installerURL
	}

	return &apps, nil
}

func loadSecurityInfo() (*securityInfoData, error) {
	data, err := os.ReadFile(securityInfoJSON)
	if err != nil {
		if os.IsNotExist(err) {
			return &securityInfoData{Apps: []securityInfoItem{}}, nil
		}
		return nil, err
	}

	var security securityInfoData
	if err := json.Unmarshal(data, &security); err != nil {
		return nil, err
	}

	return &security, nil
}

func mergeSecurityInfo(apps *appsJSON, security *securityInfoData) {
	if security == nil {
		return
	}
	// Create a map of security info by slug (both macOS and Windows entries)
	securityMap := make(map[string]securityInfoItem)
	for _, sec := range security.Apps {
		securityMap[sec.Slug] = sec
	}

	// Merge security info into apps (both macOS and Windows)
	for i := range apps.Apps {
		if sec, exists := securityMap[apps.Apps[i].Slug]; exists {
			securityData := &appSecurityInfoData{
				Sha256:       sec.Sha256,
				Cdhash:       sec.Cdhash,
				SigningID:    sec.SigningID,
				TeamID:       sec.TeamID,
				Publisher:    sec.Publisher,
				Issuer:       sec.Issuer,
				SerialNumber: sec.SerialNumber,
				Thumbprint:   sec.Thumbprint,
				Timestamp:    sec.Timestamp,
				LastUpdated:  sec.LastUpdated,
			}

			// If this is a suite with multiple apps, include them
			if len(sec.Apps) > 0 {
				securityData.Apps = make([]appSecurityInfoData, len(sec.Apps))
				for j, app := range sec.Apps {
					securityData.Apps[j] = appSecurityInfoData{
						Name:         app.Name,
						Sha256:       app.Sha256,
						Cdhash:       app.Cdhash,
						SigningID:    app.SigningID,
						TeamID:       app.TeamID,
						Publisher:    app.Publisher,
						Issuer:       app.Issuer,
						SerialNumber: app.SerialNumber,
						Thumbprint:   app.Thumbprint,
						Timestamp:    app.Timestamp,
						LastUpdated:  app.LastUpdated,
					}
				}
			}

			apps.Apps[i].SecurityInfo = securityData
		}
	}
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

func main() {
	if err := generateHTML(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
}

// writeAppDataFiles splits the app list into the grid index and the modal
// detail file. Inlining both into index.html used to account for ~90% of the
// document, which blocked first paint on data most visitors never look at.
// reconcileLatest brings today's data point in line with the live app list.
//
// main.go writes apps_growth.csv once a day, but this generator re-runs on
// every Pages deploy -- including the deploys the security-info workflows
// trigger -- and re-fetches apps.json each time. Without this, the headline
// figure and the end of the growth chart would lag the app grid by whatever
// Fleet merged since the last daily run.
//
// It only ever rewrites the row representing today, and only when the live
// list was actually fetched, so a failed fetch or a stale series is left alone
// rather than being overwritten with something wrong.
func reconcileLatest(data *csvData, apps *appsJSON) {
	if len(apps.Apps) == 0 || len(data.Dates) == 0 {
		return // nothing fetched, or nothing recorded: keep what main.go wrote
	}

	last := len(data.Dates) - 1
	if len(data.Counts) <= last || len(data.MacCounts) <= last ||
		len(data.WindowsCounts) <= last || len(data.Additions) <= last {
		return // series are ragged; do not guess
	}

	// Same clock convention main.go uses to decide which day the last row is.
	if data.Dates[last] != time.Now().Format("2006-01-02") {
		fmt.Printf("ℹ️  Latest row is %s, not today -- leaving it as recorded\n", data.Dates[last])
		return
	}

	total := len(apps.Apps)
	mac, win := 0, 0
	for _, a := range apps.Apps {
		switch a.Platform {
		case "darwin":
			mac++
		case "windows":
			win++
		}
	}

	if total == data.Counts[last] && mac == data.MacCounts[last] && win == data.WindowsCounts[last] {
		return // already in step
	}

	fmt.Printf("ℹ️  Reconciling %s with the live list: total %d->%d, mac %d->%d, windows %d->%d\n",
		data.Dates[last], data.Counts[last], total,
		data.MacCounts[last], mac, data.WindowsCounts[last], win)

	data.Counts[last] = total
	data.MacCounts[last] = mac
	data.WindowsCounts[last] = win
	if last > 0 {
		// Keep "added since previous" consistent with the count we just set.
		data.Additions[last] = total - data.Counts[last-1]
	}
}

func writeAppDataFiles(apps *appsJSON) error {
	// A failed upstream fetch leaves apps empty. Writing that out would replace
	// a good app list with an empty one -- and these files are committed -- so
	// keep whatever is already on disk and let the next run refresh it.
	if len(apps.Apps) == 0 {
		fmt.Printf("⚠️  No apps fetched; leaving %s and %s as they are\n",
			appsIndexJSON, appsDetailJSON)
		return nil
	}

	index := appIndexFile{
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
		Apps:        make([]appIndexEntry, 0, len(apps.Apps)),
	}
	detail := appDetailFile{Apps: make(map[string]appDetailEntry, len(apps.Apps))}

	for _, a := range apps.Apps {
		index.Apps = append(index.Apps, appIndexEntry{
			N: a.Name, S: a.Slug, P: a.Platform, V: a.Version,
		})
		// Only record a detail entry when there is something to show.
		if a.Description != "" || a.InstallerURL != "" || a.SecurityInfo != nil {
			detail.Apps[a.Slug] = appDetailEntry{
				D: a.Description, U: a.InstallerURL, Sec: a.SecurityInfo,
			}
		}
	}

	indexBytes, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if err := os.WriteFile(appsIndexJSON, indexBytes, 0644); err != nil {
		return err
	}

	detailBytes, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	if err := os.WriteFile(appsDetailJSON, detailBytes, 0644); err != nil {
		return err
	}

	fmt.Printf("✅ Generated %s (%d KB) and %s (%d KB)\n",
		appsIndexJSON, len(indexBytes)/1024, appsDetailJSON, len(detailBytes)/1024)
	return nil
}

// isDailyRun reports whether dates are a contiguous run of calendar days, which
// lets the page derive them from a start date instead of shipping every string.
func isDailyRun(dates []string) bool {
	if len(dates) < 2 {
		return true
	}
	prev, err := time.Parse("2006-01-02", dates[0])
	if err != nil {
		return false
	}
	for _, d := range dates[1:] {
		cur, err := time.Parse("2006-01-02", d)
		if err != nil {
			return false
		}
		if cur.Sub(prev) != 24*time.Hour {
			return false
		}
		prev = cur
	}
	return true
}

func generateHTMLContent(data *csvData, patches *patchesCSVData, apps *appsJSON) string {
	// The chart series are small and sit above the fold, so they are inlined.
	// A contiguous daily run ships only a start date; the page derives the rest.
	growth := chartPayload{Total: data.Counts, Mac: data.MacCounts, Win: data.WindowsCounts, Added: data.Additions}
	if len(data.Dates) > 0 {
		growth.Start = data.Dates[0]
	}
	if !isDailyRun(data.Dates) {
		growth.Dates = data.Dates
	}

	patch := chartPayload{Mac: patches.MacCounts, Win: patches.WindowsCounts}
	if len(patches.Dates) > 0 {
		patch.Start = patches.Dates[0]
	}
	if !isDailyRun(patches.Dates) {
		patch.Dates = patches.Dates
	}

	growthJSON, _ := json.Marshal(growth)
	patchJSON, _ := json.Marshal(patch)

	// Generate timestamp for when this HTML was created (in CST)
	cstLocation, err := time.LoadLocation("America/Chicago")
	if err != nil {
		cstLocation = time.UTC
	}
	lastUpdated := time.Now().In(cstLocation).Format("January 2, 2006 at 3:04 PM MST")

	appCount := fmt.Sprintf("%d", len(apps.Apps))

	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="description" content="Track the growth of Fleet-maintained apps over time. View app versions, download installers, and explore the expanding library of macOS and Windows applications.">
<title>Fleet-maintained app library</title>
<link rel="alternate" type="application/rss+xml" title="Fleet-maintained app updates" href="feed.xml">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=Roboto+Mono:wght@400&display=swap">
<style>
/* ============================================================
   Fleet design tokens.
   Palette and type taken from fleetdm/fleet
   website/assets/styles/mixins-and-variables/{colors,typography}.less
   ============================================================ */
:root {
  --fleet-black:      #192147;
  --fleet-black-75:   #515774;
  --fleet-black-50:   #8b8fa2;
  --fleet-black-25:   #c5c7d1;
  --fleet-black-10:   #e2e4ea;
  --fleet-black-5:    #f2f2f5;
  --fleet-green:      #009a7d;
  --fleet-green-ring: #008a70;
  --fleet-blue:       #6a67fe;
  --fleet-blue-10:    #f1f0ff;
  --fleet-red:        #ff5c83;
  --off-white:        #f9fafc;
  --fleet-wash:       #e8f1f6;

  --brand:     var(--fleet-blue);
  --brand-ink: #4b48d6;
  --cta:       var(--fleet-green);
  --cta-ring:  var(--fleet-green-ring);

  /* layout.less:19 — background: linear-gradient(180deg,#E8F1F6 0%,#FFFFFF 100px) */
  --bg:        #ffffff;
  --bg-grad:   linear-gradient(180deg, var(--fleet-wash) 0%, #ffffff 100px);
  --grad-top:  40px;

  --surface:   #ffffff;
  --surface-2: var(--fleet-black-5);
  --border:    var(--fleet-black-10);
  --border-strong: var(--fleet-black-25);
  --text:      var(--fleet-black);
  --text-2:    var(--fleet-black-75);
  --muted:     var(--fleet-black-50);
  --grid:      var(--fleet-black-10);

  --shadow-sm: 0 6px 20px rgba(0,0,0,.05);
  --shadow:    0 6px 20px rgba(0,0,0,.09);

  --r:    8px;
  --r-lg: 12px;
  --font: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  --font-mono: 'Roboto Mono', ui-monospace, SFMono-Regular, Menlo, monospace;
  --maxw: 1140px;
}

/* fleetdm.com has no dark mode, so neither does this. */
:root { color-scheme: light; }

* { box-sizing: border-box; }
html { -webkit-text-size-adjust: 100%; }

body {
  margin: 0;
  background-color: var(--bg);
  background-image: var(--bg-grad);
  background-repeat: no-repeat;
  background-position: center var(--grad-top);
  color: var(--text);
  font-family: var(--font);
  font-size: 14px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}

h1, h2, h3 { font-weight: 800; line-height: 1.2; letter-spacing: -.01em; }
a { color: var(--brand-ink); }
code, .mono { font-family: var(--font-mono); }

.sr-only {
  position: absolute; width: 1px; height: 1px;
  padding: 0; margin: -1px; overflow: hidden;
  clip: rect(0 0 0 0); clip-path: inset(50%);
  white-space: nowrap; border: 0;
}

.wrap { max-width: var(--maxw); margin: 0 auto; padding: 0 16px; }

/* ---------- Top bar ---------- */
.topbar {
  position: sticky; top: 0; z-index: 20;
  background: rgba(255,255,255,.95);
  backdrop-filter: saturate(180%) blur(12px);
  border-bottom: 1px solid var(--border);
}
.topbar .wrap {
  display: flex; align-items: center; justify-content: space-between;
  gap: 16px; height: 60px;
}
.brand { display: flex; align-items: center; gap: 11px; min-width: 0; flex: 1; }
.brand .sep { width: 1px; height: 18px; background: var(--border); flex: none; }
/* The wordmark is gone, so this is the only label in the bar: let it shrink
   and ellipsize on narrow screens rather than disappear. */
.brand .what {
  font-size: 15px; font-weight: 600; color: var(--text);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0;
}
@media (max-width: 560px) {
  /* Give the title the room by reducing the CTA to its icon. */
  .topbar .btn-primary { padding: 7.5px 12px; }
  .topbar .btn-primary .label { display: none; }
}

/* ---------- Fleet primary button ----------
   Port of .primary-button(normal) from buttons.less, spotlight included. */
.btn-primary {
  --primary-button: 1;
  --spotlight-x: 35%; --spotlight-y: 30%;
  position: relative; isolation: isolate; overflow: hidden;
  display: inline-flex; justify-content: center; align-items: center; gap: 8px;
  border: none; cursor: pointer; color: #fff;
  font-family: inherit; font-weight: 700; text-decoration: none;
  background-color: var(--cta);
  background-image: linear-gradient(180deg, rgba(255,255,255,.12) 0%, rgba(255,255,255,0) 60%);
  box-shadow: 0 0 0 1px var(--cta-ring), 0 2px 6px -1px rgba(0,154,125,.225);
  transition: transform .35s cubic-bezier(.2,.8,.2,1), box-shadow .35s cubic-bezier(.2,.8,.2,1);
  min-height: 36px; padding: 7.5px 16px; border-radius: 12px;
  font-size: 14px; line-height: 150%;
}
.btn-primary::before, .btn-primary::after {
  content: ''; position: absolute; inset: 0; z-index: -1;
  border-radius: inherit; pointer-events: none; opacity: 0;
  transition: opacity .4s ease;
}
.btn-primary::after {
  background: radial-gradient(120px circle at var(--spotlight-x) var(--spotlight-y), rgba(133,255,198,.25), rgba(133,255,198,0) 70%);
}
.btn-primary::before {
  padding: 1px;
  background: radial-gradient(80px circle at var(--spotlight-x) var(--spotlight-y), rgba(133,255,198,.8), rgba(133,255,198,0) 70%);
  -webkit-mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0);
  -webkit-mask-composite: xor;
          mask: linear-gradient(#000 0 0) content-box exclude, linear-gradient(#000 0 0);
}
.btn-primary:hover, .btn-primary:focus { color: #fff; text-decoration: none; }
.btn-primary:hover {
  transform: translateY(-1px);
  box-shadow: inset 0 1px 0 rgba(255,255,255,.45), 0 0 0 1px var(--cta-ring), 0 4px 9px -2px rgba(0,154,125,.275);
}
.btn-primary:hover::before, .btn-primary:hover::after { opacity: 1; }
.btn-primary:active { transform: scale(.98); transition-duration: .1s; }
.btn-primary:focus-visible { outline: 2px solid var(--fleet-black); outline-offset: 2px; }
.btn-primary svg { fill: currentColor; }
@media (prefers-reduced-motion: reduce) {
  .btn-primary { transition: none; }
  .btn-primary:hover, .btn-primary:active { transform: none; }
  .btn-primary::before, .btn-primary::after { transition: none; }
}

/* ---------- Kicker ---------- */
.kicker {
  font-family: var(--font-mono); font-weight: 400; font-size: 12px;
  line-height: 1.5; letter-spacing: .04em; text-transform: uppercase;
  color: var(--text-2);
}

/* ---------- Hero ---------- */
.hero { padding: 28px 0 24px; }
.hero-grid { display: grid; grid-template-columns: minmax(0,1fr) auto; gap: 32px; align-items: end; }
.big { display: flex; align-items: baseline; gap: 12px; flex-wrap: wrap; }
.big .n {
  font-size: 52px; font-weight: 800; letter-spacing: -.03em; line-height: 1;
  font-variant-numeric: tabular-nums; color: var(--text);
}
.big .l { font-size: 15px; color: var(--text-2); }
.big .d {
  font-size: 13px; font-weight: 700; color: var(--fleet-green);
  background: rgba(0,154,125,.1); padding: 3px 9px; border-radius: 4px;
  font-variant-numeric: tabular-nums;
}
.split { margin-top: 18px; width: 100%; max-width: 440px; }
.split-bar { display: flex; height: 8px; border-radius: 4px; overflow: hidden; background: var(--surface-2); }
.split-bar i { display: block; height: 100%; }
.split-keys { display: flex; justify-content: space-between; gap: 12px; margin-top: 10px; font-size: 13px; color: var(--text-2); }
.split-keys span { display: inline-flex; align-items: center; gap: 7px; }
.split-keys em { font-style: normal; font-weight: 700; color: var(--text); font-variant-numeric: tabular-nums; }
.split-keys i { width: 8px; height: 8px; border-radius: 2px; }

.minis-wrap { display: flex; flex-direction: column; gap: 9px; }
.minis { display: flex; gap: 10px; }
.mini {
  background: var(--surface); border: 1px solid var(--border);
  border-radius: var(--r); padding: 13px 15px 9px; min-width: 142px; overflow: hidden;
}
.mini .mv { font-size: 20px; font-weight: 800; letter-spacing: -.02em; font-variant-numeric: tabular-nums; line-height: 1.1; }
.mini .ml { margin-top: 4px; font-size: 11px; letter-spacing: .03em; }
.mini .ms { margin: 7px -15px -9px; }

/* ---------- Panels ---------- */
.card { background: var(--surface); border: 1px solid var(--border); border-radius: var(--r-lg); box-shadow: var(--shadow-sm); }
.duo { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
.panel { padding: 18px 20px 13px; }
.panel-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.panel h2 { margin: 0; font-size: 16px; font-weight: 800; letter-spacing: -.01em; }
.panel .note { margin: 4px 0 0; font-size: 13px; color: var(--text-2); }
.panel-foot { margin: 10px 0 0; font-size: 12px; text-align: right; }
.panel-foot a { color: var(--muted); }

/* ---------- Charts ---------- */
.chart { position: relative; width: 100%; }
.chart svg { display: block; width: 100%; height: auto; overflow: visible; }
.chart .c-grid  { stroke: var(--grid); stroke-width: 1; }
.chart .c-axis  { fill: var(--muted); font-size: 11px; font-variant-numeric: tabular-nums; }
.chart .c-line  { fill: none; stroke-width: 2; stroke-linejoin: round; stroke-linecap: round; }
.chart .c-hover { stroke: var(--border-strong); stroke-width: 1; stroke-dasharray: 3 3; }
.chart .c-hit   { fill: transparent; cursor: crosshair; }
.c-tip {
  position: absolute; z-index: 5; pointer-events: none; opacity: 0;
  transform: translate(-50%, -112%); transition: opacity .12s ease;
  background: var(--fleet-black); color: #fff; border-radius: 8px;
  padding: 7px 10px; font-size: 12px; line-height: 1.35; white-space: nowrap;
  box-shadow: 0 6px 18px -6px rgba(0,0,0,.45);
}
.c-tip.on { opacity: 1; }
.c-tip .t-date { color: #aab2e0; font-size: 11px; margin-bottom: 2px; }
.c-tip .t-row { display: flex; align-items: center; gap: 6px; }
.c-tip .t-dot { width: 7px; height: 7px; border-radius: 50%; flex: none; }
.c-tip b { font-variant-numeric: tabular-nums; font-weight: 700; }

.legend { display: flex; gap: 14px; align-items: center; flex-wrap: wrap; font-size: 12.5px; color: var(--text-2); }
.legend span { display: inline-flex; align-items: center; gap: 6px; }
.legend i { width: 9px; height: 9px; border-radius: 3px; display: block; }

/* ---------- Segmented control ---------- */
.seg { display: inline-flex; background: var(--surface-2); border: 1px solid var(--border); border-radius: 8px; padding: 3px; gap: 2px; }
.seg button {
  appearance: none; border: 0; background: transparent; color: var(--text-2);
  font: inherit; font-size: 12.5px; font-weight: 600; padding: 5px 13px;
  border-radius: 6px; cursor: pointer; white-space: nowrap;
  transition: background .15s, color .15s;
}
.seg button:hover { color: var(--text); }
.seg button[aria-pressed="true"] { background: var(--surface); color: var(--text); box-shadow: 0 1px 2px rgba(0,0,0,.06); }

/* ---------- Search (form-fields.less .text-input(normal)) ---------- */
.search { position: relative; display: flex; align-items: center; }
.search svg { position: absolute; left: 11px; color: var(--muted); pointer-events: none; }
.search input {
  appearance: none; background: var(--surface); border: 1px solid var(--border);
  border-radius: 8px; color: var(--text); font: inherit; font-size: 14px;
  line-height: 150%; height: 36px; padding: 6.5px 12px 6.5px 33px; width: 230px;
  transition: border-color .15s;
}
.search input::placeholder { color: var(--muted); }
.search input:focus { outline: none; border-color: var(--fleet-black); box-shadow: none; }

/* ---------- Apps ---------- */
.apps { margin-top: 32px; }
.apps-head { display: flex; align-items: center; justify-content: space-between; gap: 14px; flex-wrap: wrap; margin-bottom: 16px; }
.apps-head h2 { margin: 0; font-size: 20px; font-weight: 800; letter-spacing: -.012em; }
.apps-head .n { color: var(--muted); font-weight: 500; }
.apps-tools { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }

.apps-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(148px, 1fr)); gap: 10px; }
.app {
  background: var(--surface); border: 1px solid var(--border); border-radius: var(--r);
  padding: 14px 10px 12px; text-align: center; cursor: pointer;
  transition: border-color .14s, transform .14s, box-shadow .14s;
  content-visibility: auto; contain-intrinsic-size: 150px;
}
.app:hover { border-color: var(--brand); transform: translateY(-2px); box-shadow: var(--shadow); }
.app:focus-visible { outline: 2px solid var(--fleet-black); outline-offset: 2px; }
.app-ico {
  width: 40px; height: 40px; margin: 0 auto 9px; border-radius: 9px;
  display: grid; place-items: center; background: var(--surface-2);
  overflow: hidden; font-weight: 700; font-size: 15px; color: var(--muted);
}
.app-ico img { width: 100%; height: 100%; object-fit: contain; }
.app-name { font-size: 13px; font-weight: 600; color: var(--text); line-height: 1.3; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.app-ver { font-size: 11.5px; color: var(--muted); font-variant-numeric: tabular-nums; margin-top: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pill { display: inline-block; margin-top: 7px; font-size: 10.5px; font-weight: 600; letter-spacing: .02em; padding: 2px 8px; border-radius: 999px; }
.pill.mac { background: var(--fleet-blue-10); color: var(--brand-ink); }
.pill.win { background: #fff0f4; color: #d13a63; }
.more { grid-column: 1 / -1; text-align: center; color: var(--muted); font-size: 13px; padding: 18px 0; }

.foot { color: var(--muted); font-size: 13px; text-align: center; padding: 36px 0 44px; }
.foot a { color: var(--text-2); }

/* ---------- Modal ---------- */
.modal {
  display: none; position: fixed; inset: 0; z-index: 100;
  background: rgba(25,33,71,.45); padding: 24px;
  overflow-y: auto;
}
.modal.show { display: flex; align-items: flex-start; justify-content: center; }
.modal-content {
  background: var(--surface); border-radius: var(--r-lg); width: 100%; max-width: 640px;
  margin: auto; box-shadow: 0 20px 60px -12px rgba(25,33,71,.4);
  animation: modal-in .18s cubic-bezier(.2,.8,.2,1);
}
@keyframes modal-in { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
@media (prefers-reduced-motion: reduce) { .modal-content { animation: none; } }
.modal-header { display: flex; align-items: flex-start; gap: 14px; padding: 20px 20px 16px; border-bottom: 1px solid var(--border); }
.modal-icon { width: 48px; height: 48px; border-radius: 10px; background: var(--surface-2); overflow: hidden; flex: none; display: grid; place-items: center; font-weight: 700; color: var(--muted); }
.modal-icon img { width: 100%; height: 100%; object-fit: contain; }
.modal-title-section { flex: 1; min-width: 0; }
.modal-title { margin: 0 0 4px; font-size: 20px; font-weight: 800; letter-spacing: -.015em; }
.modal-platform { display: inline-block; font-size: 11px; font-weight: 600; padding: 2px 8px; border-radius: 999px; }
.modal-platform.darwin { background: var(--fleet-blue-10); color: var(--brand-ink); }
.modal-platform.windows { background: #fff0f4; color: #d13a63; }
.modal-close {
  appearance: none; border: 0; background: transparent; color: var(--muted);
  font-size: 26px; line-height: 1; cursor: pointer; padding: 2px 6px; border-radius: 6px;
}
.modal-close:hover { background: var(--surface-2); color: var(--text); }
.modal-body { padding: 18px 20px 20px; }
.modal-row { margin-bottom: 16px; }
.modal-row:last-child { margin-bottom: 0; }
.modal-label {
  font-family: var(--font-mono); font-size: 11px; letter-spacing: .04em;
  text-transform: uppercase; color: var(--text-2); margin-bottom: 5px;
}
.modal-value { font-size: 14px; color: var(--text); word-break: break-word; }
.modal-sec { display: flex; flex-direction: column; gap: 8px; }
.modal-sec-item { display: flex; flex-wrap: wrap; gap: 4px 8px; align-items: baseline; }
.modal-sec-label { font-size: 12px; font-weight: 600; color: var(--text-2); min-width: 92px; }
.modal-sec-value {
  font-family: var(--font-mono); font-size: 11.5px; color: var(--text);
  background: var(--surface-2); border: 1px solid var(--border); border-radius: 6px;
  padding: 3px 7px; cursor: pointer; word-break: break-all; flex: 1; min-width: 0;
  transition: border-color .15s, background .15s;
}
.modal-sec-value:hover { border-color: var(--brand); }
.modal-sec-value.copied { background: rgba(0,154,125,.1); border-color: var(--cta); color: var(--cta); }
.modal-sec-app { margin-bottom: 18px; }
.modal-sec-app:last-child { margin-bottom: 0; }
.modal-sec-app-title { font-weight: 700; font-size: 14px; margin-bottom: 8px; }
.modal-footer { padding: 14px 20px; border-top: 1px solid var(--border); font-size: 12px; color: var(--muted); display: flex; justify-content: space-between; gap: 12px; flex-wrap: wrap; }

@media (max-width: 920px) {
  .hero-grid { grid-template-columns: 1fr; align-items: start; }
  .duo { grid-template-columns: 1fr; }
  .minis { display: grid; grid-template-columns: 1fr 1fr; }
  .mini { min-width: 0; }
}
@media (max-width: 520px) {
  .big .n { font-size: 40px; }
  .modal { padding: 0; }
  .modal-content { border-radius: 0; min-height: 100%; }
}
</style>
</head>
<body>

<div class="topbar"><div class="wrap">
  <div class="brand">
    <svg width="22" height="22" viewBox="0 0 64 64" aria-hidden="true">
      <circle cx="7.5"  cy="7.5"  r="7.5" fill="#63C740"/>
      <circle cx="31.5" cy="7.5"  r="7.5" fill="#5CABDF"/>
      <circle cx="55.5" cy="7.5"  r="7.5" fill="#D66C7B"/>
      <circle cx="7.5"  cy="31.5" r="7.5" fill="#C98DEF"/>
      <circle cx="31.5" cy="31.5" r="7.5" fill="#FAA669"/>
      <circle cx="7.5"  cy="55.5" r="7.5" fill="#3AEFC4"/>
    </svg>
    <span class="sep"></span>
    <span class="what">Fleet-maintained app library</span>
  </div>
  <a class="btn-primary" href="feed.xml" aria-label="Subscribe to updates">
    <svg width="13" height="13" viewBox="0 0 24 24" aria-hidden="true"><path d="M6.503 20.752c0 1.794-1.456 3.248-3.251 3.248-1.796 0-3.252-1.454-3.252-3.248 0-1.794 1.456-3.248 3.252-3.248 1.795.001 3.251 1.454 3.251 3.248zm-6.503-12.572v4.811c6.05.062 10.96 4.966 11.022 11.009h4.817c-.062-8.71-7.118-15.758-15.839-15.82zm0-3.368c10.58.046 19.152 8.594 19.183 19.188h4.817c-.03-13.231-10.755-23.954-24-24v4.812z"/></svg>
    <span class="label">Subscribe to updates</span>
  </a>
</div></div>

<div class="wrap">
  <section class="hero">
    <h1 class="sr-only">Fleet-maintained app library</h1>
    <div class="hero-grid">
      <div>
        <div class="big">
          <span class="n" id="bTotal">&mdash;</span>
          <span class="l">apps in the library</span>
          <span class="d" id="bDelta"></span>
        </div>
        <div class="split">
          <div class="split-bar">
            <i id="barMac" style="background:#6a67fe;width:50%"></i>
            <i id="barWin" style="background:#ff5c83;width:50%"></i>
          </div>
          <div class="split-keys">
            <span><i style="background:#6a67fe"></i>macOS <em id="bMac">&mdash;</em></span>
            <span><i style="background:#ff5c83"></i>Windows <em id="bWin">&mdash;</em></span>
          </div>
        </div>
      </div>
      <div class="minis-wrap">
        <p class="kicker">Last 30 days</p>
        <div class="minis">
          <div class="mini">
            <div class="mv" id="mPatch">&mdash;</div>
            <div class="ml kicker">Patches</div>
            <div class="ms" id="msPatch"></div>
          </div>
          <div class="mini">
            <div class="mv" id="mAdds">&mdash;</div>
            <div class="ml kicker">New apps</div>
            <div class="ms" id="msAdds"></div>
          </div>
        </div>
      </div>
    </div>
  </section>

  <section class="duo">
    <div class="card panel">
      <div class="panel-head">
        <div>
          <h2>Library growth</h2>
          <p class="note">Cumulative apps maintained</p>
        </div>
        <div class="seg" id="rangeSeg">
          <button type="button" data-r="180" aria-pressed="false">6m</button>
          <button type="button" data-r="365" aria-pressed="false">1y</button>
          <button type="button" data-r="all" aria-pressed="true">All</button>
        </div>
      </div>
      <div id="growth" style="height:200px"></div>
      <p class="panel-foot"><a href="data/apps_growth.csv">View the data (CSV)</a></p>
    </div>

    <div class="card panel">
      <div class="panel-head">
        <div>
          <h2>Patches released per day</h2>
          <p class="note">New app additions are not counted</p>
        </div>
        <div class="legend">
          <span><i style="background:#6a67fe"></i>macOS</span>
          <span><i style="background:#ff5c83"></i>Windows</span>
        </div>
      </div>
      <div id="patches" style="height:200px"></div>
      <p class="panel-foot"><a href="data/patches_per_day.csv">View the data (CSV)</a></p>
    </div>
  </section>

  <section class="apps">
    <div class="apps-head">
      <h2>Apps <span class="n" id="appsCount">` + appCount + `</span></h2>
      <div class="apps-tools">
        <div class="seg" id="platSeg">
          <button type="button" data-f="all" aria-pressed="true">All</button>
          <button type="button" data-f="mac" aria-pressed="false">macOS</button>
          <button type="button" data-f="win" aria-pressed="false">Windows</button>
        </div>
        <label class="search">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"><circle cx="11" cy="11" r="7"/><path d="M21 21l-4.3-4.3"/></svg>
          <input id="appSearch" type="search" placeholder="Search apps..." aria-label="Search apps">
        </label>
      </div>
    </div>
    <div class="apps-grid" id="appsGrid"></div>
  </section>

  <footer class="foot">
    <p>Data source: <a href="https://github.com/fleetdm/fleet" target="_blank" rel="noopener">fleetdm/fleet</a> &middot; Last updated: ` + lastUpdated + `</p>
  </footer>
</div>

<div id="appModal" class="modal" role="dialog" aria-modal="true" aria-labelledby="modalTitle">
  <div class="modal-content">
    <div class="modal-header">
      <div class="modal-icon" id="modalIcon"></div>
      <div class="modal-title-section">
        <h2 class="modal-title" id="modalTitle"></h2>
        <span class="modal-platform" id="modalPlatform"></span>
      </div>
      <button class="modal-close" type="button" aria-label="Close">&times;</button>
    </div>
    <div class="modal-body" id="modalBody"></div>
    <div class="modal-footer">
      <span id="modalLastUpdated"></span>
      <a id="modalInstaller" href="#" target="_blank" rel="noopener" style="display:none">Download installer</a>
    </div>
  </div>
</div>
<script>
(function () {
'use strict';

var NS    = 'http://www.w3.org/2000/svg';
var COL   = { mac: '#6a67fe', win: '#ff5c83' };
var MONTH = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
var ICON_BASE = 'https://raw.githubusercontent.com/fleetdm/fleet/main/website/assets/images/';

var GROWTH = ` + string(growthJSON) + `;
var PATCH  = ` + string(patchJSON) + `;

/* ---------- helpers ---------- */

function fmt(n) { return Number(n).toLocaleString('en-US'); }

function dateList(p, n) {
  if (p.dates) { return p.dates; }
  var out = [], d = new Date(p.start + 'T00:00:00Z');
  for (var i = 0; i < n; i++) {
    out.push(d.toISOString().slice(0, 10));
    d.setUTCDate(d.getUTCDate() + 1);
  }
  return out;
}

function prettyDate(iso) {
  var p = iso.split('-');
  return MONTH[+p[1] - 1] + ' ' + (+p[2]) + ', ' + p[0];
}

function niceScale(max, ticks) {
  if (max <= 0) { return { max: 1, step: 1 }; }
  var raw = max / ticks;
  var mag = Math.pow(10, Math.floor(Math.log10(raw)));
  var norm = raw / mag;
  var step = (norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10) * mag;
  return { max: Math.ceil(max / step) * step, step: step };
}

function monthTicks(dates, maxLabels) {
  var idx = [], i;
  for (i = 1; i < dates.length; i++) {
    if (dates[i].slice(5, 7) !== dates[i - 1].slice(5, 7)) { idx.push(i); }
  }
  var every = Math.ceil(idx.length / maxLabels) || 1;
  return idx.filter(function (_, k) { return k % every === 0; });
}

function el(name, attrs) {
  var n = document.createElementNS(NS, name);
  for (var k in attrs) { n.setAttribute(k, attrs[k]); }
  return n;
}

function escapeHtml(text) {
  var d = document.createElement('div');
  d.textContent = text == null ? '' : text;
  return d.innerHTML;
}

function iconUrl(slug) {
  return ICON_BASE + 'app-icon-' + slug.split('/')[0] + '-60x60@2x.png';
}

function initials(name) {
  var w = String(name || '').split(' ').filter(Boolean);
  return (w.length > 1 ? w[0][0] + w[1][0] : String(name || '?').slice(0, 2)).toUpperCase();
}

function platformLabel(p) { return p === 'darwin' ? 'macOS' : 'Windows'; }

/* ---------- responsive chart plumbing ----------
   Charts render at true pixel width (viewBox == CSS px) so axis text is never
   stretched, and redraw when their box changes size. */

var charts = new Map(), ro = null, raf = null;

function chartWidth(host) {
  return Math.max(Math.round(host.getBoundingClientRect().width) || 900, 300);
}

function register(host, fn, cfg) {
  charts.set(host, { fn: fn, cfg: cfg });
  if (!ro) {
    ro = new ResizeObserver(function (entries) {
      if (raf) { cancelAnimationFrame(raf); }
      raf = requestAnimationFrame(function () {
        entries.forEach(function (e) {
          var rec = charts.get(e.target);
          if (rec && Math.abs(chartWidth(e.target) - (e.target._w || 0)) > 1) {
            e.target._w = chartWidth(e.target);
            rec.fn(e.target, rec.cfg);
          }
        });
      });
    });
  }
  if (!host._observed) { host._observed = true; ro.observe(host); }
  host._w = chartWidth(host);
}

function addTip(host) {
  var t = document.createElement('div');
  t.className = 'c-tip';
  host.appendChild(t);
  return t;
}

function tipRows(series, i) {
  return series.map(function (s) {
    return '<div class="t-row"><i class="t-dot" style="background:' + s.color + '"></i>' +
           s.label + ' <b>' + fmt(s.values[i]) + '</b></div>';
  }).join('');
}

/* ---------- area chart ---------- */

function areaChart(host, cfg) {
  register(host, areaChart, cfg);
  var dates = cfg.dates, series = cfg.series, H = cfg.height || 200;
  host.innerHTML = '';
  host.classList.add('chart');

  var W = chartWidth(host);
  var padL = 46, padR = 10, padT = 12, padB = 24;
  var iw = W - padL - padR, ih = H - padT - padB;

  var peak = 0;
  series.forEach(function (s) { s.values.forEach(function (v) { if (v > peak) { peak = v; } }); });
  var sc = niceScale(peak, 4);

  function x(i) { return padL + (dates.length < 2 ? iw / 2 : (i / (dates.length - 1)) * iw); }
  function y(v) { return padT + ih - (v / sc.max) * ih; }

  var svg = el('svg', { viewBox: '0 0 ' + W + ' ' + H, width: W, height: H, role: 'img' });
  svg.style.height = H + 'px';

  var defs = document.createElementNS(NS, 'defs');
  series.forEach(function (s, i) {
    var g = el('linearGradient', { id: 'g' + i + '-' + host.id, x1: '0', y1: '0', x2: '0', y2: '1' });
    g.innerHTML = '<stop offset="0%" stop-color="' + s.color + '" stop-opacity=".26"/>' +
                  '<stop offset="100%" stop-color="' + s.color + '" stop-opacity="0"/>';
    defs.appendChild(g);
  });
  svg.appendChild(defs);

  for (var v = 0; v <= sc.max + 1e-9; v += sc.step) {
    var gy = y(v);
    svg.appendChild(el('line', { 'class': 'c-grid', x1: padL, x2: W - padR, y1: gy, y2: gy, 'vector-effect': 'non-scaling-stroke' }));
    var t = el('text', { 'class': 'c-axis', x: padL - 9, y: gy + 3.5, 'text-anchor': 'end' });
    t.textContent = v >= 1000 ? (v / 1000) + 'k' : v;
    svg.appendChild(t);
  }

  monthTicks(dates, 8).forEach(function (i) {
    var p = dates[i].split('-');
    var t = el('text', { 'class': 'c-axis', x: x(i), y: H - 6, 'text-anchor': 'middle' });
    t.textContent = MONTH[+p[1] - 1] + (+p[1] === 1 ? " '" + p[0].slice(2) : '');
    svg.appendChild(t);
  });

  series.forEach(function (s, i) {
    var pts = s.values.map(function (v, j) { return x(j) + ',' + y(v); }).join(' L ');
    svg.appendChild(el('path', {
      d: 'M ' + x(0) + ',' + y(0) + ' L ' + pts + ' L ' + x(dates.length - 1) + ',' + y(0) + ' Z',
      fill: 'url(#g' + i + '-' + host.id + ')'
    }));
    svg.appendChild(el('path', { 'class': 'c-line', d: 'M ' + pts, stroke: s.color, 'vector-effect': 'non-scaling-stroke' }));
  });

  var vline = el('line', { 'class': 'c-hover', y1: padT, y2: padT + ih, 'vector-effect': 'non-scaling-stroke' });
  vline.style.opacity = 0;
  svg.appendChild(vline);

  var dots = series.map(function (s) {
    var c = el('circle', { r: 4, fill: s.color, stroke: 'var(--surface)', 'stroke-width': 2 });
    c.style.opacity = 0;
    svg.appendChild(c);
    return c;
  });

  var hit = el('rect', { 'class': 'c-hit', x: padL, y: padT, width: iw, height: ih });
  svg.appendChild(hit);
  host.appendChild(svg);
  var tip = addTip(host);

  hit.addEventListener('pointermove', function (ev) {
    var r = svg.getBoundingClientRect();
    var px = (ev.clientX - r.left) / r.width * W;
    var i = Math.round((px - padL) / iw * (dates.length - 1));
    i = Math.max(0, Math.min(dates.length - 1, i));

    vline.setAttribute('x1', x(i)); vline.setAttribute('x2', x(i));
    vline.style.opacity = 1;
    dots.forEach(function (d, k) {
      d.setAttribute('cx', x(i)); d.setAttribute('cy', y(series[k].values[i]));
      d.style.opacity = 1;
    });

    var top = Math.max.apply(null, series.map(function (s) { return s.values[i]; }));
    tip.innerHTML = '<div class="t-date">' + prettyDate(dates[i]) + '</div>' + tipRows(series, i);
    tip.style.left = (x(i) / W * 100) + '%';
    tip.style.top = (y(top) / H * 100) + '%';
    tip.classList.add('on');
  });

  hit.addEventListener('pointerleave', function () {
    vline.style.opacity = 0;
    dots.forEach(function (d) { d.style.opacity = 0; });
    tip.classList.remove('on');
  });
}

/* ---------- stacked bar chart ---------- */

function barChart(host, cfg) {
  register(host, barChart, cfg);
  var dates = cfg.dates, series = cfg.series, H = cfg.height || 200;
  host.innerHTML = '';
  host.classList.add('chart');

  var W = chartWidth(host);
  var padL = 46, padR = 10, padT = 12, padB = 24;
  var iw = W - padL - padR, ih = H - padT - padB;

  var totals = dates.map(function (_, i) {
    return series.reduce(function (a, s) { return a + s.values[i]; }, 0);
  });
  var sc = niceScale(Math.max.apply(null, totals), 4);

  var slot = iw / dates.length;
  var bw = Math.max(slot * 0.68, 0.9);
  function x(i) { return padL + i * slot + (slot - bw) / 2; }
  function y(v) { return padT + ih - (v / sc.max) * ih; }

  var svg = el('svg', { viewBox: '0 0 ' + W + ' ' + H, width: W, height: H, role: 'img' });
  svg.style.height = H + 'px';

  for (var v = 0; v <= sc.max + 1e-9; v += sc.step) {
    var gy = y(v);
    svg.appendChild(el('line', { 'class': 'c-grid', x1: padL, x2: W - padR, y1: gy, y2: gy, 'vector-effect': 'non-scaling-stroke' }));
    var t = el('text', { 'class': 'c-axis', x: padL - 9, y: gy + 3.5, 'text-anchor': 'end' });
    t.textContent = v;
    svg.appendChild(t);
  }

  monthTicks(dates, 8).forEach(function (i) {
    var p = dates[i].split('-');
    var t = el('text', { 'class': 'c-axis', x: x(i) + bw / 2, y: H - 6, 'text-anchor': 'middle' });
    t.textContent = MONTH[+p[1] - 1] + (+p[1] === 1 ? " '" + p[0].slice(2) : '');
    svg.appendChild(t);
  });

  var hl = el('rect', { fill: 'var(--text)', opacity: '.07', y: padT, height: ih });
  hl.style.opacity = 0;
  svg.appendChild(hl);

  var g = document.createElementNS(NS, 'g');
  dates.forEach(function (_, i) {
    var acc = 0;
    series.forEach(function (s) {
      var val = s.values[i];
      if (!val) { return; }
      g.appendChild(el('rect', {
        x: x(i), width: bw, y: y(acc + val),
        height: Math.max(y(acc) - y(acc + val), 0.6), fill: s.color
      }));
      acc += val;
    });
  });
  svg.appendChild(g);

  var hit = el('rect', { 'class': 'c-hit', x: padL, y: padT, width: iw, height: ih });
  svg.appendChild(hit);
  host.appendChild(svg);
  var tip = addTip(host);

  hit.addEventListener('pointermove', function (ev) {
    var r = svg.getBoundingClientRect();
    var px = (ev.clientX - r.left) / r.width * W;
    var i = Math.floor((px - padL) / slot);
    i = Math.max(0, Math.min(dates.length - 1, i));

    hl.setAttribute('x', padL + i * slot);
    hl.setAttribute('width', slot);
    hl.style.opacity = 1;

    tip.innerHTML = '<div class="t-date">' + prettyDate(dates[i]) + '</div>' + tipRows(series, i) +
      '<div class="t-row" style="margin-top:3px;opacity:.8">Total <b>' + fmt(totals[i]) + '</b></div>';
    tip.style.left = ((padL + i * slot + slot / 2) / W * 100) + '%';
    tip.style.top = (y(totals[i]) / H * 100) + '%';
    tip.classList.add('on');
  });

  hit.addEventListener('pointerleave', function () {
    hl.style.opacity = 0;
    tip.classList.remove('on');
  });
}

/* ---------- sparkline ---------- */

function sparkline(host, values, color) {
  var W = 200, H = 40;
  var min = Math.min.apply(null, values), max = Math.max.apply(null, values);
  var span = (max - min) || 1;
  var pts = values.map(function (v, i) {
    return (i / (values.length - 1)) * W + ',' + (H - 3 - ((v - min) / span) * (H - 6));
  }).join(' L ');
  host.innerHTML =
    '<svg viewBox="0 0 ' + W + ' ' + H + '" preserveAspectRatio="none" style="width:100%;height:' + H + 'px">' +
      '<defs><linearGradient id="sp-' + host.id + '" x1="0" y1="0" x2="0" y2="1">' +
        '<stop offset="0%" stop-color="' + color + '" stop-opacity=".22"/>' +
        '<stop offset="100%" stop-color="' + color + '" stop-opacity="0"/>' +
      '</linearGradient></defs>' +
      '<path d="M 0,' + H + ' L ' + pts + ' L ' + W + ',' + H + '" fill="url(#sp-' + host.id + ')"/>' +
      '<path d="M ' + pts + '" fill="none" stroke="' + color + '" stroke-width="2" ' +
        'stroke-linejoin="round" stroke-linecap="round" vector-effect="non-scaling-stroke"/>' +
    '</svg>';
}

/* ============================================================
   Hero stats + charts (inlined data, no fetch)
   ============================================================ */

var gDates = dateList(GROWTH, GROWTH.mac.length);
var pDates = dateList(PATCH, PATCH.mac.length);
var lastIdx = gDates.length - 1;
var prevIdx = Math.max(0, gDates.length - 31);

function setText(id, v) {
  var n = document.getElementById(id);
  if (n) { n.textContent = v; }
}

if (lastIdx >= 0) {
  setText('bTotal', fmt(GROWTH.total[lastIdx]));
  setText('bMac', fmt(GROWTH.mac[lastIdx]));
  setText('bWin', fmt(GROWTH.win[lastIdx]));

  var delta = GROWTH.total[lastIdx] - GROWTH.total[prevIdx];
  setText('bDelta', (delta >= 0 ? '+' : '') + fmt(delta) + ' in 30 days');

  var pct = GROWTH.mac[lastIdx] / GROWTH.total[lastIdx] * 100;
  document.getElementById('barMac').style.width = pct + '%';
  document.getElementById('barWin').style.width = (100 - pct) + '%';
}

var p30mac = PATCH.mac.slice(-30), p30win = PATCH.win.slice(-30);
var p30 = p30mac.map(function (v, i) { return v + p30win[i]; });
setText('mPatch', fmt(p30.reduce(function (a, b) { return a + b; }, 0)));
sparkline(document.getElementById('msPatch'), p30, COL.win);

var adds30 = (GROWTH.added || []).slice(-30).map(function (v) { return Math.max(v, 0); });
setText('mAdds', fmt(adds30.reduce(function (a, b) { return a + b; }, 0)));
sparkline(document.getElementById('msAdds'), adds30, COL.mac);

function drawGrowth(range) {
  var n = gDates.length;
  var from = range === 'all' ? 0 : Math.max(0, n - range);
  areaChart(document.getElementById('growth'), {
    height: 200,
    dates: gDates.slice(from),
    series: [
      { label: 'macOS',   color: COL.mac, values: GROWTH.mac.slice(from) },
      { label: 'Windows', color: COL.win, values: GROWTH.win.slice(from) }
    ]
  });
}

drawGrowth('all');

document.getElementById('rangeSeg').addEventListener('click', function (e) {
  var b = e.target.closest('button');
  if (!b) { return; }
  this.querySelectorAll('button').forEach(function (x) {
    x.setAttribute('aria-pressed', String(x === b));
  });
  drawGrowth(b.dataset.r === 'all' ? 'all' : +b.dataset.r);
});

barChart(document.getElementById('patches'), {
  height: 200,
  dates: pDates,
  series: [
    { label: 'macOS',   color: COL.mac, values: PATCH.mac },
    { label: 'Windows', color: COL.win, values: PATCH.win }
  ]
});

/* ============================================================
   App grid — index fetched after first paint, 60 cards at a time
   ============================================================ */

var PAGE = 60;
var allApps = [], view = [], shown = 0, filter = 'all', query = '';
var gridEl   = document.getElementById('appsGrid');
var countEl  = document.getElementById('appsCount');
var detail   = null, detailPromise = null;

function card(a) {
  var mac = a.p === 'darwin';
  return '<div class="app" role="button" tabindex="0" data-slug="' + escapeHtml(a.s) + '">' +
    '<div class="app-ico" data-i="' + escapeHtml(initials(a.n)) + '">' +
      '<img loading="lazy" decoding="async" alt="" src="' + escapeHtml(iconUrl(a.s)) + '" ' +
      'onerror="var p=this.parentNode;this.remove();p.textContent=p.dataset.i">' +
    '</div>' +
    '<div class="app-name" title="' + escapeHtml(a.n) + '">' + escapeHtml(a.n) + '</div>' +
    '<div class="app-ver">' + escapeHtml(a.v || '—') + '</div>' +
    '<span class="pill ' + (mac ? 'mac' : 'win') + '">' + (mac ? 'macOS' : 'Windows') + '</span>' +
  '</div>';
}

var io = new IntersectionObserver(function (es) {
  if (es.some(function (e) { return e.isIntersecting; })) { page(); }
}, { rootMargin: '400px' });

function page() {
  var next = view.slice(shown, shown + PAGE);
  shown += next.length;
  var sentinel = gridEl.querySelector('.more');
  if (sentinel) { sentinel.remove(); }
  gridEl.insertAdjacentHTML('beforeend', next.map(card).join(''));
  if (shown < view.length) {
    gridEl.insertAdjacentHTML('beforeend',
      '<div class="more">Showing ' + fmt(shown) + ' of ' + fmt(view.length) + '</div>');
    io.observe(gridEl.querySelector('.more'));
  }
}

function applyFilter() {
  var needle = query.trim().toLowerCase();
  view = allApps.filter(function (a) {
    var platOk = filter === 'all' || (filter === 'mac') === (a.p === 'darwin');
    return platOk && (!needle || a.n.toLowerCase().indexOf(needle) !== -1);
  });
  shown = 0;
  gridEl.innerHTML = '';
  if (countEl) { countEl.textContent = fmt(view.length); }
  if (!view.length) {
    gridEl.innerHTML = '<div class="more">No apps match that search.</div>';
    return;
  }
  page();
}

document.getElementById('appSearch').addEventListener('input', function (e) {
  query = e.target.value;
  applyFilter();
});

document.getElementById('platSeg').addEventListener('click', function (e) {
  var b = e.target.closest('button');
  if (!b) { return; }
  this.querySelectorAll('button').forEach(function (x) {
    x.setAttribute('aria-pressed', String(x === b));
  });
  filter = b.dataset.f;
  applyFilter();
});

fetch('data/apps_index.json')
  .then(function (r) { return r.json(); })
  .then(function (d) { allApps = d.apps || []; applyFilter(); })
  .catch(function () {
    gridEl.innerHTML = '<div class="more">Could not load the app list.</div>';
  });

/* ---------- modal ---------- */

var modal      = document.getElementById('appModal');
var modalBody  = document.getElementById('modalBody');
var modalInst  = document.getElementById('modalInstaller');

function loadDetail() {
  if (detail) { return Promise.resolve(detail); }
  if (!detailPromise) {
    detailPromise = fetch('data/apps_detail.json')
      .then(function (r) { return r.json(); })
      .then(function (d) { detail = d.apps || {}; return detail; })
      .catch(function () { detail = {}; return detail; });
  }
  return detailPromise;
}

function secFields(src, isWindows) {
  return isWindows ? [
    ['SHA-256', src.sha256], ['Publisher', src.publisher], ['Issuer', src.issuer],
    ['Serial Number', src.serialNumber], ['Thumbprint', src.thumbprint], ['Timestamp', src.timestamp]
  ] : [
    ['SHA-256', src.sha256], ['CDHash', src.cdhash],
    ['Signing ID', src.signingId], ['Team ID', src.teamId]
  ];
}

function secBlock(src, isWindows) {
  var rows = secFields(src, isWindows).filter(function (f) {
    return f[1] !== undefined && f[1] !== null && f[1] !== '';
  });
  if (!rows.length) { return ''; }
  return '<div class="modal-sec">' + rows.map(function (f) {
    return '<div class="modal-sec-item">' +
      '<span class="modal-sec-label">' + f[0] + '</span>' +
      '<code class="modal-sec-value" tabindex="0" title="Click to copy">' + escapeHtml(f[1]) + '</code>' +
    '</div>';
  }).join('') + '</div>';
}

function renderModal(app, det) {
  var isWindows = app.p === 'windows';
  var sec = det && det.sec;
  var html = '';

  html += '<div class="modal-row"><div class="modal-label">Version</div>' +
          '<div class="modal-value">' + escapeHtml(app.v || 'N/A') + '</div></div>';

  html += '<div class="modal-row"><div class="modal-label">Description</div>' +
          '<div class="modal-value">' + escapeHtml((det && det.d) || 'No description available.') + '</div></div>';

  html += '<div class="modal-row"><div class="modal-label">Slug</div>' +
          '<div class="modal-value"><code>' + escapeHtml(app.s) + '</code></div></div>';

  if (sec) {
    var body = '';
    if (sec.apps && sec.apps.length) {
      body = sec.apps.map(function (sub, i) {
        var block = secBlock(sub, isWindows);
        if (!block) { return ''; }
        return '<div class="modal-sec-app">' +
          '<div class="modal-sec-app-title">' + escapeHtml(sub.name || ('App ' + (i + 1))) + '</div>' +
          block + '</div>';
      }).join('');
    } else {
      body = secBlock(sec, isWindows);
    }
    if (body) {
      html += '<div class="modal-row"><div class="modal-label">Security information</div>' + body + '</div>';
    }
  }

  modalBody.innerHTML = html;

  modalBody.querySelectorAll('.modal-sec-value').forEach(function (node) {
    node.addEventListener('click', function () { copyText(node); });
    node.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); copyText(node); }
    });
  });

  if (det && det.u) {
    modalInst.href = det.u;
    modalInst.style.display = '';
  } else {
    modalInst.style.display = 'none';
  }

  var stamp = 'Last updated: ` + lastUpdated + `';
  if (sec && sec.lastUpdated) {
    var when = new Date(sec.lastUpdated);
    if (!isNaN(when)) {
      stamp = 'Last updated: ' + new Intl.DateTimeFormat('en-US', {
        timeZone: 'America/Chicago', year: 'numeric', month: 'long', day: 'numeric',
        hour: 'numeric', minute: '2-digit', hour12: true
      }).format(when) + ' CST';
    }
  }
  setText('modalLastUpdated', stamp);
}

function copyText(node) {
  var text = node.textContent;

  function flash() {
    node.classList.add('copied');
    node.textContent = 'Copied!';
    setTimeout(function () {
      node.classList.remove('copied');
      node.textContent = text;
    }, 1600);
  }

  // Last resort: if nothing can write to the clipboard, select the value
  // so the reader can copy it by hand rather than getting no response.
  function selectValue() {
    try {
      var range = document.createRange();
      range.selectNodeContents(node);
      var sel = window.getSelection();
      sel.removeAllRanges();
      sel.addRange(range);
    } catch (e) {}
  }

  function legacyCopy() {
    var ok = false;
    var ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(ta);
    if (ok) { flash(); } else { selectValue(); }
  }

  // The async API can reject (permission denied, no user activation), so the
  // legacy path is the catch handler, not just the no-API branch.
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(flash).catch(legacyCopy);
  } else {
    legacyCopy();
  }
}

var lastFocus = null;

function openModal(slug) {
  var app = allApps.find(function (a) { return a.s === slug; });
  if (!app) { return; }

  lastFocus = document.activeElement;

  var ico = document.getElementById('modalIcon');
  ico.dataset.i = initials(app.n);
  ico.innerHTML = '<img alt="" src="' + escapeHtml(iconUrl(app.s)) + '" ' +
    'onerror="var p=this.parentNode;this.remove();p.textContent=p.dataset.i">';

  setText('modalTitle', app.n);
  var plat = document.getElementById('modalPlatform');
  plat.textContent = platformLabel(app.p);
  plat.className = 'modal-platform ' + app.p;

  modalBody.innerHTML = '<div class="more">Loading details...</div>';
  modal.classList.add('show');
  document.body.style.overflow = 'hidden';
  document.querySelector('.modal-close').focus();

  loadDetail().then(function (d) {
    if (!modal.classList.contains('show')) { return; }
    renderModal(app, d[slug]);
  });
}

function closeModal() {
  modal.classList.remove('show');
  document.body.style.overflow = '';
  if (lastFocus && lastFocus.focus) { lastFocus.focus(); }
}

gridEl.addEventListener('click', function (e) {
  var c = e.target.closest('.app');
  if (c) { openModal(c.dataset.slug); }
});

gridEl.addEventListener('keydown', function (e) {
  if (e.key !== 'Enter' && e.key !== ' ') { return; }
  var c = e.target.closest('.app');
  if (c) { e.preventDefault(); openModal(c.dataset.slug); }
});

document.querySelector('.modal-close').addEventListener('click', closeModal);

modal.addEventListener('click', function (e) {
  if (e.target === modal) { closeModal(); }
});

document.addEventListener('keydown', function (e) {
  if (e.key === 'Escape' && modal.classList.contains('show')) { closeModal(); }
});

/* ---------- primary-button spotlight (buttons.less + its utility) ---------- */

if (window.matchMedia && window.matchMedia('(hover: hover)').matches) {
  var pending = null;
  document.addEventListener('pointermove', function (event) {
    if (event.pointerType !== 'mouse') { return; }
    if (!pending) {
      requestAnimationFrame(function () {
        var latest = pending;
        pending = null;
        var button = null, node = latest.target;
        for (var depth = 0; node && node.nodeType === 1 && depth < 5; depth++, node = node.parentElement) {
          if (getComputedStyle(node).getPropertyValue('--primary-button').trim() === '1') {
            button = node;
          } else if (button) {
            break;
          }
        }
        if (button) {
          var b = button.getBoundingClientRect();
          button.style.setProperty('--spotlight-x', (latest.clientX - b.left) + 'px');
          button.style.setProperty('--spotlight-y', (latest.clientY - b.top) + 'px');
        }
      });
    }
    pending = event;
  }, { passive: true });
}

})();
</script>
</body>
</html>`
}
