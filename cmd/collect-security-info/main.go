package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	securityVersionsJSON = "../../data/app_versions.json"
	securityInfoJSON     = "../../data/app_security_info.json"
	tempDir              = "/tmp/fleet-app-install"
	applicationsDir      = "/Applications"
)

type securityAppVersionInfo struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Platform     string `json:"platform"`
	Version      string `json:"version"`
	InstallerURL string `json:"installerUrl"`
}

type securityAppVersionsData struct {
	LastUpdated string                   `json:"lastUpdated"`
	Apps        []securityAppVersionInfo `json:"apps"`
}

type appSecurityInfo struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Sha256      string `json:"sha256"`
	Cdhash      string `json:"cdhash"`
	SigningID   string `json:"signingId"`
	TeamID      string `json:"teamId"`
	LastUpdated string `json:"lastUpdated"`
}

type securityInfoData struct {
	LastUpdated string            `json:"lastUpdated"`
	Apps        []appSecurityInfo `json:"apps"`
}

func main() {
	fmt.Println("🔒 Collecting macOS App Security Information")
	fmt.Println("============================================")
	fmt.Println()

	// Load current app versions
	versions, err := loadAppVersions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error loading app versions: %v\n", err)
		os.Exit(1)
	}

	// Load existing security info
	existingSecurity, err := loadSecurityInfo()
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: Error loading existing security info: %v (will reprocess all apps)\n", err)
	}
	existingMap := make(map[string]appSecurityInfo)
	if existingSecurity != nil {
		for _, app := range existingSecurity.Apps {
			existingMap[app.Slug] = app
		}
		fmt.Printf("📋 Loaded %d existing security info entries\n", len(existingMap))
	} else {
		fmt.Printf("📋 No existing security info found (starting fresh)\n")
	}

	// Filter to macOS apps only
	var macApps []securityAppVersionInfo
	for _, app := range versions.Apps {
		if app.Platform == "darwin" && app.InstallerURL != "" {
			// Check if we need to update this app
			existing, exists := existingMap[app.Slug]
			if !exists || existing.Version != app.Version {
				macApps = append(macApps, app)
			}
		}
	}

	if len(macApps) == 0 {
		fmt.Println("✅ All macOS apps are up to date. No security info collection needed.")
		return
	}

	// Check for test mode (limit to first app)
	testMode := len(os.Args) > 1 && os.Args[1] == "--test"
	if testMode && len(macApps) > 0 {
		fmt.Printf("🧪 TEST MODE: Processing only first app: %s\n\n", macApps[0].Name)
		macApps = macApps[:1]
	}

	fmt.Printf("📦 Found %d macOS apps to process\n\n", len(macApps))

	// Create temp directory
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error creating temp directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	// Set up signal handling to save on interruption
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Track collected security info
	collectedSecurity := make(map[string]appSecurityInfo)
	processedSlugs := make(map[string]bool)
	processedCount := 0

	// Save function that merges with existing data
	saveSecurityInfo := func() error {
		// Merge collected data with existing data
		finalSecurityMap := make(map[string]appSecurityInfo)

		// Add existing apps that weren't processed
		for slug, existing := range existingMap {
			if !processedSlugs[slug] {
				// Check if this app still exists in current versions
				found := false
				for _, v := range versions.Apps {
					if v.Slug == slug && v.Platform == "darwin" {
						found = true
						break
					}
				}
				if found {
					finalSecurityMap[slug] = existing
				}
			}
		}

		// Add newly collected data
		for slug, info := range collectedSecurity {
			finalSecurityMap[slug] = info
		}

		// Convert map to sorted slice
		var finalSecurityList []appSecurityInfo
		for _, app := range finalSecurityMap {
			finalSecurityList = append(finalSecurityList, app)
		}
		sort.Slice(finalSecurityList, func(i, j int) bool {
			return finalSecurityList[i].Slug < finalSecurityList[j].Slug
		})

		// Save to file
		securityData := securityInfoData{
			LastUpdated: time.Now().UTC().Format(time.RFC3339),
			Apps:        finalSecurityList,
		}

		jsonData, err := json.MarshalIndent(securityData, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling security info: %w", err)
		}

		if err := os.WriteFile(securityInfoJSON, jsonData, 0644); err != nil {
			return fmt.Errorf("writing security info: %w", err)
		}

		return nil
	}

	// Handle interruptions
	go func() {
		<-sigChan
		fmt.Printf("\n⚠️  Interruption detected. Saving progress...\n")
		if err := saveSecurityInfo(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error saving on interruption: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Progress saved. Processed %d/%d apps before interruption.\n", processedCount, len(macApps))
		os.Exit(0)
	}()

	// Process each app
	for i, app := range macApps {
		fmt.Printf("[%d/%d] Processing %s (%s)...\n", i+1, len(macApps), app.Name, app.Version)

		securityInfo, err := collectSecurityInfoForApp(app)
		if err != nil {
			fmt.Printf("  ⚠️  Warning: Failed to collect security info: %v\n", err)
			// Keep existing info if available
			if existing, exists := existingMap[app.Slug]; exists {
				collectedSecurity[app.Slug] = existing
				processedSlugs[app.Slug] = true
			}
			// Save progress even on failure
			if err := saveSecurityInfo(); err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠️  Warning: Failed to save progress: %v\n", err)
			}
			continue
		}

		collectedSecurity[app.Slug] = securityInfo
		processedSlugs[app.Slug] = true
		processedCount++

		// Save incrementally after each successful collection
		if err := saveSecurityInfo(); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠️  Warning: Failed to save progress: %v\n", err)
		} else {
			fmt.Printf("  💾 Progress saved (%d/%d apps)\n", processedCount, len(macApps))
		}

		// Commit changes periodically (every 10 apps or on first/last app) to preserve progress
		shouldCommit := processedCount == 1 || processedCount%10 == 0 || processedCount == len(macApps)
		if shouldCommit {
			if err := commitProgress(processedCount, len(macApps)); err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠️  Warning: Failed to commit progress: %v\n", err)
			} else {
				fmt.Printf("  📝 Progress committed to repo (%d/%d apps)\n", processedCount, len(macApps))
			}
		}

		// Clean up after each app to save disk space
		cleanupTempFiles()
	}

	// Final save (redundant but ensures everything is saved)
	if err := saveSecurityInfo(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error saving final security info: %v\n", err)
		os.Exit(1)
	}

	// Final commit
	if err := commitProgress(processedCount, len(macApps)); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to commit final progress: %v\n", err)
	}

	fmt.Printf("\n✅ Successfully processed %d/%d apps\n", processedCount, len(macApps))
	fmt.Printf("✅ Security info saved to: %s\n", securityInfoJSON)
}

func commitProgress(processedCount, totalApps int) error {
	// Check if we're in a git repository and have changes
	if err := exec.Command("git", "rev-parse", "--git-dir").Run(); err != nil {
		// Not in a git repo, skip commit
		return nil
	}

	// Check if there are changes
	statusCmd := exec.Command("git", "status", "--porcelain", securityInfoJSON)
	output, err := statusCmd.Output()
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}

	if len(output) == 0 {
		// No changes, nothing to commit
		return nil
	}

	// Configure git (if not already configured)
	exec.Command("git", "config", "--local", "user.email", "action@github.com").Run()
	exec.Command("git", "config", "--local", "user.name", "GitHub Action").Run()

	// Add the file
	if err := exec.Command("git", "add", securityInfoJSON).Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	// Commit
	commitMsg := fmt.Sprintf("Update macOS app security info - %d/%d apps processed", processedCount, totalApps)
	if err := exec.Command("git", "commit", "-m", commitMsg).Run(); err != nil {
		// If commit fails (e.g., no changes), that's okay
		return nil
	}

	// Push (non-blocking - if it fails, that's okay, next run will push)
	go func() {
		exec.Command("git", "push").Run()
	}()

	return nil
}

func loadAppVersions() (*securityAppVersionsData, error) {
	data, err := os.ReadFile(securityVersionsJSON)
	if err != nil {
		return nil, err
	}

	var versions securityAppVersionsData
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, err
	}

	return &versions, nil
}

func loadSecurityInfo() (*securityInfoData, error) {
	data, err := os.ReadFile(securityInfoJSON)
	if err != nil {
		if os.IsNotExist(err) {
			return &securityInfoData{Apps: []appSecurityInfo{}}, nil
		}
		return nil, err
	}

	var security securityInfoData
	if err := json.Unmarshal(data, &security); err != nil {
		return nil, err
	}

	return &security, nil
}

func collectSecurityInfoForApp(app securityAppVersionInfo) (appSecurityInfo, error) {
	var securityInfo appSecurityInfo

	// Download installer
	installerPath, err := downloadInstaller(app.InstallerURL, app.Slug)
	if err != nil {
		return securityInfo, fmt.Errorf("failed to download installer: %w", err)
	}
	defer os.Remove(installerPath)

	// Install app
	appPath, err := installApp(installerPath, app)
	if err != nil {
		return securityInfo, fmt.Errorf("failed to install app: %w", err)
	}

	// Verify the app exists before proceeding
	if _, err := os.Stat(appPath); err != nil {
		return securityInfo, fmt.Errorf("installed app not found at %s: %w", appPath, err)
	}
	fmt.Printf("  ✅ Verified app exists at: %s\n", appPath)

	// Wait longer to ensure app is fully installed and ready (santactl can take time)
	time.Sleep(3 * time.Second)

	// Run santactl fileinfo
	santactlOutput, err := runSantactl(appPath)
	if err != nil {
		// Try to uninstall even if santactl failed
		uninstallApp(app)
		return securityInfo, fmt.Errorf("failed to run santactl: %w", err)
	}

	// Parse santactl output
	securityInfo, err = parseSantactlOutput(santactlOutput, app)
	if err != nil {
		uninstallApp(app)
		return securityInfo, fmt.Errorf("failed to parse santactl output: %w", err)
	}

	// Uninstall app
	if err := uninstallApp(app); err != nil {
		fmt.Printf("  ⚠️  Warning: Failed to uninstall app: %v\n", err)
	}

	return securityInfo, nil
}

