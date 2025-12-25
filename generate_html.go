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
	outputHTML       = "index.html"
	appsJSONURL      = "https://raw.githubusercontent.com/fleetdm/fleet/main/ee/maintained-apps/outputs/apps.json"
	appBaseURL       = "https://raw.githubusercontent.com/fleetdm/fleet/main/ee/maintained-apps/outputs"
	iconsBaseURL     = "https://raw.githubusercontent.com/fleetdm/fleet/main/website/assets/images"
	securityInfoJSON = "data/app_security_info.json"
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
	Publisher    string                `json:"publisher,omitempty"`     // Windows: Certificate subject
	Issuer       string                `json:"issuer,omitempty"`        // Windows: Certificate authority
	SerialNumber string                `json:"serialNumber,omitempty"`  // Windows: Certificate serial
	Thumbprint   string                `json:"thumbprint,omitempty"`    // Windows: Certificate thumbprint
	Timestamp    string                `json:"timestamp,omitempty"`     // Windows: Signing timestamp
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

func generateHTML() error {
	fmt.Println("🎨 Generating HTML visualization...")

	data, err := loadCSVData()
	if err != nil {
		return fmt.Errorf("failed to load CSV data: %w", err)
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

	htmlContent := generateHTMLContent(data, apps)

	if err := os.WriteFile(outputHTML, []byte(htmlContent), 0644); err != nil {
		return fmt.Errorf("failed to write HTML file: %w", err)
	}

	fmt.Printf("✅ Generated %s\n", outputHTML)
	fmt.Printf("   Total days: %d\n", len(data.Dates))
	fmt.Printf("   Growth events: %d\n", len(data.GrowthDates))

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
	// Create a map of security info by slug
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

func generateHTMLContent(data *csvData, apps *appsJSON) string {
	dataJSON, _ := json.MarshalIndent(data, "        ", "  ")
	dataJSONStr := string(dataJSON)

	appsJSONBytes, _ := json.MarshalIndent(apps.Apps, "            ", "  ")
	appsJSONStr := string(appsJSONBytes)

	// Generate timestamp for when this HTML was created (in CST)
	cstLocation, err := time.LoadLocation("America/Chicago")
	if err != nil {
		// Fallback to UTC if CST location can't be loaded
		cstLocation = time.UTC
	}
	lastUpdated := time.Now().In(cstLocation).Format("January 2, 2006 at 3:04 PM MST")

	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <meta name="description" content="Track the growth of Fleet-maintained apps over time. View app versions, download installers, and explore the expanding library of macOS and Windows applications.">
    
    <!-- Open Graph / Facebook / LinkedIn -->
    <meta property="og:type" content="website">
    <meta property="og:url" content="https://fmalibrary.com/">
    <meta property="og:title" content="Fleet Maintained Apps Library">
    <meta property="og:description" content="Track the growth of Fleet-maintained apps over time. View app versions, download installers, and explore the expanding library of macOS and Windows applications.">
    <meta property="og:image" content="https://fmalibrary.com/cloud-city.png">
    <meta property="og:image:secure_url" content="https://fmalibrary.com/cloud-city.png">
    <meta property="og:image:type" content="image/png">
    <meta property="og:image:width" content="1920">
    <meta property="og:image:height" content="1080">
    <meta property="og:image:alt" content="Fleet Maintained Apps Library - Growth tracking dashboard">
    <meta property="og:site_name" content="Fleet Maintained Apps Library">
    <meta property="og:locale" content="en_US">
    
    <!-- Twitter -->
    <meta name="twitter:card" content="summary_large_image">
    <meta name="twitter:url" content="https://fmalibrary.com/">
    <meta name="twitter:title" content="Fleet Maintained Apps Library">
    <meta name="twitter:description" content="Track the growth of Fleet-maintained apps over time. View app versions, download installers, and explore the expanding library of macOS and Windows applications.">
    <meta name="twitter:image" content="https://fmalibrary.com/cloud-city.png">
    <meta name="twitter:image:alt" content="Fleet Maintained Apps Library - Growth tracking dashboard">
    
    <!-- RSS Feed -->
    <link rel="alternate" type="application/rss+xml" title="Fleet Maintained Apps - Version Updates" href="https://fmalibrary.com/feed.xml">
    
    <!-- Favicon (Swan Emoji) -->
    <link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'%3E%3Ctext y='0.9em' font-size='90'%3E🦢%3C/text%3E%3C/svg%3E">
    <link rel="apple-touch-icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'%3E%3Ctext y='0.9em' font-size='90'%3E🦢%3C/text%3E%3C/svg%3E">
    
    <title>Fleet Maintained Apps Growth</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/chartjs-adapter-date-fns@3.0.0/dist/chartjs-adapter-date-fns.bundle.min.js"></script>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
            margin: 0;
            padding: 20px;
            background: #f5f5f5;
        }
        .container {
            max-width: 1400px;
            margin: 0 auto;
            background: white;
            padding: 30px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            position: relative;
        }
        .header-section {
            display: flex;
            justify-content: space-between;
            align-items: flex-start;
            margin-bottom: 30px;
        }
        .header-content {
            flex: 1;
        }
        h1 {
            color: #1e293b;
            margin-bottom: 10px;
            margin-top: 0;
        }
        .subtitle {
            color: #64748b;
            margin-bottom: 0;
        }
        .chart-container {
            position: relative;
            height: 450px;
            margin-bottom: 40px;
        }
        .stats {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 20px;
            margin-top: 30px;
            padding-top: 30px;
            border-top: 2px solid #e2e8f0;
        }
        .stat-card {
            background: #f8fafc;
            padding: 20px;
            border-radius: 6px;
            border-left: 4px solid #2563eb;
            cursor: pointer;
            transition: all 0.2s ease;
        }
        .stat-card:hover {
            background: #f1f5f9;
            transform: translateY(-2px);
            box-shadow: 0 4px 6px rgba(0,0,0,0.1);
        }
        .stat-card.active {
            background: #eff6ff;
            border-left-color: #1d4ed8;
            box-shadow: 0 2px 4px rgba(37, 99, 235, 0.2);
        }
        .stat-card.clickable {
            cursor: pointer;
        }
        .stat-card:not(.clickable) {
            cursor: default;
        }
        .stat-value {
            font-size: 32px;
            font-weight: bold;
            color: #1e293b;
            margin-bottom: 5px;
        }
        .stat-label {
            color: #64748b;
            font-size: 14px;
        }
        .footer {
            margin-top: 40px;
            padding-top: 20px;
            border-top: 2px solid #e2e8f0;
            text-align: center;
            color: #64748b;
            font-size: 14px;
        }
        .apps-section {
            margin-top: 50px;
            padding-top: 40px;
            border-top: 2px solid #e2e8f0;
        }
        .apps-header {
            margin-bottom: 30px;
        }
        .apps-header h2 {
            color: #1e293b;
            margin-bottom: 10px;
            font-size: 24px;
        }
        .apps-count {
            color: #64748b;
            font-size: 16px;
        }
        .apps-grid {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
            gap: 20px;
            margin-top: 20px;
        }
        .app-card {
            background: #f8fafc;
            border: 1px solid #e2e8f0;
            border-radius: 8px;
            padding: 20px;
            transition: all 0.2s ease;
            cursor: pointer;
            display: flex;
            flex-direction: column;
            align-items: center;
            text-align: center;
            color: inherit;
        }
        .app-card:hover {
            transform: translateY(-4px);
            box-shadow: 0 8px 16px rgba(0,0,0,0.1);
            border-color: #2563eb;
        }
        .app-icon {
            width: 64px;
            height: 64px;
            border-radius: 12px;
            display: flex;
            align-items: center;
            justify-content: center;
            margin-bottom: 12px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.15);
            overflow: hidden;
            background: #f8fafc;
        }
        .app-icon img {
            width: 100%;
            height: 100%;
            object-fit: contain;
        }
        .app-name {
            font-weight: 600;
            color: #1e293b;
            font-size: 16px;
            margin-bottom: 8px;
            line-height: 1.3;
        }
        .app-platform {
            display: inline-block;
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 12px;
            font-weight: 500;
            margin-top: 8px;
        }
        .app-platform.darwin {
            background: #dbeafe;
            color: #1e40af;
        }
        .app-platform.windows {
            background: #dbeafe;
            color: #0284c7;
        }
        .app-version {
            font-size: 13px;
            color: #64748b;
            line-height: 1.4;
            margin-top: 8px;
            font-weight: 500;
        }
        .apps-grid.hidden {
            display: none;
        }
        /* Modal Styles */
        .modal {
            display: none !important;
            position: fixed;
            z-index: 1000;
            left: 0;
            top: 0;
            width: 100%;
            height: 100%;
            overflow: auto;
            background-color: rgba(0, 0, 0, 0.5);
            animation: fadeIn 0.2s ease;
            visibility: hidden;
            opacity: 0;
        }
        .modal.show {
            display: flex !important;
            align-items: center;
            justify-content: center;
            visibility: visible;
            opacity: 1;
        }
        @keyframes fadeIn {
            from { opacity: 0; }
            to { opacity: 1; }
        }
        .modal-content {
            background-color: white;
            margin: auto;
            padding: 0;
            border-radius: 12px;
            width: 90%;
            max-width: 600px;
            max-height: 90vh;
            overflow-y: auto;
            box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
            animation: slideUp 0.3s ease;
        }
        @keyframes slideUp {
            from {
                transform: translateY(50px);
                opacity: 0;
            }
            to {
                transform: translateY(0);
                opacity: 1;
            }
        }
        .modal-header {
            padding: 24px;
            border-bottom: 1px solid #e2e8f0;
            display: flex;
            align-items: center;
            gap: 16px;
        }
        .modal-icon {
            width: 64px;
            height: 64px;
            border-radius: 12px;
            display: flex;
            align-items: center;
            justify-content: center;
            box-shadow: 0 2px 8px rgba(0,0,0,0.15);
            overflow: hidden;
            background: #f8fafc;
            flex-shrink: 0;
        }
        .modal-icon img {
            width: 100%;
            height: 100%;
            object-fit: contain;
        }
        .modal-title-section {
            flex: 1;
        }
        .modal-title {
            font-size: 24px;
            font-weight: 600;
            color: #1e293b;
            margin: 0 0 4px 0;
        }
        .modal-platform {
            display: inline-block;
            padding: 4px 12px;
            border-radius: 6px;
            font-size: 13px;
            font-weight: 500;
            margin-top: 4px;
        }
        .modal-platform.darwin {
            background: #dbeafe;
            color: #1e40af;
        }
        .modal-platform.windows {
            background: #dbeafe;
            color: #0284c7;
        }
        .modal-close {
            color: #64748b;
            font-size: 28px;
            font-weight: 300;
            cursor: pointer;
            line-height: 1;
            padding: 0;
            background: none;
            border: none;
            width: 32px;
            height: 32px;
            display: flex;
            align-items: center;
            justify-content: center;
            border-radius: 6px;
            transition: all 0.2s ease;
        }
        .modal-close:hover {
            background: #f1f5f9;
            color: #1e293b;
        }
        .modal-body {
            padding: 24px;
        }
        .modal-footer {
            padding: 16px 24px;
            border-top: 1px solid #e2e8f0;
            text-align: center;
        }
        .modal-footer p {
            margin: 0;
            color: #64748b;
            font-size: 12px;
        }
        .modal-info-row {
            margin-bottom: 20px;
        }
        .modal-info-label {
            font-size: 12px;
            font-weight: 600;
            color: #64748b;
            text-transform: uppercase;
            letter-spacing: 0.5px;
            margin-bottom: 6px;
        }
        .modal-info-value {
            font-size: 16px;
            color: #1e293b;
            line-height: 1.6;
        }
        .modal-installer-link {
            display: block;
            padding: 12px 24px;
            background: #2563eb;
            color: white;
            text-decoration: none;
            border-radius: 6px;
            font-weight: 500;
            text-align: center;
            transition: all 0.2s ease;
            width: 100%;
            box-sizing: border-box;
        }
        .modal-installer-link:hover {
            background: #1d4ed8;
            transform: translateY(-2px);
            box-shadow: 0 4px 6px rgba(37, 99, 235, 0.3);
        }
        .modal-security-info {
            background: #f8fafc;
            border: 1px solid #e2e8f0;
            border-radius: 8px;
            padding: 16px;
            margin-top: 8px;
        }
        .modal-security-item {
            margin-bottom: 12px;
            display: flex;
            align-items: center;
            gap: 8px;
        }
        .modal-security-item:last-child {
            margin-bottom: 0;
        }
        .modal-security-label {
            font-weight: 600;
            color: #475569;
            flex-shrink: 0;
            min-width: 100px;
            font-size: 14px;
        }
        .modal-security-value {
            font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
            font-size: 13px;
            background: white;
            padding: 4px 8px;
            border-radius: 4px;
            border: 1px solid #e2e8f0;
            color: #1e293b;
            white-space: nowrap;
            overflow-x: auto;
            flex: 1;
            min-width: 0;
            cursor: pointer;
            transition: all 0.2s ease;
            position: relative;
        }
        .modal-security-value:hover {
            background: #f1f5f9;
            border-color: #2563eb;
        }
        .modal-security-value:active {
            background: #e0e7ff;
        }
        .modal-security-value.copied {
            background: #dcfce7;
            border-color: #22c55e;
        }
        .modal-security-value::after {
            content: 'Click to copy';
            position: absolute;
            bottom: 100%;
            left: 50%;
            transform: translateX(-50%);
            background: #1e293b;
            color: white;
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 11px;
            white-space: nowrap;
            opacity: 0;
            pointer-events: none;
            transition: opacity 0.2s ease;
            margin-bottom: 4px;
        }
        .modal-security-value:hover::after {
            opacity: 1;
        }
        .rss-button {
            display: inline-flex;
            align-items: center;
            gap: 8px;
            padding: 10px 20px;
            background: #2563eb;
            color: white;
            text-decoration: none;
            border-radius: 6px;
            font-weight: 500;
            font-size: 14px;
            transition: all 0.2s ease;
            flex-shrink: 0;
        }
        .rss-button:hover {
            background: #1d4ed8;
            transform: translateY(-2px);
            box-shadow: 0 4px 6px rgba(37, 99, 235, 0.3);
        }
        .rss-button svg {
            width: 18px;
            height: 18px;
            fill: currentColor;
            flex-shrink: 0;
        }
        @media (max-width: 768px) {
            .header-section {
                flex-direction: column;
                align-items: stretch;
            }
            .rss-button {
                margin-top: 15px;
                width: 100%;
                justify-content: center;
            }
            .apps-grid {
                grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
                gap: 15px;
            }
            .app-card {
                padding: 15px;
            }
            .app-icon {
                width: 48px;
                height: 48px;
                font-size: 24px;
            }
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header-section">
            <div class="header-content">
                <h1>Fleet-maintained app library</h1>
                <p class="subtitle">Continuous daily tracking of the Fleet-maintained app library</p>
            </div>
            <a href="feed.xml" class="rss-button" title="Subscribe to version updates">
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">
                    <path d="M6.503 20.752c0 1.794-1.456 3.248-3.251 3.248-1.796 0-3.252-1.454-3.252-3.248 0-1.794 1.456-3.248 3.252-3.248 1.795.001 3.251 1.454 3.251 3.248zm-6.503-12.572v4.811c6.05.062 10.96 4.966 11.022 11.009h4.817c-.062-8.71-7.118-15.758-15.839-15.82zm0-3.368c10.58.046 19.152 8.594 19.183 19.188h4.817c-.03-13.231-10.755-23.954-24-24v4.812z"/>
                </svg>
                Subscribe to updates
            </a>
        </div>
        
        <div class="chart-container">
            <canvas id="cumulativeChart"></canvas>
        </div>
        
        <div class="stats" id="stats">
            <!-- Stats will be populated by JavaScript -->
        </div>
        
        <div class="apps-section">
            <div class="apps-header">
                <h2>Fleet-maintained apps</h2>
                <p class="apps-count"><span id="appsCount">0</span> and counting...</p>
            </div>
            <div class="apps-grid" id="appsGrid">
                <!-- Apps will be populated by JavaScript -->
            </div>
        </div>
        
        <div class="footer">
            <p>Data source: <a href="https://github.com/fleetdm/fleet" target="_blank">fleetdm/fleet</a> | 
            Last updated: ` + lastUpdated + `</p>
        </div>
    </div>

    <!-- App Details Modal -->
    <div id="appModal" class="modal">
        <div class="modal-content">
            <div class="modal-header">
                <div class="modal-icon" id="modalIcon">
                    <img id="modalIconImg" src="" alt="" onerror="handleModalIconError(this);">
                </div>
                <div class="modal-title-section">
                    <h2 class="modal-title" id="modalTitle"></h2>
                    <span class="modal-platform" id="modalPlatform"></span>
                </div>
                <button class="modal-close" onclick="closeModal()">&times;</button>
            </div>
            <div class="modal-body">
                <div class="modal-info-row">
                    <div class="modal-info-label">Version</div>
                    <div class="modal-info-value" id="modalVersion"></div>
                </div>
                <div class="modal-info-row">
                    <div class="modal-info-label">Description</div>
                    <div class="modal-info-value" id="modalDescription"></div>
                </div>
                <div class="modal-info-row" id="modalSecurityRow" style="display: none;">
                    <div class="modal-info-label">Security Information</div>
                    <div id="modalSecurityContainer">
                        <!-- Single app security info (legacy) -->
                        <div class="modal-security-info" id="modalSecuritySingle">
                            <div class="modal-security-item">
                                <span class="modal-security-label">SHA-256:</span>
                                <code class="modal-security-value" id="modalSha256"></code>
                            </div>
                            <div class="modal-security-item">
                                <span class="modal-security-label">CDHash:</span>
                                <code class="modal-security-value" id="modalCdhash"></code>
                            </div>
                            <div class="modal-security-item">
                                <span class="modal-security-label">Signing ID:</span>
                                <code class="modal-security-value" id="modalSigningID"></code>
                            </div>
                            <div class="modal-security-item">
                                <span class="modal-security-label">Team ID:</span>
                                <code class="modal-security-value" id="modalTeamID"></code>
                            </div>
                        </div>
                        <!-- Multiple apps security info (suites) -->
                        <div id="modalSecurityMultiple"></div>
                    </div>
                </div>
                <div class="modal-info-row" id="modalInstallerRow" style="display: none; margin-top: 24px;">
                    <a href="#" id="modalInstallerLink" class="modal-installer-link" target="_blank" rel="noopener noreferrer">Download Installer</a>
                </div>
            </div>
            <div class="modal-footer">
                <p id="modalLastUpdated">Last updated: ` + lastUpdated + `</p>
            </div>
        </div>
    </div>

    <script>
        // Embedded CSV data
        const csvData = ` + dataJSONStr + `;
        
        // Embedded apps data
        const appsData = ` + appsJSONStr + `;
        
        // Process data into format needed for charts
        function processData() {
            const data = {
                dates: csvData.dates.map(d => new Date(d + 'T00:00:00')),
                counts: csvData.counts,
                additions: csvData.additions,
                macCounts: csvData.macCounts || [],
                windowsCounts: csvData.windowsCounts || [],
                growthDates: csvData.growthDates.map(d => new Date(d + 'T00:00:00')),
                growthCounts: csvData.growthCounts,
                growthAdditions: csvData.growthAdditions
            };
            return data;
        }
        
        let chartInstance = null;
        let chartData = null;
        let currentFilter = 'total';
        
        function getAppIconUrl(slug) {
            // Convert slug format "app-name/platform" to icon filename "app-icon-app-name-60x60@2x.png"
            const appName = slug.split('/')[0];
            const iconFilename = 'app-icon-' + appName + '-60x60@2x.png';
            return 'https://raw.githubusercontent.com/fleetdm/fleet/main/website/assets/images/' + iconFilename;
        }
        
        function getAppIconFallback(name) {
            // Get first letter or first two letters for fallback icon
            const words = name.split(' ');
            if (words.length > 1) {
                return (words[0][0] + words[1][0]).toUpperCase();
            }
            return name.substring(0, 2).toUpperCase();
        }
        
        function getPlatformLabel(platform) {
            return platform === 'darwin' ? 'Mac' : 'Windows';
        }
        
        function handleIconError(img) {
            const iconDiv = img.parentElement;
            const fallbackText = iconDiv.getAttribute('data-fallback') || '?';
            img.style.display = 'none';
            iconDiv.innerHTML = '<div style="width:100%;height:100%;display:flex;align-items:center;justify-content:center;background:linear-gradient(135deg, #667eea 0%, #764ba2 100%);color:white;font-weight:bold;font-size:24px;">' + escapeHtml(fallbackText) + '</div>';
        }
        
        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }
        
        function filterApps(viewType) {
            currentFilter = viewType;
            const grid = document.getElementById('appsGrid');
            const countEl = document.getElementById('appsCount');
            
            let filteredApps = appsData;
            
            if (viewType === 'mac') {
                filteredApps = appsData.filter(app => app.platform === 'darwin');
            } else if (viewType === 'windows') {
                filteredApps = appsData.filter(app => app.platform === 'windows');
            }
            
            // Sort apps by name (case-insensitive), then by platform to group same-name apps together
            filteredApps.sort((a, b) => {
                const nameA = a.name.toLowerCase();
                const nameB = b.name.toLowerCase();
                if (nameA !== nameB) {
                    return nameA.localeCompare(nameB);
                }
                // If names are the same, sort by platform (darwin before windows)
                return a.platform.localeCompare(b.platform);
            });
            
            countEl.textContent = filteredApps.length;
            
            grid.innerHTML = filteredApps.map(app => {
                const iconUrl = getAppIconUrl(app.slug);
                const fallbackText = getAppIconFallback(app.name);
                const platformLabel = getPlatformLabel(app.platform);
                const version = app.version || 'N/A';
                const versionHtml = '<div class="app-version">' + escapeHtml(version) + '</div>';
                
                // Make cards clickable divs that open modal
                // Store app slug to find app data when clicked
                return '<div class="app-card" data-platform="' + escapeHtml(app.platform) + '" data-app-slug="' + escapeHtml(app.slug) + '" onclick="openModalFromCard(this)" style="cursor: pointer;">' +
                    '<div class="app-icon" data-fallback="' + escapeHtml(fallbackText) + '">' +
                    '<img src="' + escapeHtml(iconUrl) + '" alt="' + escapeHtml(app.name) + '" onerror="handleIconError(this);">' +
                    '</div>' +
                    '<div class="app-name">' + escapeHtml(app.name) + '</div>' +
                    versionHtml +
                    '<span class="app-platform ' + escapeHtml(app.platform) + '">' + escapeHtml(platformLabel) + '</span>' +
                    '</div>';
            }).join('');
        }
        
        function updateChart(viewType) {
            if (!chartInstance || !chartData) return;
            
            let dataArray, label, color, borderColor, backgroundColor;
            
            switch(viewType) {
                case 'total':
                    dataArray = chartData.counts;
                    label = 'Total Apps';
                    color = '#2563eb';
                    borderColor = '#2563eb';
                    backgroundColor = 'rgba(37, 99, 235, 0.1)';
                    break;
                case 'mac':
                    dataArray = chartData.macCounts;
                    label = 'Mac Apps';
                    color = '#059669';
                    borderColor = '#059669';
                    backgroundColor = 'rgba(5, 150, 105, 0.1)';
                    break;
                case 'windows':
                    dataArray = chartData.windowsCounts;
                    label = 'Windows Apps';
                    color = '#0284c7';
                    borderColor = '#0284c7';
                    backgroundColor = 'rgba(2, 132, 199, 0.1)';
                    break;
                default:
                    return;
            }
            
            // Update chart data
            chartInstance.data.datasets[0].label = label;
            chartInstance.data.datasets[0].data = chartData.dates.map((date, i) => ({x: date, y: dataArray[i]}));
            chartInstance.data.datasets[0].borderColor = borderColor;
            chartInstance.data.datasets[0].backgroundColor = backgroundColor;
            
            // Update tooltip callback
            chartInstance.options.plugins.tooltip.callbacks.label = function(context) {
                const idx = chartData.dates.findIndex(d => 
                    d.getTime() === context.raw.x.getTime());
                const current = dataArray[idx];
                const prev = idx > 0 ? dataArray[idx - 1] : 0;
                const added = current - prev;
                return label + ': ' + context.parsed.y + ' apps' + (added > 0 ? ' (+' + added + ' added)' : '');
            };
            
            // Update active state
            document.querySelectorAll('.stat-card').forEach(card => {
                card.classList.remove('active');
            });
            document.querySelector('.stat-card[data-view="' + viewType + '"]').classList.add('active');
            
            // Update apps filter
            filterApps(viewType);
            
            chartInstance.update();
        }
        
        function createCharts() {
            const data = processData();
            chartData = data;
            
            // Calculate stats
            const daysSpan = Math.ceil((data.dates[data.dates.length - 1] - data.dates[0]) / (1000 * 60 * 60 * 24));
            const totalApps = data.counts[data.counts.length - 1];
            const macApps = data.macCounts.length > 0 ? data.macCounts[data.macCounts.length - 1] : 0;
            const windowsApps = data.windowsCounts.length > 0 ? data.windowsCounts[data.windowsCounts.length - 1] : 0;
            
            // Update stats cards
            document.getElementById('stats').innerHTML = 
                '<div class="stat-card clickable active" data-view="total">' +
                    '<div class="stat-value">' + totalApps + '</div>' +
                    '<div class="stat-label">Total Apps</div>' +
                '</div>' +
                '<div class="stat-card clickable" data-view="mac">' +
                    '<div class="stat-value">' + macApps + '</div>' +
                    '<div class="stat-label">Mac Apps</div>' +
                '</div>' +
                '<div class="stat-card clickable" data-view="windows">' +
                    '<div class="stat-value">' + windowsApps + '</div>' +
                    '<div class="stat-label">Windows Apps</div>' +
                '</div>' +
                '<div class="stat-card">' +
                    '<div class="stat-value">' + daysSpan + '</div>' +
                    '<div class="stat-label">Days Tracked</div>' +
                '</div>';
            
            // Add click event listeners to stat cards
            document.querySelectorAll('.stat-card.clickable').forEach(card => {
                card.addEventListener('click', function() {
                    const viewType = this.getAttribute('data-view');
                    updateChart(viewType);
                });
            });
            
            // Initialize apps display
            filterApps('total');
            
            // Cumulative Growth Chart
            const ctx1 = document.getElementById('cumulativeChart').getContext('2d');
            chartInstance = new Chart(ctx1, {
                type: 'line',
                data: {
                    datasets: [{
                        label: 'Total Apps',
                        data: data.dates.map((date, i) => ({x: date, y: data.counts[i]})),
                        borderColor: '#2563eb',
                        backgroundColor: 'rgba(37, 99, 235, 0.1)',
                        borderWidth: 2.5,
                        pointRadius: 0,
                        fill: true,
                        tension: 0,
                        stepped: 'after'
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        title: {
                            display: true,
                            text: 'Cumulative Growth (Daily)',
                            font: { size: 16, weight: 'bold' }
                        },
                        legend: {
                            display: true,
                            position: 'top'
                        },
                        tooltip: {
                            callbacks: {
                                label: function(context) {
                                    const idx = data.dates.findIndex(d => 
                                        d.getTime() === context.raw.x.getTime());
                                    const added = idx > 0 ? data.counts[idx] - data.counts[idx - 1] : data.counts[idx];
                                    return 'Total Apps: ' + context.parsed.y + ' apps' + (added > 0 ? ' (+' + added + ' added)' : '');
                                }
                            }
                        }
                    },
                    scales: {
                        x: {
                            type: 'time',
                            time: {
                                unit: 'month',
                                displayFormats: {
                                    month: 'MMM'
                                }
                            },
                            title: {
                                display: true,
                                text: 'Date',
                                font: { weight: 'bold' }
                            }
                        },
                        y: {
                            beginAtZero: true,
                            title: {
                                display: true,
                                text: 'Number of Apps',
                                font: { weight: 'bold' }
                            },
                            ticks: {
                                stepSize: 5
                            }
                        }
                    }
                }
            });
        }
        
        createCharts();
        
        // Modal functions
        function openModalFromCard(cardElement) {
            // Handle clicks on child elements - find the card element
            let card = cardElement;
            while (card && !card.classList.contains('app-card')) {
                card = card.parentElement;
            }
            if (!card) {
                console.error('Could not find app-card element');
                return;
            }
            
            const appSlug = card.getAttribute('data-app-slug');
            if (!appSlug) {
                console.error('No app-slug attribute found');
                return;
            }
            
            // Find the app in appsData array
            const app = appsData.find(a => a.slug === appSlug);
            if (app) {
                openModal(app);
            } else {
                console.error('App not found for slug:', appSlug);
            }
        }
        
        function openModal(app) {
            const modal = document.getElementById('appModal');
            if (!modal) {
                console.error('Modal element not found');
                return;
            }
            
            const iconUrl = getAppIconUrl(app.slug);
            const fallbackText = getAppIconFallback(app.name);
            const platformLabel = getPlatformLabel(app.platform);
            
            // Set modal icon - reset and reload to ensure it displays
            const modalIcon = document.getElementById('modalIcon');
            if (modalIcon) {
                modalIcon.setAttribute('data-fallback', fallbackText);
                // Reset the icon container and create new image element with the URL directly
                modalIcon.innerHTML = '<img id="modalIconImg" src="' + escapeHtml(iconUrl) + '" alt="' + escapeHtml(app.name) + '" onerror="handleModalIconError(this);" style="display:block;width:100%;height:100%;object-fit:contain;">';
            }
            
            // Set modal title and platform
            const modalTitle = document.getElementById('modalTitle');
            if (modalTitle) {
                modalTitle.textContent = app.name;
            }
            
            const modalPlatform = document.getElementById('modalPlatform');
            if (modalPlatform) {
                modalPlatform.textContent = platformLabel;
                modalPlatform.className = 'modal-platform ' + app.platform;
            }
            
            // Set version
            const modalVersion = document.getElementById('modalVersion');
            if (modalVersion) {
                modalVersion.textContent = app.version || 'N/A';
            }
            
            // Set description
            const modalDescription = document.getElementById('modalDescription');
            if (modalDescription) {
                const description = app.description || 'No description available.';
                modalDescription.textContent = description;
            }
            
            // Set installer link
            const installerRow = document.getElementById('modalInstallerRow');
            const installerLink = document.getElementById('modalInstallerLink');
            if (installerRow && installerLink) {
                if (app.installerUrl) {
                    installerLink.href = app.installerUrl;
                    installerRow.style.display = 'block';
                } else {
                    installerRow.style.display = 'none';
                }
            }
            
            // Set security info (macOS and Windows)
            const securityRow = document.getElementById('modalSecurityRow');
            const securitySingle = document.getElementById('modalSecuritySingle');
            const securityMultiple = document.getElementById('modalSecurityMultiple');
            
            // Debug logging
            console.log('Security Info Debug:', {
                hasSecurityInfo: !!app.securityInfo,
                securityInfo: app.securityInfo,
                platform: app.platform,
                slug: app.slug
            });
            
            if (securityRow) {
                if (app.securityInfo) {
                    // Check if this is a suite with multiple apps
                    if (app.securityInfo.apps && app.securityInfo.apps.length > 0) {
                        console.log('Suite detected with', app.securityInfo.apps.length, 'apps');
                        // Hide single app view, show multiple apps view
                        if (securitySingle) securitySingle.style.display = 'none';
                        if (securityMultiple) {
                            securityMultiple.innerHTML = '';
                            
                            // Create a section for each app in the suite
                            app.securityInfo.apps.forEach((suiteApp, index) => {
                                console.log('Processing suite app', index, ':', suiteApp.name, suiteApp);
                                const appSection = document.createElement('div');
                                appSection.className = 'modal-security-app-section';
                                appSection.style.marginBottom = index < app.securityInfo.apps.length - 1 ? '24px' : '0';
                                
                                const appTitle = document.createElement('div');
                                appTitle.className = 'modal-security-app-title';
                                appTitle.textContent = suiteApp.name || 'App ' + (index + 1);
                                appTitle.style.fontWeight = '600';
                                appTitle.style.color = '#1e293b';
                                appTitle.style.marginBottom = '12px';
                                appTitle.style.fontSize = '15px';
                                
                                const appInfo = document.createElement('div');
                                appInfo.className = 'modal-security-info';
                                
                                // Determine fields based on platform
                                const isWindows = app.platform === 'windows';
                                const fields = isWindows ? [
                                    { label: 'SHA-256', value: suiteApp.sha256, id: 'sha256' },
                                    { label: 'Publisher', value: suiteApp.publisher, id: 'publisher' },
                                    { label: 'Issuer', value: suiteApp.issuer, id: 'issuer' },
                                    { label: 'Serial Number', value: suiteApp.serialNumber, id: 'serialNumber' },
                                    { label: 'Thumbprint', value: suiteApp.thumbprint, id: 'thumbprint' },
                                    { label: 'Timestamp', value: suiteApp.timestamp, id: 'timestamp' }
                                ] : [
                                    { label: 'SHA-256', value: suiteApp.sha256, id: 'sha256' },
                                    { label: 'CDHash', value: suiteApp.cdhash, id: 'cdhash' },
                                    { label: 'Signing ID', value: suiteApp.signingId, id: 'signingId' },
                                    { label: 'Team ID', value: suiteApp.teamId, id: 'teamId' }
                                ];
                                
                                fields.forEach(field => {
                                    if (field.value) {
                                        const item = document.createElement('div');
                                        item.className = 'modal-security-item';
                                        
                                        const label = document.createElement('span');
                                        label.className = 'modal-security-label';
                                        label.textContent = field.label + ':';
                                        
                                        const value = document.createElement('code');
                                        value.className = 'modal-security-value';
                                        value.textContent = field.value;
                                        setupCopyToClipboard(value, field.value);
                                        
                                        item.appendChild(label);
                                        item.appendChild(value);
                                        appInfo.appendChild(item);
                                    }
                                });
                                
                                appSection.appendChild(appTitle);
                                appSection.appendChild(appInfo);
                                securityMultiple.appendChild(appSection);
                            });
                            
                            securityMultiple.style.display = 'block';
                            securityRow.style.display = 'block';
                        }
                    } else {
                        // Single app view - dynamically build security info based on platform
                        if (securitySingle) {
                            securitySingle.style.display = 'block';
                            // Ensure the container has the correct class
                            if (!securitySingle.classList.contains('modal-security-info')) {
                                securitySingle.classList.add('modal-security-info');
                            }
                        }
                        if (securityMultiple) securityMultiple.style.display = 'none';
                        
                        // Clear existing content and rebuild based on platform
                        const securityContainer = securitySingle;
                        if (securityContainer) {
                            securityContainer.innerHTML = '';
                            
                            const isWindows = app.platform === 'windows';
                            const fields = isWindows ? [
                                { label: 'SHA-256', value: app.securityInfo.sha256, id: 'sha256' },
                                { label: 'Publisher', value: app.securityInfo.publisher, id: 'publisher' },
                                { label: 'Issuer', value: app.securityInfo.issuer, id: 'issuer' },
                                { label: 'Serial Number', value: app.securityInfo.serialNumber, id: 'serialNumber' },
                                { label: 'Thumbprint', value: app.securityInfo.thumbprint, id: 'thumbprint' },
                                { label: 'Timestamp', value: app.securityInfo.timestamp, id: 'timestamp' }
                            ] : [
                                { label: 'SHA-256', value: app.securityInfo.sha256, id: 'sha256' },
                                { label: 'CDHash', value: app.securityInfo.cdhash, id: 'cdhash' },
                                { label: 'Signing ID', value: app.securityInfo.signingId, id: 'signingId' },
                                { label: 'Team ID', value: app.securityInfo.teamId, id: 'teamId' }
                            ];
                            
                            let hasFields = false;
                            console.log('Single app security fields:', fields);
                            fields.forEach(field => {
                                if (field.value) {
                                    hasFields = true;
                                    console.log('Adding field:', field.label, '=', field.value);
                                    const item = document.createElement('div');
                                    item.className = 'modal-security-item';
                                    
                                    const label = document.createElement('span');
                                    label.className = 'modal-security-label';
                                    label.textContent = field.label + ':';
                                    
                                    const value = document.createElement('code');
                                    value.className = 'modal-security-value';
                                    value.textContent = field.value;
                                    setupCopyToClipboard(value, field.value);
                                    
                                    item.appendChild(label);
                                    item.appendChild(value);
                                    securityContainer.appendChild(item);
                                }
                            });
                            
                            // Only show security row if we have at least one field
                            console.log('Single app hasFields:', hasFields);
                            if (hasFields) {
                                securityRow.style.display = 'block';
                                console.log('Security row set to block');
                            } else {
                                securityRow.style.display = 'none';
                                console.log('Security row set to none (no fields)');
                            }
                        } else {
                            securityRow.style.display = 'block';
                        }
                    }
                } else {
                    securityRow.style.display = 'none';
                }
            }
            
            // Set last updated timestamp
            const modalLastUpdated = document.getElementById('modalLastUpdated');
            if (modalLastUpdated) {
                let timestampText = 'Last updated: ' + ` + "`" + lastUpdated + "`" + `;
                
                // If app has security info with lastUpdated, use that instead
                if (app.securityInfo && app.securityInfo.lastUpdated) {
                    // Parse RFC3339 timestamp (UTC) and convert to CST
                    const securityDate = new Date(app.securityInfo.lastUpdated);
                    
                    // Format in CST timezone: "January 2, 2006 at 3:04 PM CST"
                    const cstFormatter = new Intl.DateTimeFormat('en-US', {
                        timeZone: 'America/Chicago',
                        year: 'numeric',
                        month: 'long',
                        day: 'numeric',
                        hour: 'numeric',
                        minute: '2-digit',
                        hour12: true
                    });
                    
                    const parts = cstFormatter.formatToParts(securityDate);
                    const month = parts.find(p => p.type === 'month').value;
                    const day = parts.find(p => p.type === 'day').value;
                    const year = parts.find(p => p.type === 'year').value;
                    const hour = parts.find(p => p.type === 'hour').value;
                    const minute = parts.find(p => p.type === 'minute').value;
                    const dayPeriod = parts.find(p => p.type === 'dayPeriod').value.toUpperCase();
                    
                    timestampText = 'Last updated: ' + month + ' ' + day + ', ' + year + ' at ' + hour + ':' + minute + ' ' + dayPeriod + ' CST';
                }
                
                modalLastUpdated.textContent = timestampText;
            }
            
            // Show modal
            modal.classList.add('show');
            document.body.style.overflow = 'hidden';
        }
        
        function closeModal() {
            const modal = document.getElementById('appModal');
            modal.classList.remove('show');
            document.body.style.overflow = '';
        }
        
        function handleModalIconError(img) {
            const iconDiv = img.parentElement;
            const fallbackText = iconDiv.getAttribute('data-fallback') || '?';
            img.style.display = 'none';
            iconDiv.innerHTML = '<div style="width:100%;height:100%;display:flex;align-items:center;justify-content:center;background:linear-gradient(135deg, #667eea 0%, #764ba2 100%);color:white;font-weight:bold;font-size:24px;">' + escapeHtml(fallbackText) + '</div>';
        }
        
        // Close modal when clicking outside (on the backdrop)
        document.getElementById('appModal').addEventListener('click', function(event) {
            // Only close if clicking directly on the modal backdrop, not on modal-content
            if (event.target.id === 'appModal') {
                closeModal();
            }
        });
        
        // Close modal with Escape key
        document.addEventListener('keydown', function(event) {
            if (event.key === 'Escape') {
                closeModal();
            }
        });
        
        // Copy to clipboard functionality
        function setupCopyToClipboard(element, text) {
            if (!element || text === 'N/A') return;
            
            element.addEventListener('click', async function() {
                try {
                    await navigator.clipboard.writeText(text);
                    // Visual feedback
                    element.classList.add('copied');
                    const originalText = element.textContent;
                    element.textContent = 'Copied!';
                    
                    setTimeout(() => {
                        element.classList.remove('copied');
                        element.textContent = originalText;
                    }, 2000);
                } catch (err) {
                    // Fallback for older browsers
                    const textArea = document.createElement('textarea');
                    textArea.value = text;
                    textArea.style.position = 'fixed';
                    textArea.style.opacity = '0';
                    document.body.appendChild(textArea);
                    textArea.select();
                    try {
                        document.execCommand('copy');
                        element.classList.add('copied');
                        const originalText = element.textContent;
                        element.textContent = 'Copied!';
                        setTimeout(() => {
                            element.classList.remove('copied');
                            element.textContent = originalText;
                        }, 2000);
                    } catch (fallbackErr) {
                        console.error('Failed to copy:', fallbackErr);
                    }
                    document.body.removeChild(textArea);
                }
            });
        }
    </script>
</body>
</html>`
}