func downloadInstaller(url, slug string) (string, error) {
	fmt.Printf("  📥 Downloading installer...\n")

	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download: status %d", resp.StatusCode)
	}

	// Determine file extension from URL or Content-Type header
	ext := getInstallerExtension(url, resp.Header.Get("Content-Type"))
	if ext == "" {
		ext = ".dmg" // Default to DMG
	}

	filename := filepath.Join(tempDir, fmt.Sprintf("%s%s", strings.ReplaceAll(slug, "/", "_"), ext))
	out, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		out.Close()
		os.Remove(filename) // Clean up partial download
		return "", err
	}
	out.Close() // Close before checking file type

	// Verify file was actually written and has content
	if info, err := os.Stat(filename); err != nil || info.Size() == 0 {
		if err == nil {
			os.Remove(filename)
			return "", fmt.Errorf("downloaded file is empty")
		}
		return "", fmt.Errorf("downloaded file not found: %w", err)
	}

	// Verify and correct file type by checking actual file content
	actualExt, err := detectActualFileType(filename)
	if err == nil && actualExt != "" && actualExt != ext {
		// File type doesn't match extension, rename it
		newFilename := strings.TrimSuffix(filename, ext) + actualExt
		if err := os.Rename(filename, newFilename); err != nil {
			fmt.Printf("  ⚠️  Warning: Detected file type %s but extension was %s, rename failed: %v\n", actualExt, ext, err)
			return filename, nil // Return original filename
		}
		fmt.Printf("  ℹ️  Detected actual file type: %s (was %s)\n", actualExt, ext)
		return newFilename, nil
	}

	return filename, nil
}

// detectActualFileType uses the `file` command to determine the actual file type
func detectActualFileType(filepath string) (string, error) {
	cmd := exec.Command("file", filepath)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	fileType := strings.ToLower(string(output))
	
	// Check for PKG (xar archive)
	if strings.Contains(fileType, "xar archive") || strings.Contains(fileType, "pkg") {
		return ".pkg", nil
	}
	
	// Check for DMG
	if strings.Contains(fileType, "disk image") || strings.Contains(fileType, "dmg") || strings.Contains(fileType, "udif") {
		return ".dmg", nil
	}
	
	// Check for ZIP (handle various formats: "Zip archive", "Zip archive data", etc.)
	if strings.Contains(fileType, "zip archive") || strings.Contains(fileType, "zip") || 
	   strings.Contains(fileType, "compressed") && !strings.Contains(fileType, "dmg") {
		return ".zip", nil
	}

	return "", nil // Unknown type, keep original extension
}

// getInstallerExtension determines the installer file extension from URL and Content-Type
func getInstallerExtension(url, contentType string) string {
	// First, try to get extension from Content-Type header
	if contentType != "" {
		if strings.Contains(contentType, "application/x-apple-diskimage") || strings.Contains(contentType, "application/octet-stream") {
			// Check URL for .dmg
			if strings.Contains(strings.ToLower(url), ".dmg") {
				return ".dmg"
			}
		}
		if strings.Contains(contentType, "application/zip") {
			return ".zip"
		}
		if strings.Contains(contentType, "application/x-pkg") || strings.Contains(contentType, "application/x-installer") {
			return ".pkg"
		}
	}

	// Parse URL to find extension, but be smart about version numbers
	// Remove query string and fragment
	urlPath := url
	if idx := strings.Index(urlPath, "?"); idx != -1 {
		urlPath = urlPath[:idx]
	}
	if idx := strings.Index(urlPath, "#"); idx != -1 {
		urlPath = urlPath[:idx]
	}

	// Look for known installer extensions in the URL
	knownExts := []string{".dmg", ".pkg", ".zip"}
	for _, knownExt := range knownExts {
		if strings.HasSuffix(strings.ToLower(urlPath), knownExt) {
			return knownExt
		}
		// Also check if it appears before a query string or version number
		idx := strings.Index(strings.ToLower(urlPath), knownExt)
		if idx != -1 {
			// Check if there's a version-like pattern after it (e.g., .dmg.1.2.3)
			remaining := urlPath[idx+len(knownExt):]
			if len(remaining) > 0 && (remaining[0] == '.' || remaining[0] == '?' || remaining[0] == '#') {
				return knownExt
			}
		}
	}

	// If no known extension found, try filepath.Ext but filter out version numbers
	ext := filepath.Ext(urlPath)
	// If extension looks like a version number (starts with digit or has multiple dots), ignore it
	if ext != "" {
		extLower := strings.ToLower(ext)
		// Check if it's a known extension
		for _, knownExt := range knownExts {
			if extLower == knownExt {
				return extLower
			}
		}
		// If extension starts with a digit or has multiple parts, it's likely a version number
		if len(ext) > 1 && (ext[1] >= '0' && ext[1] <= '9') {
			// Try to find a real extension before this
			base := strings.TrimSuffix(urlPath, ext)
			realExt := filepath.Ext(base)
			if realExt != "" {
				realExtLower := strings.ToLower(realExt)
				for _, knownExt := range knownExts {
					if realExtLower == knownExt {
						return realExtLower
					}
				}
			}
		}
	}

	return "" // Will default to .dmg
}

func installApp(installerPath string, app securityAppVersionInfo) (string, error) {
	fmt.Printf("  📦 Installing app...\n")

	// First, verify the actual file type (in case it was misnamed)
	actualExt, err := detectActualFileType(installerPath)
	if err == nil && actualExt != "" {
		currentExt := strings.ToLower(filepath.Ext(installerPath))
		if actualExt != currentExt {
			// File type doesn't match extension, rename it
			newPath := strings.TrimSuffix(installerPath, currentExt) + actualExt
			if err := os.Rename(installerPath, newPath); err == nil {
				fmt.Printf("  ℹ️  Corrected file type: %s -> %s\n", currentExt, actualExt)
				installerPath = newPath
			}
		}
	}

	ext := strings.ToLower(filepath.Ext(installerPath))
	var appPath string

	switch ext {
	case ".dmg":
		appPath, err = installFromDMG(installerPath, app)
		// If DMG fails and error suggests it's not a DMG, try as ZIP
		if err != nil && (strings.Contains(err.Error(), "not recognized") || 
		                  strings.Contains(err.Error(), "Zip archive")) {
			fmt.Printf("  ℹ️  DMG mount failed, trying as ZIP...\n")
			// Rename and try as ZIP
			zipPath := strings.TrimSuffix(installerPath, ".dmg") + ".zip"
			if renameErr := os.Rename(installerPath, zipPath); renameErr == nil {
				appPath, err = installFromZIP(zipPath, app)
			}
		}
	case ".pkg":
		// Verify PKG file exists and has content before attempting installation
		info, err := os.Stat(installerPath)
		if err != nil {
			return "", fmt.Errorf("PKG file not found: %s (%w)", installerPath, err)
		}
		if info.Size() == 0 {
			return "", fmt.Errorf("PKG file is empty: %s", installerPath)
		}
		fmt.Printf("  ℹ️  PKG file verified: %s (size: %d bytes)\n", installerPath, info.Size())
		appPath, err = installFromPKG(installerPath, app)
	case ".zip":
		appPath, err = installFromZIP(installerPath, app)
	default:
		return "", fmt.Errorf("unsupported installer type: %s", ext)
	}

	if err != nil {
		return "", err
	}

	// Wait a moment for installation to complete
	time.Sleep(2 * time.Second)

	// Remove quarantine attributes (macOS adds these when downloading files)
	// This is critical for santactl to work properly in CI environments
	if err := removeQuarantineAttributes(appPath); err != nil {
		fmt.Printf("  ⚠️  Warning: Failed to remove quarantine attributes: %v\n", err)
		// Continue anyway - it might still work
	}

	return appPath, nil
}

func installFromDMG(dmgPath string, app securityAppVersionInfo) (string, error) {
	// Verify DMG file exists and is readable
	if info, err := os.Stat(dmgPath); err != nil {
		return "", fmt.Errorf("DMG file not found or not readable: %w", err)
	} else if info.Size() == 0 {
		return "", fmt.Errorf("DMG file is empty (size: 0 bytes)")
	}

	// Check if file is actually a DMG by checking its type (optional check)
	var fileTypeInfo string
	fileCmd := exec.Command("file", dmgPath)
	if fileOutput, err := fileCmd.Output(); err == nil {
		fileType := strings.ToLower(string(fileOutput))
		fileTypeInfo = strings.TrimSpace(string(fileOutput))
		if !strings.Contains(fileType, "disk image") && !strings.Contains(fileType, "dmg") && !strings.Contains(fileType, "udif") {
			// Not a valid DMG, but try anyway (might be a false negative)
			fmt.Printf("  ⚠️  Warning: File type check suggests this may not be a valid DMG: %s\n", fileTypeInfo)
		}
	}

	// Clean up any existing mount point
	mountPoint := filepath.Join(tempDir, "mnt")
	os.RemoveAll(mountPoint)
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return "", fmt.Errorf("failed to create mount point: %w", err)
	}

	// Try mounting with explicit mountpoint (using -noverify like in workflow)
	// First attempt: try with auto-accept EULA by piping "Y"
	cmd := exec.Command("hdiutil", "attach", dmgPath, "-mountpoint", mountPoint, "-nobrowse", "-noverify", "-noautoopen", "-quiet")
	cmd.Stdin = strings.NewReader("Y\n") // Auto-accept EULA if present
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	
	if err != nil {
		// If explicit mountpoint fails, try letting hdiutil choose the mount point (with EULA acceptance)
		cmd2 := exec.Command("hdiutil", "attach", dmgPath, "-nobrowse", "-noverify", "-noautoopen", "-quiet")
		cmd2.Stdin = strings.NewReader("Y\n") // Auto-accept EULA if present
		var stdout2 bytes.Buffer
		var stderr2 bytes.Buffer
		cmd2.Stdout = &stdout2
		cmd2.Stderr = &stderr2
		err2 := cmd2.Run()
		
		if err2 != nil {
			// Both methods failed, try one more time without -quiet to get actual error (with EULA acceptance)
			cmd3 := exec.Command("hdiutil", "attach", dmgPath, "-nobrowse", "-noverify", "-noautoopen")
			cmd3.Stdin = strings.NewReader("Y\n") // Auto-accept EULA if present
			var stdout3 bytes.Buffer
			var stderr3 bytes.Buffer
			cmd3.Stdout = &stdout3
			cmd3.Stderr = &stderr3
			err3 := cmd3.Run()
			
			// Check if the error is due to EULA (output contains "EULA" or "license" or "agreement")
			output3 := stdout3.String() + stderr3.String()
			if strings.Contains(strings.ToLower(output3), "eula") || strings.Contains(strings.ToLower(output3), "license") || strings.Contains(strings.ToLower(output3), "agreement") || strings.Contains(strings.ToLower(output3), "end-user") {
				// EULA detected, try using shell command to pipe "Y" to hdiutil
				fmt.Printf("  ℹ️  DMG contains EULA, attempting to auto-accept...\n")
				
				// Try with explicit mountpoint first
				shellCmd := fmt.Sprintf("echo 'Y' | hdiutil attach '%s' -mountpoint '%s' -nobrowse -noverify -noautoopen -quiet 2>&1", dmgPath, mountPoint)
				cmd4 := exec.Command("sh", "-c", shellCmd)
				var stdout4 bytes.Buffer
				var stderr4 bytes.Buffer
				cmd4.Stdout = &stdout4
				cmd4.Stderr = &stderr4
				err4 := cmd4.Run()
				
				if err4 != nil {
					// Try without explicit mountpoint
					shellCmd2 := fmt.Sprintf("echo 'Y' | hdiutil attach '%s' -nobrowse -noverify -noautoopen -quiet 2>&1", dmgPath)
					cmd5 := exec.Command("sh", "-c", shellCmd2)
					var stdout5 bytes.Buffer
					var stderr5 bytes.Buffer
					cmd5.Stdout = &stdout5
					cmd5.Stderr = &stderr5
					err5 := cmd5.Run()
					
					if err5 == nil {
						// Success, parse mount point
						output := stdout5.String()
						if output == "" {
							output = stderr5.String()
						}
						lines := strings.Split(output, "\n")
						for _, line := range lines {
							fields := strings.Fields(line)
							if len(fields) >= 2 && strings.HasPrefix(fields[1], "/Volumes/") {
								detectedMount := fields[1]
								// Verify it's not a system volume
								if !strings.Contains(strings.ToLower(detectedMount), "macintosh") &&
								   !strings.Contains(strings.ToLower(detectedMount), "system") &&
								   !strings.Contains(strings.ToLower(detectedMount), "recovery") {
									mountPoint = detectedMount
									break
								}
							}
						}
						// If we still don't have a mount point, try to find recently mounted volumes
						if mountPoint == filepath.Join(tempDir, "mnt") {
							volumes, _ := filepath.Glob("/Volumes/*")
							var latestVolume string
							var latestTime time.Time
							systemVolumes := map[string]bool{
								"/Volumes/Macintosh HD": true,
								"/Volumes/Preboot":      true,
								"/Volumes/Recovery":      true,
								"/Volumes/Update":        true,
								"/Volumes/VM":            true,
							}
							for _, vol := range volumes {
								// Skip system volumes
								if systemVolumes[vol] {
									continue
								}
								// Skip volumes that look like system volumes
								volBase := filepath.Base(vol)
								if strings.Contains(strings.ToLower(volBase), "macintosh") || 
								   strings.Contains(strings.ToLower(volBase), "system") ||
								   strings.Contains(strings.ToLower(volBase), "recovery") {
									continue
								}
								if info, err := os.Stat(vol); err == nil && info.IsDir() {
									if info.ModTime().After(latestTime) {
										latestTime = info.ModTime()
										latestVolume = vol
									}
								}
							}
							if latestVolume != "" {
								mountPoint = latestVolume
								fmt.Printf("  ℹ️  Using auto-detected mount point: %s\n", mountPoint)
							} else {
								return "", fmt.Errorf("failed to mount DMG: could not determine mount point after EULA acceptance")
							}
						}
						// Verify the mount point is actually a DMG mount (not a system volume)
						if strings.Contains(strings.ToLower(mountPoint), "macintosh") {
							return "", fmt.Errorf("failed to mount DMG: detected system volume instead of DMG mount point: %s", mountPoint)
						}
						goto verifyMount
					}
				} else {
					// Method 4 succeeded with explicit mountpoint
					goto verifyMount
				}
			}
			
			// Collect all error messages
			errorMsgs := []string{}
			if stderr.String() != "" {
				errorMsgs = append(errorMsgs, fmt.Sprintf("method1-stderr: %s", strings.TrimSpace(stderr.String())))
			}
			if stdout.String() != "" {
				errorMsgs = append(errorMsgs, fmt.Sprintf("method1-stdout: %s", strings.TrimSpace(stdout.String())))
			}
			if stderr2.String() != "" {
				errorMsgs = append(errorMsgs, fmt.Sprintf("method2-stderr: %s", strings.TrimSpace(stderr2.String())))
			}
			if stdout2.String() != "" {
				errorMsgs = append(errorMsgs, fmt.Sprintf("method2-stdout: %s", strings.TrimSpace(stdout2.String())))
			}
			if stderr3.String() != "" {
				errorMsgs = append(errorMsgs, fmt.Sprintf("method3-stderr: %s", strings.TrimSpace(stderr3.String())))
			}
			if stdout3.String() != "" {
				errorMsgs = append(errorMsgs, fmt.Sprintf("method3-stdout: %s", strings.TrimSpace(stdout3.String())))
			}
			
			errorMsg := "unknown error"
			if len(errorMsgs) > 0 {
				errorMsg = strings.Join(errorMsgs, "; ")
			} else {
				// Last resort: check exit codes
				errorMsg = fmt.Sprintf("hdiutil failed with exit codes: %v, %v, %v", err, err2, err3)
			}
			
			// Also check file type for additional context
			fileInfo := ""
			if fileTypeInfo != "" {
				fileInfo = fmt.Sprintf(" (file type: %s)", fileTypeInfo)
			}
			
			return "", fmt.Errorf("failed to mount DMG: %s%s", errorMsg, fileInfo)
		}
		
		// Method 2 succeeded, parse output to find mount point
		output := stdout2.String()
		if output == "" {
			output = stderr2.String() // Sometimes hdiutil outputs to stderr
		}
		// Parse output to find mount point
		// hdiutil attach output format: /dev/diskXsY	/Volumes/MountName
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.HasPrefix(fields[1], "/Volumes/") {
				mountPoint = fields[1]
				break
			}
		}
		// If we still don't have a mount point, try to find recently mounted volumes
		if mountPoint == filepath.Join(tempDir, "mnt") {
			// List volumes and find the one that matches
			volumes, _ := filepath.Glob("/Volumes/*")
			// Use the most recently modified volume as a fallback, but exclude system volumes
			var latestVolume string
			var latestTime time.Time
			systemVolumes := map[string]bool{
				"/Volumes/Macintosh HD": true,
				"/Volumes/Preboot":      true,
				"/Volumes/Recovery":      true,
				"/Volumes/Update":        true,
				"/Volumes/VM":            true,
			}
			for _, vol := range volumes {
				// Skip system volumes
				if systemVolumes[vol] {
					continue
				}
				// Skip volumes that look like system volumes (contain "Macintosh" or are common system names)
				volBase := filepath.Base(vol)
				if strings.Contains(strings.ToLower(volBase), "macintosh") || 
				   strings.Contains(strings.ToLower(volBase), "system") ||
				   strings.Contains(strings.ToLower(volBase), "recovery") {
					continue
				}
				if info, err := os.Stat(vol); err == nil && info.IsDir() {
					if info.ModTime().After(latestTime) {
						latestTime = info.ModTime()
						latestVolume = vol
					}
				}
			}
			if latestVolume != "" {
				mountPoint = latestVolume
				fmt.Printf("  ℹ️  Using auto-detected mount point: %s\n", mountPoint)
			} else {
				return "", fmt.Errorf("failed to mount DMG: could not determine mount point")
			}
		}
	} else {
		// Method 1 succeeded, check if mount point is valid
		if _, err := os.Stat(mountPoint); err != nil {
			// Mount succeeded but mount point doesn't exist, try parsing stdout
			output := stdout.String()
			if output == "" {
				output = stderr.String()
			}
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				fields := strings.Fields(line)
				if len(fields) >= 2 && strings.HasPrefix(fields[1], "/Volumes/") {
					mountPoint = fields[1]
					break
				}
			}
		}
	}

verifyMount:
	// Verify mount point exists and is accessible
	if _, err := os.Stat(mountPoint); err != nil {
		return "", fmt.Errorf("failed to mount DMG: mount point not accessible: %s", mountPoint)
	}

	defer func() {
		// Detach using the actual mount point
		exec.Command("hdiutil", "detach", mountPoint, "-quiet", "-force").Run()
	}()

	// First, check if DMG contains a .pkg file (some DMGs contain installers, not apps)
	// Skip PKGs that are inside .app bundles (those are not installers)
	var pkgFile string
	_ = filepath.Walk(mountPoint, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".pkg") && info != nil && !info.IsDir() {
			// Skip PKGs that are inside .app bundles (e.g., CloudConfig.pkg inside VNC Viewer.app)
			pathLower := strings.ToLower(path)
			if strings.Contains(pathLower, ".app/") {
				return nil // Skip PKGs inside app bundles
			}
			// Verify it's actually a file and exists
			if stat, err := os.Stat(path); err == nil && stat.Mode().IsRegular() {
				pkgFile = path
				return filepath.SkipDir
			}
		}
		return nil
	})

	// If we found a PKG, verify it exists and install it
	if pkgFile != "" {
		// Verify PKG file exists and is readable
		if _, err := os.Stat(pkgFile); err != nil {
			fmt.Printf("  ⚠️  Warning: PKG file not found or not accessible: %s, trying to find .app bundle instead\n", pkgFile)
			pkgFile = "" // Clear it so we look for .app bundle instead
		} else {
			fmt.Printf("  📦 Found PKG installer in DMG, installing...\n")
			// Install the PKG with -allowUntrusted and -verbose for better error reporting
			installCmd := exec.Command("sudo", "installer", "-pkg", pkgFile, "-target", "/", "-allowUntrusted", "-verbose")
			var installStderr bytes.Buffer
			var installStdout bytes.Buffer
			installCmd.Stderr = &installStderr
			installCmd.Stdout = &installStdout
				if err := installCmd.Run(); err != nil {
				stderrStr := strings.TrimSpace(installStderr.String())
				stdoutStr := strings.TrimSpace(installStdout.String())
				errorDetails := []string{}
				if stderrStr != "" {
					errorDetails = append(errorDetails, fmt.Sprintf("stderr: %s", stderrStr))
				}
				if stdoutStr != "" {
					errorDetails = append(errorDetails, fmt.Sprintf("stdout: %s", stdoutStr))
				}
				errorMsg := strings.Join(errorDetails, "; ")
				if errorMsg != "" {
					return "", fmt.Errorf("failed to install PKG from DMG: %w (%s)", err, errorMsg)
				}
				return "", fmt.Errorf("failed to install PKG from DMG: %w", err)
			}

			// Wait for installation to complete
			time.Sleep(3 * time.Second)

			// Now find the installed app in /Applications
			return findInstalledApp(app)
		}
	}

	// Otherwise, look for .app bundle in mounted DMG - try multiple strategies
	var appBundle string

	// Strategy 1: Look for .app bundle by walking the directory tree
	_ = filepath.Walk(mountPoint, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Continue walking even if we hit permission errors
			return nil
		}
		// Verify path is within mount point (prevent following symlinks outside)
		if relPath, err := filepath.Rel(mountPoint, path); err != nil || strings.HasPrefix(relPath, "..") {
			return filepath.SkipDir // Path is outside mount point, skip it
		}
		// Check if this is a .app bundle (directory ending in .app)
		if strings.HasSuffix(path, ".app") {
			// Verify it's actually a directory (app bundles are directories)
			if info != nil && info.IsDir() {
				appBundle = path
				return filepath.SkipDir // Found it, stop searching
			}
		}
		return nil
	})

	// Strategy 2: If not found, try looking for common app names
	if appBundle == "" {
		commonNames := []string{
			app.Name + ".app",
			strings.ReplaceAll(app.Name, " ", "") + ".app",
			strings.ReplaceAll(app.Name, " ", "_") + ".app",
		}

		for _, name := range commonNames {
			candidate := filepath.Join(mountPoint, name)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				appBundle = candidate
				break
			}
		}
	}

	// Strategy 3: Look in common subdirectories (some DMGs have apps in subfolders)
	if appBundle == "" {
		commonDirs := []string{"Applications", "Contents", "Install"}
		for _, dir := range commonDirs {
			searchPath := filepath.Join(mountPoint, dir)
			if _, err := os.Stat(searchPath); err == nil {
				_ = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return nil
					}
					// Verify path is within mount point (prevent following symlinks outside)
					if relPath, err := filepath.Rel(mountPoint, path); err != nil || strings.HasPrefix(relPath, "..") {
						return filepath.SkipDir // Path is outside mount point, skip it
					}
					if strings.HasSuffix(path, ".app") && info != nil && info.IsDir() {
						appBundle = path
						return filepath.SkipDir
					}
					return nil
				})
				if appBundle != "" {
					break
				}
			}
		}
	}

	if appBundle == "" {
		// List contents for debugging
		var contents []string
		filepath.Walk(mountPoint, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil {
				relPath, _ := filepath.Rel(mountPoint, path)
				if relPath != "." {
					contents = append(contents, relPath)
				}
			}
			return nil
		})
		return "", fmt.Errorf("could not find .app bundle or .pkg installer in DMG. Contents: %v", contents[:min(10, len(contents))])
	}

	// Verify app bundle is within the mount point (safety check to prevent following symlinks outside)
	if relPath, err := filepath.Rel(mountPoint, appBundle); err != nil || strings.HasPrefix(relPath, "..") {
		return "", fmt.Errorf("app bundle path %s is outside mount point %s (possible symlink issue)", appBundle, mountPoint)
	}

	// Copy .app to Applications
	appName := filepath.Base(appBundle)
	destPath := filepath.Join(applicationsDir, appName)

	// Verify source exists
	if _, err := os.Stat(appBundle); err != nil {
		return "", fmt.Errorf("app bundle not found at %s: %w", appBundle, err)
	}

	// Verify source bundle structure is valid (check for required bundle components)
	infoPlistPath := filepath.Join(appBundle, "Contents", "Info.plist")
	if _, err := os.Stat(infoPlistPath); err != nil {
		return "", fmt.Errorf("source app bundle appears invalid (missing Info.plist): %s", appBundle)
	}

	// Verify source bundle with codesign before copying
	verifyCmd := exec.Command("codesign", "-dv", appBundle)
	var verifyStderr bytes.Buffer
	verifyCmd.Stderr = &verifyStderr
	if err := verifyCmd.Run(); err != nil {
		verifyOutput := strings.TrimSpace(verifyStderr.String())
		// If it says "bundle format unrecognized", the source is already corrupted
		if strings.Contains(verifyOutput, "bundle format unrecognized") {
			return "", fmt.Errorf("source app bundle is corrupted on DMG mount point: %s (codesign: %s)", appBundle, verifyOutput)
		}
		// Other codesign errors are OK (unsigned apps, etc.), but log them
		fmt.Printf("  ℹ️  Source bundle codesign check: %s\n", verifyOutput)
	}

	// Remove existing app if present (use more thorough cleanup)
	os.RemoveAll(destPath)
	// Wait a moment for filesystem to sync
	time.Sleep(500 * time.Millisecond)

	// Use ditto to copy app bundle (preserves resource forks, extended attributes, symlinks, and bundle structure)
	// ditto is specifically designed for copying macOS app bundles correctly
	cmd = exec.Command("ditto", appBundle, destPath)
	var dittoStderr bytes.Buffer
	var dittoStdout bytes.Buffer
	cmd.Stderr = &dittoStderr
	cmd.Stdout = &dittoStdout
	if err := cmd.Run(); err != nil {
		// If ditto fails, try using Go's file operations as fallback
		fmt.Printf("  ⚠️  Warning: ditto command failed: %v, trying alternative copy method...\n", strings.TrimSpace(dittoStderr.String()))
		
		// Use filepath.Walk to copy directory tree
		if err := copyDirectory(appBundle, destPath); err != nil {
			return "", fmt.Errorf("failed to copy app (ditto failed: %s, fallback failed: %w)", strings.TrimSpace(dittoStderr.String()), err)
		}
	}

	// Verify copy succeeded and bundle structure is intact
	if _, err := os.Stat(destPath); err != nil {
		return "", fmt.Errorf("copy appeared to succeed but destination not found: %w", err)
	}

	// Verify destination bundle structure
	destInfoPlistPath := filepath.Join(destPath, "Contents", "Info.plist")
	if _, err := os.Stat(destInfoPlistPath); err != nil {
		return "", fmt.Errorf("copied app bundle appears invalid (missing Info.plist): %s", destPath)
	}

	// Verify destination bundle with codesign
	destVerifyCmd := exec.Command("codesign", "-dv", destPath)
	var destVerifyStderr bytes.Buffer
	destVerifyCmd.Stderr = &destVerifyStderr
	if err := destVerifyCmd.Run(); err != nil {
		verifyOutput := strings.TrimSpace(destVerifyStderr.String())
		// If it says "bundle format unrecognized", the copy corrupted the bundle
		if strings.Contains(verifyOutput, "bundle format unrecognized") {
			return "", fmt.Errorf("copied app bundle is corrupted: %s (codesign: %s). Source may be corrupted or copy failed.", destPath, verifyOutput)
		}
		// Other codesign errors are OK (unsigned apps, etc.)
		fmt.Printf("  ℹ️  Destination bundle codesign check: %s\n", verifyOutput)
	}

	return destPath, nil
}

func findInstalledApp(app securityAppVersionInfo) (string, error) {
	// Wait a bit longer for installation to fully complete
	time.Sleep(2 * time.Second)

	// Try to find the installed app by name variations
	variations := []string{
		app.Name + ".app",
		strings.ReplaceAll(app.Name, " ", "") + ".app",
		strings.ReplaceAll(app.Name, " ", "_") + ".app",
		strings.ReplaceAll(app.Name, " ", "-") + ".app",
		// Adobe-specific variations
		strings.TrimSuffix(app.Name, " DC") + ".app",
		strings.TrimSuffix(app.Name, " Pro DC") + ".app",
		strings.TrimSuffix(app.Name, " Pro") + ".app",
		// Remove common suffixes
		strings.TrimSuffix(app.Name, " Desktop") + ".app",
		strings.TrimSuffix(app.Name, " Suite") + ".app",
		strings.TrimSuffix(app.Name, " Viewer") + ".app",
	}

	// For multi-word names, try just the first word (e.g., "Box Drive" -> "Box.app")
	nameWords := strings.Fields(app.Name)
	if len(nameWords) > 1 {
		variations = append(variations, nameWords[0]+".app")
		// Also try first two words
		if len(nameWords) > 2 {
			variations = append(variations, strings.Join(nameWords[:2], " ")+".app")
			variations = append(variations, strings.Join(nameWords[:2], "")+".app")
		}
		// Try last word (e.g., "Podman Desktop" -> "Desktop.app" is unlikely, but try it)
		// Actually skip this, it's too generic
	}

	for _, variation := range variations {
		appPath := filepath.Join(applicationsDir, variation)
		if _, err := os.Stat(appPath); err == nil {
			return appPath, nil
		}
	}

	// If not found by name, search for any .app that might match
	var foundApp string
	var bestMatch string
	bestMatchScore := 0

	_ = filepath.Walk(applicationsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Look for apps that contain key words from the app name
		if strings.HasSuffix(path, ".app") && info != nil && info.IsDir() {
			appBaseName := strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".app"))
			nameLower := strings.ToLower(app.Name)

			// Exact match (case-insensitive)
			if appBaseName == nameLower {
				foundApp = path
				return filepath.SkipDir
			}

			// Check if app name contains key words
			keyWords := strings.Fields(nameLower)
			matchScore := 0
			for _, word := range keyWords {
				if len(word) > 2 { // Lower threshold to catch "Box"
					if strings.Contains(appBaseName, word) {
						matchScore++
					}
				}
			}

			// Also check if the app name starts with any key word
			if len(keyWords) > 0 && strings.HasPrefix(appBaseName, strings.ToLower(keyWords[0])) {
				matchScore += 2 // Higher score for prefix match
			}

			if matchScore > bestMatchScore {
				bestMatch = path
				bestMatchScore = matchScore
			}
		}
		return nil
	})

	if foundApp != "" {
		return foundApp, nil
	}

	if bestMatch != "" && bestMatchScore > 0 {
		fmt.Printf("  ℹ️  Found app by keyword matching: %s (score: %d)\n", bestMatch, bestMatchScore)
		return bestMatch, nil
	}

	// Last resort: check recently modified apps (within last 5 minutes)
	// This helps catch apps that were just installed but have unexpected names
	var recentApps []string
	cutoffTime := time.Now().Add(-5 * time.Minute)
	_ = filepath.Walk(applicationsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(path, ".app") && info != nil && info.IsDir() {
			// Check if modified in last 5 minutes
			if info.ModTime().After(cutoffTime) {
				recentApps = append(recentApps, path) // Store full path, not just name
			}
		}
		return nil
	})

	if len(recentApps) > 0 {
		// Filter out helper apps and look for the main app
		var mainApps []string
		for _, appPath := range recentApps {
			appName := filepath.Base(appPath)
			appLower := strings.ToLower(appName)
			// Skip helper apps, code helpers, etc.
			if strings.Contains(appLower, "helper") || 
			   strings.Contains(appLower, "plugin") || 
			   strings.Contains(appLower, "renderer") ||
			   strings.Contains(appLower, "gpu") {
				continue
			}
			mainApps = append(mainApps, appPath)
		}
		
		// If we have main apps, try them
		if len(mainApps) > 0 {
			for _, appPath := range mainApps {
				if _, err := os.Stat(appPath); err == nil {
					// Check if it matches the app name we're looking for
					appName := filepath.Base(appPath)
					appNameLower := strings.ToLower(strings.TrimSuffix(appName, ".app"))
					searchNameLower := strings.ToLower(app.Name)
					if strings.Contains(appNameLower, searchNameLower) || 
					   strings.Contains(searchNameLower, appNameLower) ||
					   len(mainApps) == 1 {
						fmt.Printf("  ℹ️  Using recently modified app: %s\n", appName)
						return appPath, nil
					}
				}
			}
		}
		
		// If we found recently modified apps but they're command-line tools (not GUI apps),
		// try to use the first one if it's the only option
		if len(recentApps) == 1 || (len(recentApps) == 2 && 
			(strings.Contains(strings.ToLower(recentApps[0]), "tctl") || 
			 strings.Contains(strings.ToLower(recentApps[0]), "tsh"))) {
			// Try using the first recently modified app
			appPath := filepath.Join(applicationsDir, recentApps[0])
			if _, err := os.Stat(appPath); err == nil {
				fmt.Printf("  ℹ️  Using recently modified app (may be command-line tool): %s\n", recentApps[0])
				return appPath, nil
			}
		}
		return "", fmt.Errorf("could not find installed app after PKG installation. Recently modified apps: %v", recentApps[:min(5, len(recentApps))])
	}

	return "", fmt.Errorf("could not find installed app after PKG installation")
}

// copyDirectory recursively copies a directory tree from src to dst
func copyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from source
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		// Construct destination path
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			// Create directory
			return os.MkdirAll(dstPath, info.Mode())
		} else {
			// Create parent directory if needed
			if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
				return err
			}

			// Copy file
			srcFile, err := os.Open(path)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
			if err != nil {
				return err
			}
			defer dstFile.Close()

			_, err = io.Copy(dstFile, srcFile)
			return err
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func installFromPKG(pkgPath string, app securityAppVersionInfo) (string, error) {
	// Verify PKG file exists and is readable
	if _, err := os.Stat(pkgPath); err != nil {
		return "", fmt.Errorf("PKG file not found or not accessible: %s (%w)", pkgPath, err)
	}
	
	// Install PKG with -allowUntrusted and -verbose for better error reporting
	cmd := exec.Command("sudo", "installer", "-pkg", pkgPath, "-target", "/", "-allowUntrusted", "-verbose")
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		stdoutStr := strings.TrimSpace(stdout.String())
		errorDetails := []string{}
		if stderrStr != "" {
			errorDetails = append(errorDetails, fmt.Sprintf("stderr: %s", stderrStr))
		}
		if stdoutStr != "" {
			errorDetails = append(errorDetails, fmt.Sprintf("stdout: %s", stdoutStr))
		}
		errorMsg := strings.Join(errorDetails, "; ")
		if errorMsg != "" {
			return "", fmt.Errorf("failed to install PKG: %w (%s)", err, errorMsg)
		}
		return "", fmt.Errorf("failed to install PKG: %w", err)
	}

	// Wait for installation to complete
	time.Sleep(3 * time.Second)

	// Find the installed app
	appPath, err := findInstalledApp(app)
	if err != nil {
		// If we can't find the app, list what was recently installed for debugging
		fmt.Printf("  ⚠️  Could not find installed app, listing recently modified apps in /Applications:\n")
		var recentApps []string
		cutoffTime := time.Now().Add(-5 * time.Minute)
		_ = filepath.Walk(applicationsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if strings.HasSuffix(path, ".app") && info != nil && info.IsDir() {
				if info.ModTime().After(cutoffTime) {
					recentApps = append(recentApps, filepath.Base(path))
				}
			}
			return nil
		})
		if len(recentApps) > 0 {
			fmt.Printf("  ℹ️  Recently modified apps: %v\n", recentApps)
		} else {
			fmt.Printf("  ℹ️  No recently modified apps found\n")
		}
		return "", err
	}
	return appPath, nil
}

func installFromZIP(zipPath string, app securityAppVersionInfo) (string, error) {
	// Extract ZIP using ditto (preserves resource forks, extended attributes, symlinks, and macOS bundle structure)
	// ditto -xk means: -x = extract, -k = source is a ZIP archive
	extractDir := filepath.Join(tempDir, "extracted")
	os.RemoveAll(extractDir) // Clean up any previous extraction
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", err
	}

	cmd := exec.Command("ditto", "-xk", zipPath, extractDir)
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		errorMsg := strings.TrimSpace(stderr.String())
		if errorMsg == "" {
			errorMsg = strings.TrimSpace(stdout.String())
		}
		if errorMsg == "" {
			errorMsg = "unknown error"
		}
		return "", fmt.Errorf("failed to extract ZIP with ditto: %s (%w)", errorMsg, err)
	}

	// First, check if ZIP contains a .pkg file (some ZIPs contain installers, not apps)
	var pkgFile string
	_ = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".pkg") && info != nil && !info.IsDir() {
			// Skip PKGs that are inside .app bundles (e.g., CloudConfig.pkg inside VNC Viewer.app)
			pathLower := strings.ToLower(path)
			if strings.Contains(pathLower, ".app/") {
				return nil // Skip PKGs inside app bundles
			}
			// Verify it's actually a file and exists
			if stat, err := os.Stat(path); err == nil && stat.Mode().IsRegular() {
				pkgFile = path
				return filepath.SkipDir
			}
		}
		return nil
	})

	// If we found a PKG, verify it exists and install it
	if pkgFile != "" {
		// Verify PKG file exists and is readable
		if _, err := os.Stat(pkgFile); err != nil {
			fmt.Printf("  ⚠️  Warning: PKG file not found or not accessible: %s, trying to find .app bundle instead\n", pkgFile)
			pkgFile = "" // Clear it so we look for .app bundle instead
		} else {
			fmt.Printf("  📦 Found PKG installer in ZIP, installing...\n")
			// Install the PKG with -allowUntrusted and -verbose for better error reporting
			installCmd := exec.Command("sudo", "installer", "-pkg", pkgFile, "-target", "/", "-allowUntrusted", "-verbose")
			var installStderr bytes.Buffer
			var installStdout bytes.Buffer
			installCmd.Stderr = &installStderr
			installCmd.Stdout = &installStdout
			if err := installCmd.Run(); err != nil {
				stderrStr := strings.TrimSpace(installStderr.String())
				stdoutStr := strings.TrimSpace(installStdout.String())
				errorDetails := []string{}
				if stderrStr != "" {
					errorDetails = append(errorDetails, fmt.Sprintf("stderr: %s", stderrStr))
				}
				if stdoutStr != "" {
					errorDetails = append(errorDetails, fmt.Sprintf("stdout: %s", stdoutStr))
				}
				errorMsg := strings.Join(errorDetails, "; ")
				if errorMsg != "" {
					return "", fmt.Errorf("failed to install PKG from ZIP: %w (%s)", err, errorMsg)
				}
				return "", fmt.Errorf("failed to install PKG from ZIP: %w", err)
			}

			// Wait for installation to complete
			time.Sleep(3 * time.Second)

			// Now find the installed app in /Applications
			return findInstalledApp(app)
		}
	}

	// Otherwise, look for .app bundle in extracted ZIP - try multiple strategies
	var appBundle string

	// Strategy 1: Look for .app bundle by walking the directory tree
	_ = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Continue walking even if we hit permission errors
			return nil
		}
		// Check if this is a .app bundle (directory ending in .app)
		if strings.HasSuffix(path, ".app") {
			// Verify it's actually a directory (app bundles are directories)
			if info != nil && info.IsDir() {
				appBundle = path
				return filepath.SkipDir // Found it, stop searching
			}
		}
		return nil
	})

	// Strategy 2: If not found, try looking for common app names
	if appBundle == "" {
		commonNames := []string{
			app.Name + ".app",
			strings.ReplaceAll(app.Name, " ", "") + ".app",
			strings.ReplaceAll(app.Name, " ", "_") + ".app",
			strings.ReplaceAll(app.Name, " ", "-") + ".app",
		}

		// Also try first word of multi-word names
		nameParts := strings.Fields(app.Name)
		if len(nameParts) > 1 {
			commonNames = append(commonNames, nameParts[0]+".app")
		}

		for _, name := range commonNames {
			candidate := filepath.Join(extractDir, name)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				appBundle = candidate
				break
			}
		}
	}

	// Strategy 3: Look in common subdirectories (some ZIPs have apps in subfolders)
	if appBundle == "" {
		commonDirs := []string{"Applications", "Contents", "Install", "Installers"}
		for _, dir := range commonDirs {
			searchPath := filepath.Join(extractDir, dir)
			if _, err := os.Stat(searchPath); err == nil {
				_ = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return nil
					}
					if strings.HasSuffix(path, ".app") && info != nil && info.IsDir() {
						appBundle = path
						return filepath.SkipDir
					}
					return nil
				})
				if appBundle != "" {
					break
				}
			}
		}
	}

	if appBundle == "" {
		// List contents for debugging
		var contents []string
		filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil {
				relPath, _ := filepath.Rel(extractDir, path)
				if relPath != "." && relPath != "" {
					contents = append(contents, relPath)
				}
			}
			return nil
		})
		maxContents := min(20, len(contents))
		return "", fmt.Errorf("could not find .app bundle or .pkg installer in ZIP. Contents: %v", contents[:maxContents])
	}

	// Copy .app to Applications
	appName := filepath.Base(appBundle)
	destPath := filepath.Join(applicationsDir, appName)

	// Verify source exists
	if _, err := os.Stat(appBundle); err != nil {
		return "", fmt.Errorf("app bundle not found at %s: %w", appBundle, err)
	}

	// Verify source bundle structure is valid (check for required bundle components)
	infoPlistPath := filepath.Join(appBundle, "Contents", "Info.plist")
	if _, err := os.Stat(infoPlistPath); err != nil {
		return "", fmt.Errorf("source app bundle appears invalid (missing Info.plist): %s", appBundle)
	}

	// Verify source bundle with codesign before copying
	verifyCmd := exec.Command("codesign", "-dv", appBundle)
	var verifyStderr bytes.Buffer
	verifyCmd.Stderr = &verifyStderr
	if err := verifyCmd.Run(); err != nil {
		verifyOutput := strings.TrimSpace(verifyStderr.String())
		// If it says "bundle format unrecognized", the source is already corrupted
		if strings.Contains(verifyOutput, "bundle format unrecognized") {
			return "", fmt.Errorf("source app bundle is corrupted on DMG mount point: %s (codesign: %s)", appBundle, verifyOutput)
		}
		// Other codesign errors are OK (unsigned apps, etc.), but log them
		fmt.Printf("  ℹ️  Source bundle codesign check: %s\n", verifyOutput)
	}

	// Remove existing app if present (use more thorough cleanup)
	os.RemoveAll(destPath)
	// Wait a moment for filesystem to sync
	time.Sleep(500 * time.Millisecond)

	// Use ditto to copy app bundle (preserves resource forks, extended attributes, symlinks, and bundle structure)
	// ditto is specifically designed for copying macOS app bundles correctly
	cmd = exec.Command("ditto", appBundle, destPath)
	var dittoStderr bytes.Buffer
	var dittoStdout bytes.Buffer
	cmd.Stderr = &dittoStderr
	cmd.Stdout = &dittoStdout
	if err := cmd.Run(); err != nil {
		// If ditto fails, try using Go's file operations as fallback
		fmt.Printf("  ⚠️  Warning: ditto command failed: %v, trying alternative copy method...\n", strings.TrimSpace(dittoStderr.String()))
		
		// Use filepath.Walk to copy directory tree
		if err := copyDirectory(appBundle, destPath); err != nil {
			return "", fmt.Errorf("failed to copy app (ditto failed: %s, fallback failed: %w)", strings.TrimSpace(dittoStderr.String()), err)
		}
	}

	// Verify copy succeeded and bundle structure is intact
	if _, err := os.Stat(destPath); err != nil {
		return "", fmt.Errorf("copy appeared to succeed but destination not found: %w", err)
	}

	// Verify destination bundle structure
	destInfoPlistPath := filepath.Join(destPath, "Contents", "Info.plist")
	if _, err := os.Stat(destInfoPlistPath); err != nil {
		return "", fmt.Errorf("copied app bundle appears invalid (missing Info.plist): %s", destPath)
	}

	// Verify destination bundle with codesign
	destVerifyCmd := exec.Command("codesign", "-dv", destPath)
	var destVerifyStderr bytes.Buffer
	destVerifyCmd.Stderr = &destVerifyStderr
	if err := destVerifyCmd.Run(); err != nil {
		verifyOutput := strings.TrimSpace(destVerifyStderr.String())
		// If it says "bundle format unrecognized", the copy corrupted the bundle
		if strings.Contains(verifyOutput, "bundle format unrecognized") {
			return "", fmt.Errorf("copied app bundle is corrupted: %s (codesign: %s). Source may be corrupted or copy failed.", destPath, verifyOutput)
		}
		// Other codesign errors are OK (unsigned apps, etc.)
		fmt.Printf("  ℹ️  Destination bundle codesign check: %s\n", verifyOutput)
	}

	return destPath, nil
}

// removeQuarantineAttributes removes macOS quarantine extended attributes from an app
// This is critical for santactl to work properly in CI environments where files
// are downloaded via http.Get() and may have quarantine flags set
func removeQuarantineAttributes(appPath string) error {
	// Remove quarantine attribute recursively for .app bundles
	if strings.HasSuffix(appPath, ".app") {
		cmd := exec.Command("xattr", "-dr", "com.apple.quarantine", appPath)
		if err := cmd.Run(); err != nil {
			// If recursive removal fails, try non-recursive
			cmd = exec.Command("xattr", "-d", "com.apple.quarantine", appPath)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to remove quarantine: %w", err)
			}
		}
	} else {
		// For executables, just remove from the file itself
		cmd := exec.Command("xattr", "-d", "com.apple.quarantine", appPath)
		if err := cmd.Run(); err != nil {
			// Ignore errors if attribute doesn't exist
			return nil
		}
	}
	return nil
}

func runSantactl(appPath string) ([]byte, error) {
	fmt.Printf("  🔍 Running santactl fileinfo...\n")

	// If appPath is a .app bundle, try to find the executable inside
	targetPath := appPath
	if strings.HasSuffix(appPath, ".app") {
		// Check if it's a directory (app bundle)
		if info, err := os.Stat(appPath); err == nil && info.IsDir() {
			// Try to find the executable in Contents/MacOS/
			// First, try to read Info.plist to get the executable name
			infoPlistPath := filepath.Join(appPath, "Contents", "Info.plist")
			executableName := ""
			if data, err := os.ReadFile(infoPlistPath); err == nil {
				// Simple parsing to find CFBundleExecutable
				content := string(data)
				if idx := strings.Index(content, "<key>CFBundleExecutable</key>"); idx != -1 {
					// Find the next <string> tag
					if strIdx := strings.Index(content[idx:], "<string>"); strIdx != -1 {
						start := idx + strIdx + 8
						if endIdx := strings.Index(content[start:], "</string>"); endIdx != -1 {
							executableName = strings.TrimSpace(content[start : start+endIdx])
						}
					}
				}
			}
			
			// If we found the executable name, use it; otherwise try common names
			if executableName != "" {
				executablePath := filepath.Join(appPath, "Contents", "MacOS", executableName)
				if _, err := os.Stat(executablePath); err == nil && !strings.HasPrefix(executableName, "._") {
					targetPath = executablePath
				}
			} else {
				// Try common executable names
				appName := strings.TrimSuffix(filepath.Base(appPath), ".app")
				commonNames := []string{appName}
				for _, name := range commonNames {
					executablePath := filepath.Join(appPath, "Contents", "MacOS", name)
					if _, err := os.Stat(executablePath); err == nil && !strings.HasPrefix(name, "._") {
						targetPath = executablePath
						break
					}
				}
			}
			
			// If we still don't have an executable, try listing Contents/MacOS/
			if targetPath == appPath {
				macosDir := filepath.Join(appPath, "Contents", "MacOS")
				if entries, err := os.ReadDir(macosDir); err == nil {
					// Find the first non-resource-fork file (skip files starting with ._)
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), "._") {
							continue // Skip macOS resource fork files
						}
						executablePath := filepath.Join(macosDir, entry.Name())
						if info, err := os.Stat(executablePath); err == nil && !info.IsDir() {
							targetPath = executablePath
							break
						}
					}
				}
			}
		}
	}

	// Verify the app/executable exists before running santactl
	if _, err := os.Stat(targetPath); err != nil {
		// If executable doesn't exist, try .app bundle path
		if targetPath != appPath && strings.HasSuffix(appPath, ".app") {
			if _, err := os.Stat(appPath); err == nil {
				targetPath = appPath
				fmt.Printf("  ℹ️  Executable not found, using .app bundle path: %s\n", appPath)
			}
		}
	}
	
	// Verify target exists
	if _, err := os.Stat(targetPath); err != nil {
		return nil, fmt.Errorf("target path does not exist: %s", targetPath)
	}

	// Remove quarantine from target path if it's different from app path
	// (e.g., if we're using the executable path instead of .app bundle)
	if targetPath != appPath {
		if err := removeQuarantineAttributes(targetPath); err != nil {
			fmt.Printf("  ⚠️  Warning: Failed to remove quarantine from target path: %v\n", err)
		}
	}

	// Add diagnostics before running santactl
	fmt.Printf("  🔍 Diagnostics for %s:\n", targetPath)
	
	// Check extended attributes (quarantine flags)
	xattrCmd := exec.Command("xattr", "-l", targetPath)
	xattrOut, _ := xattrCmd.CombinedOutput()
	xattrStr := strings.TrimSpace(string(xattrOut))
	if xattrStr != "" {
		fmt.Printf("  xattr: %s\n", xattrStr)
	} else {
		fmt.Printf("  xattr: (none)\n")
	}
	
	// Check codesign directly
	codesignCmd := exec.Command("codesign", "-dv", "--verbose=2", targetPath)
	codesignOut, _ := codesignCmd.CombinedOutput()
	codesignStr := strings.TrimSpace(string(codesignOut))
	if codesignStr != "" {
		// Only show first few lines to avoid too much output
		lines := strings.Split(codesignStr, "\n")
		if len(lines) > 5 {
			codesignStr = strings.Join(lines[:5], "\n") + "..."
		}
		fmt.Printf("  codesign: %s\n", codesignStr)
	} else {
		fmt.Printf("  codesign: (no output)\n")
	}
	
	// Check file type (for executables)
	if !strings.HasSuffix(targetPath, ".app") {
		fileCmd := exec.Command("file", targetPath)
		fileOut, _ := fileCmd.CombinedOutput()
		fileStr := strings.TrimSpace(string(fileOut))
		if fileStr != "" {
			fmt.Printf("  file: %s\n", fileStr)
		}
	}

	// Add a delay to ensure app is fully installed (santactl can take 5-10 seconds)
	// Also try to "touch" the app to ensure it's accessible and registered with the system
	if strings.HasSuffix(targetPath, ".app") {
		// Try to access the app bundle to ensure it's ready
		exec.Command("ls", "-la", targetPath).Run()
		
		// Try to find and access the executable inside to "wake it up"
		macosDir := filepath.Join(targetPath, "Contents", "MacOS")
		if entries, err := os.ReadDir(macosDir); err == nil {
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), "._") && !entry.IsDir() {
					execPath := filepath.Join(macosDir, entry.Name())
					exec.Command("ls", "-la", execPath).Run()
					// Try running codesign to verify the app is signed (this might help santactl)
					exec.Command("codesign", "-dv", execPath).Run()
					break
				}
			}
		}
		
		// Try to assess the app with spctl (this might help register it with santactl)
		exec.Command("spctl", "-a", "-vv", targetPath).Run()
	}
	time.Sleep(5 * time.Second) // Increased wait time since santactl can take 5-10 seconds

	// Try .app bundle path first (this is what works locally per user feedback)
	// Retry up to 3 times with increasing delays if we get empty results
	maxRetries := 3
	var output []byte
	var err error
	
	// Determine which path to try first
	tryAppPath := strings.HasSuffix(appPath, ".app")
	pathsToTry := []string{}
	if tryAppPath && targetPath != appPath {
		pathsToTry = append(pathsToTry, appPath)
	}
	pathsToTry = append(pathsToTry, targetPath)
	
	for attempt := 1; attempt <= maxRetries; attempt++ {
		for pathIdx, pathToTry := range pathsToTry {
			if attempt == 1 && pathIdx == 0 {
				fmt.Printf("  ℹ️  Trying .app bundle path: %s\n", pathToTry)
			} else if attempt == 1 {
				fmt.Printf("  ℹ️  Trying path: %s\n", pathToTry)
			} else {
				fmt.Printf("  ℹ️  Retry %d: Trying path: %s\n", attempt, pathToTry)
			}
			
			// If this is an .app bundle and we got empty results before, try codesign first
			if attempt > 1 && strings.HasSuffix(pathToTry, ".app") {
				// Try to find the executable and run codesign on it to "register" it
				macosDir := filepath.Join(pathToTry, "Contents", "MacOS")
				if entries, err := os.ReadDir(macosDir); err == nil {
					for _, entry := range entries {
						if !strings.HasPrefix(entry.Name(), "._") && !entry.IsDir() {
							execPath := filepath.Join(macosDir, entry.Name())
							// Run codesign to verify signature (this might help santactl recognize it)
							exec.Command("codesign", "-dv", execPath).Run()
							time.Sleep(1 * time.Second)
							break
						}
					}
				}
			}
			
			cmd := exec.Command("santactl", "fileinfo", "--json", pathToTry)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err = cmd.Run()
			output = stdout.Bytes()
			
			// Debug: show what we got
			outputStr := strings.TrimSpace(string(output))
			
			if len(outputStr) > 0 && outputStr != "[]" && outputStr != "null" {
				var testArray []interface{}
				if json.Unmarshal(output, &testArray) == nil && len(testArray) > 0 {
					// Got valid data
					fmt.Printf("  ✅ Got data from path (attempt %d)\n", attempt)
					return output, nil
				}
			}
			
			// If we got empty array, try the executable path directly as a fallback
			if outputStr == "[]" && strings.HasSuffix(pathToTry, ".app") && attempt >= 2 {
				// Try finding and using the executable path directly
				macosDir := filepath.Join(pathToTry, "Contents", "MacOS")
				if entries, err := os.ReadDir(macosDir); err == nil {
					for _, entry := range entries {
						if !strings.HasPrefix(entry.Name(), "._") && !entry.IsDir() {
							execPath := filepath.Join(macosDir, entry.Name())
							fmt.Printf("  ℹ️  Trying executable path directly: %s\n", execPath)
							cmd2 := exec.Command("santactl", "fileinfo", "--json", execPath)
							var stdout2 bytes.Buffer
							var stderr2 bytes.Buffer
							cmd2.Stdout = &stdout2
							cmd2.Stderr = &stderr2
							if err2 := cmd2.Run(); err2 == nil {
								output2 := stdout2.Bytes()
								outputStr2 := strings.TrimSpace(string(output2))
								if outputStr2 != "[]" && len(outputStr2) > 0 {
									var testArray2 []interface{}
									if json.Unmarshal(output2, &testArray2) == nil && len(testArray2) > 0 {
										fmt.Printf("  ✅ Got data from executable path\n")
										return output2, nil
									}
								}
							}
							break
						}
					}
				}
			}
			
			// If we got empty array, try text format as fallback
			if outputStr == "[]" {
				fmt.Printf("  ℹ️  JSON returned empty, trying text format (may take 10+ seconds)...\n")
				// Try without --json flag (this can take 10+ seconds)
				cmdText := exec.Command("santactl", "fileinfo", pathToTry)
				var stdoutText bytes.Buffer
				var stderrText bytes.Buffer
				cmdText.Stdout = &stdoutText
				cmdText.Stderr = &stderrText
				// Set a longer timeout context for text format (15 seconds)
				if errText := cmdText.Run(); errText == nil {
					textOutput := stdoutText.Bytes()
					if len(textOutput) > 0 {
						// Parse text output and convert to JSON-like structure
						parsedData, parseErr := parseSantactlTextOutput(textOutput, pathToTry)
						if parseErr == nil && (parsedData["SHA-256"] != "" || parsedData["CDHash"] != "") {
							// Convert to JSON format that parseSantactlOutput expects
							jsonData := convertTextToJSON(parsedData)
							fmt.Printf("  ✅ Got data from text format\n")
							return jsonData, nil
						} else if parseErr != nil {
							fmt.Printf("  ⚠️  Failed to parse text output: %v\n", parseErr)
						}
					}
				} else {
					fmt.Printf("  ⚠️  Text format command failed: %v\n", errText)
				}
				
				if attempt < maxRetries {
					fmt.Printf("  ⏳ Got empty array, waiting 5 seconds before retry...\n")
					time.Sleep(5 * time.Second)
					break // Break out of path loop to retry
				}
			} else if len(outputStr) > 0 {
				// Got something but it's not valid, continue to next path
				continue
			}
		}
		
		// If we've exhausted all retries, break
		if attempt >= maxRetries {
			break
		}
	}
	
		// Final fallback: if we got empty arrays from JSON, try text format one last time
		if len(output) > 0 {
			outputStr := strings.TrimSpace(string(output))
			if outputStr == "[]" {
				fmt.Printf("  ℹ️  All JSON attempts returned empty, trying text format as final fallback (may take 10+ seconds)...\n")
				// Try text format on the original app path
				if strings.HasSuffix(appPath, ".app") {
					// Try .app bundle path
					cmdText := exec.Command("santactl", "fileinfo", appPath)
				var stdoutText bytes.Buffer
				var stderrText bytes.Buffer
				cmdText.Stdout = &stdoutText
				cmdText.Stderr = &stderrText
				if errText := cmdText.Run(); errText == nil {
					textOutput := stdoutText.Bytes()
					if len(textOutput) > 0 {
						// Parse text output and convert to JSON-like structure
						parsedData, parseErr := parseSantactlTextOutput(textOutput, appPath)
						if parseErr == nil && (parsedData["SHA-256"] != "" || parsedData["CDHash"] != "") {
							// Convert to JSON format that parseSantactlOutput expects
							jsonData := convertTextToJSON(parsedData)
							fmt.Printf("  ✅ Got data from text format (final fallback)\n")
							return jsonData, nil
						} else if parseErr != nil {
							fmt.Printf("  ⚠️  Failed to parse text output: %v\n", parseErr)
						}
					}
				}
			}
		}
	}
	
	if err != nil {
		// Even if command fails, check if we got valid JSON output
		// Sometimes santactl returns valid JSON but exits with non-zero code
		if len(output) > 0 {
			// Try to parse to see if it's valid JSON
			var testArray []interface{}
			if json.Unmarshal(output, &testArray) == nil && len(testArray) > 0 {
				// Valid JSON with data, return it even though command "failed"
				fmt.Printf("  ✅ Got data despite error code\n")
				return output, nil
			}
		}
		outputStr := strings.TrimSpace(string(output))
		return nil, fmt.Errorf("santactl failed after %d attempts: %w (output: %s)", 
			maxRetries, err, outputStr[:min(200, len(outputStr))])
	}

	return output, nil
}

// parseSantactlTextOutput parses text output from santactl (without --json flag)
// Format example:
//   SHA-256                : eadb726f24b005cb2a5d1a6271ea41288bd6af7379ed3eee0d7921140652d55a
//   Team ID                : JP58VMK957
//   Signing ID             : JP58VMK957:com.kapeli.dashdoc
//   CDHash                 : 026e1e6b906106e60c668c66903386748432cea3
func parseSantactlTextOutput(output []byte, path string) (map[string]string, error) {
	result := make(map[string]string)
	text := string(output)
	lines := strings.Split(text, "\n")
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// Look for key-value pairs with colon separator
		// Format: "Field Name            : value"
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			
			if value == "" {
				continue
			}
			
			// Normalize key names (case-insensitive matching)
			keyLower := strings.ToLower(key)
			if keyLower == "sha-256" || (strings.Contains(keyLower, "sha") && strings.Contains(keyLower, "256")) {
				result["SHA-256"] = value
			} else if keyLower == "cdhash" || strings.Contains(keyLower, "cd hash") {
				result["CDHash"] = value
			} else if keyLower == "signing id" || keyLower == "signingid" {
				result["Signing ID"] = value
			} else if keyLower == "team id" || keyLower == "teamid" {
				result["Team ID"] = value
			}
		}
	}
	
	return result, nil
}

// convertTextToJSON converts parsed text data to JSON format expected by parseSantactlOutput
func convertTextToJSON(data map[string]string) []byte {
	// Create a JSON array with one object, matching santactl's JSON output format
	jsonObj := map[string]interface{}{}
	
	if sha256, ok := data["SHA-256"]; ok && sha256 != "" {
		jsonObj["SHA-256"] = sha256
	}
	if cdhash, ok := data["CDHash"]; ok && cdhash != "" {
		jsonObj["CDHash"] = cdhash
	}
	if signingID, ok := data["Signing ID"]; ok && signingID != "" {
		jsonObj["Signing ID"] = signingID
	}
	if teamID, ok := data["Team ID"]; ok && teamID != "" {
		jsonObj["Team ID"] = teamID
	}
	
	jsonArray := []map[string]interface{}{jsonObj}
	jsonBytes, _ := json.Marshal(jsonArray)
	return jsonBytes
}

func parseSantactlOutput(output []byte, app securityAppVersionInfo) (appSecurityInfo, error) {
	// Check if output is empty
	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" || outputStr == "[]" || outputStr == "null" {
		return appSecurityInfo{}, fmt.Errorf("santactl returned empty output (app may not be signed or may be unsigned)")
	}

	// santactl returns an array of file info objects
	var santactlArray []map[string]interface{}
	if err := json.Unmarshal(output, &santactlArray); err != nil {
		// Try to provide more context in error message
		outputPreview := outputStr
		if len(outputPreview) > 500 {
			outputPreview = outputPreview[:500] + "..."
		}
		return appSecurityInfo{}, fmt.Errorf("failed to parse santactl JSON: %w (output preview: %s)", err, outputPreview)
	}

	if len(santactlArray) == 0 {
		// Debug: log what we actually got
		fmt.Printf("  ⚠️  Debug: santactl returned array with length 0. Raw output (first 500 chars): %s\n", outputStr[:min(500, len(outputStr))])
		return appSecurityInfo{}, fmt.Errorf("santactl returned empty array (app may not be signed or may be unsigned)")
	}

	// Use the first entry (main executable)
	santactlData := santactlArray[0]
	
	// Check if the entry has actual signing data (ignore "Rule" field which is just a warning)
	// Even if daemon can't communicate, santactl can still return signing info
	hasSigningData := false
	if _, ok := santactlData["SHA-256"].(string); ok {
		hasSigningData = true
	}
	if _, ok := santactlData["CDHash"].(string); ok {
		hasSigningData = true
	}
	if _, ok := santactlData["Signing ID"].(string); ok {
		hasSigningData = true
	}
	if _, ok := santactlData["Team ID"].(string); ok {
		hasSigningData = true
	}
	
	// If we have a "Rule" field but no signing data, it's an error
	if rule, hasRule := santactlData["Rule"].(string); hasRule && !hasSigningData {
		return appSecurityInfo{}, fmt.Errorf("santactl returned error: %s (app may not be signed or may be unsigned)", rule)
	}

	securityInfo := appSecurityInfo{
		Slug:        app.Slug,
		Name:        app.Name,
		Version:     app.Version,
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
	}

	// Extract SHA-256 (note: santactl uses "SHA-256" with hyphen)
	if sha256, ok := santactlData["SHA-256"].(string); ok {
		securityInfo.Sha256 = sha256
	}

	// Extract CDHash
	if cdhash, ok := santactlData["CDHash"].(string); ok {
		securityInfo.Cdhash = cdhash
	}

	// Extract Signing ID and Team ID
	// These might be in different fields depending on santactl version
	if signingID, ok := santactlData["Signing ID"].(string); ok {
		securityInfo.SigningID = signingID
	}
	if teamID, ok := santactlData["Team ID"].(string); ok {
		securityInfo.TeamID = teamID
	}

	// Also check for nested signing info
	if signingInfo, ok := santactlData["SigningInfo"].(map[string]interface{}); ok {
		if signingID, ok := signingInfo["SigningID"].(string); ok {
			securityInfo.SigningID = signingID
		}
		if teamID, ok := signingInfo["TeamID"].(string); ok {
			securityInfo.TeamID = teamID
		}
	}

	return securityInfo, nil
}

func uninstallApp(app securityAppVersionInfo) error {
	fmt.Printf("  🗑️  Uninstalling app...\n")

	// Find the installed app (use same logic as installation)
	appPath, err := findInstalledApp(app)
	if err != nil {
		// App not found, consider it already uninstalled
		return nil
	}

	// Try regular removal first
	if err := os.RemoveAll(appPath); err == nil {
		return nil
	}

	// If regular removal fails (permission denied), try with sudo
	fmt.Printf("  🔐 Using sudo to remove protected files...\n")
	cmd := exec.Command("sudo", "rm", "-rf", appPath)
	if err := cmd.Run(); err != nil {
		// Even if sudo fails, try to remove what we can
		// Some apps have files that can't be deleted, which is okay
		fmt.Printf("  ⚠️  Some files may remain (this is usually okay)\n")
		return nil // Don't fail the whole process if uninstall has issues
	}

	return nil
}

func cleanupTempFiles() {
	// Clean up any remaining temp files
	os.RemoveAll(tempDir)
	os.MkdirAll(tempDir, 0755)
}
