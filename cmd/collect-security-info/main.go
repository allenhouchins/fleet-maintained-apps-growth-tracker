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
	existingSecurity, _ := loadSecurityInfo()
	existingMap := make(map[string]appSecurityInfo)
	for _, app := range existingSecurity.Apps {
		existingMap[app.Slug] = app
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

	// Determine file extension from URL
	ext := filepath.Ext(url)
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
		return "", err
	}

	return filename, nil
}

func installApp(installerPath string, app securityAppVersionInfo) (string, error) {
	fmt.Printf("  📦 Installing app...\n")

	ext := strings.ToLower(filepath.Ext(installerPath))
	var appPath string
	var err error

	switch ext {
	case ".dmg":
		appPath, err = installFromDMG(installerPath, app)
	case ".pkg":
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

	return appPath, nil
}

func installFromDMG(dmgPath string, app securityAppVersionInfo) (string, error) {
	// Verify DMG file exists and is readable
	if info, err := os.Stat(dmgPath); err != nil {
		return "", fmt.Errorf("DMG file not found or not readable: %w", err)
	} else if info.Size() == 0 {
		return "", fmt.Errorf("DMG file is empty (size: 0 bytes)")
	}

	// Clean up any existing mount point
	mountPoint := filepath.Join(tempDir, "mnt")
	os.RemoveAll(mountPoint)
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return "", fmt.Errorf("failed to create mount point: %w", err)
	}

	// Try mounting with explicit mountpoint
	cmd := exec.Command("hdiutil", "attach", dmgPath, "-mountpoint", mountPoint, "-nobrowse", "-quiet", "-noautoopen")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	
	if err != nil {
		// If explicit mountpoint fails, try letting hdiutil choose the mount point
		cmd2 := exec.Command("hdiutil", "attach", dmgPath, "-nobrowse", "-quiet", "-noautoopen")
		var stderr2 bytes.Buffer
		cmd2.Stderr = &stderr2
		output, err2 := cmd2.Output()
		if err2 != nil {
			// Both methods failed, return detailed error
			errorMsg := strings.TrimSpace(stderr.String())
			if errorMsg == "" {
				errorMsg = strings.TrimSpace(stderr2.String())
			}
			if errorMsg == "" {
				errorMsg = "unknown error (check DMG file integrity)"
			}
			return "", fmt.Errorf("failed to mount DMG: %s", errorMsg)
		}
		// Parse output to find mount point
		// hdiutil attach output format: /dev/diskXsY	/Volumes/MountName
		lines := strings.Split(string(output), "\n")
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
			// Use the most recently modified volume as a fallback
			var latestVolume string
			var latestTime time.Time
			for _, vol := range volumes {
				if info, err := os.Stat(vol); err == nil && info.IsDir() {
					if info.ModTime().After(latestTime) {
						latestTime = info.ModTime()
						latestVolume = vol
					}
				}
			}
			if latestVolume != "" {
				mountPoint = latestVolume
			} else {
				return "", fmt.Errorf("failed to mount DMG: could not determine mount point")
			}
		}
	}

	// Verify mount point exists and is accessible
	if _, err := os.Stat(mountPoint); err != nil {
		return "", fmt.Errorf("failed to mount DMG: mount point not accessible: %s", mountPoint)
	}

	defer func() {
		// Detach using the actual mount point
		exec.Command("hdiutil", "detach", mountPoint, "-quiet", "-force").Run()
	}()

	// First, check if DMG contains a .pkg file (some DMGs contain installers, not apps)
	var pkgFile string
	_ = filepath.Walk(mountPoint, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".pkg") && info != nil && !info.IsDir() {
			pkgFile = path
			return filepath.SkipDir
		}
		return nil
	})

	// If we found a PKG, install it and then find the app
	if pkgFile != "" {
		fmt.Printf("  📦 Found PKG installer in DMG, installing...\n")
		// Install the PKG
		installCmd := exec.Command("sudo", "installer", "-pkg", pkgFile, "-target", "/")
		if err := installCmd.Run(); err != nil {
			return "", fmt.Errorf("failed to install PKG from DMG: %w", err)
		}

		// Wait for installation to complete
		time.Sleep(3 * time.Second)

		// Now find the installed app in /Applications
		return findInstalledApp(app)
	}

	// Otherwise, look for .app bundle in mounted DMG - try multiple strategies
	var appBundle string

	// Strategy 1: Look for .app bundle by walking the directory tree
	_ = filepath.Walk(mountPoint, func(path string, info os.FileInfo, err error) error {
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

	// Copy .app to Applications
	appName := filepath.Base(appBundle)
	destPath := filepath.Join(applicationsDir, appName)

	// Remove existing app if present
	os.RemoveAll(destPath)

	cmd = exec.Command("cp", "-R", appBundle, destPath)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to copy app: %w", err)
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
		return bestMatch, nil
	}

	// Last resort: list recently modified apps for debugging
	var recentApps []string
	_ = filepath.Walk(applicationsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(path, ".app") && info != nil && info.IsDir() {
			// Check if modified in last 5 minutes
			if time.Since(info.ModTime()) < 5*time.Minute {
				recentApps = append(recentApps, filepath.Base(path))
			}
		}
		return nil
	})

	if len(recentApps) > 0 {
		return "", fmt.Errorf("could not find installed app after PKG installation. Recently modified apps: %v", recentApps[:min(5, len(recentApps))])
	}

	return "", fmt.Errorf("could not find installed app after PKG installation")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func installFromPKG(pkgPath string, app securityAppVersionInfo) (string, error) {
	// Install PKG
	cmd := exec.Command("sudo", "installer", "-pkg", pkgPath, "-target", "/")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to install PKG: %w", err)
	}

	// Wait for installation to complete
	time.Sleep(3 * time.Second)

	// Find the installed app
	return findInstalledApp(app)
}

func installFromZIP(zipPath string, app securityAppVersionInfo) (string, error) {
	// Extract ZIP
	extractDir := filepath.Join(tempDir, "extracted")
	os.RemoveAll(extractDir) // Clean up any previous extraction
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", err
	}

	cmd := exec.Command("unzip", "-q", zipPath, "-d", extractDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errorMsg := strings.TrimSpace(stderr.String())
		if errorMsg == "" {
			errorMsg = "unknown error"
		}
		return "", fmt.Errorf("failed to extract ZIP: %s (%w)", errorMsg, err)
	}

	// First, check if ZIP contains a .pkg file (some ZIPs contain installers, not apps)
	var pkgFile string
	_ = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".pkg") && info != nil && !info.IsDir() {
			pkgFile = path
			return filepath.SkipDir
		}
		return nil
	})

	// If we found a PKG, install it and then find the app
	if pkgFile != "" {
		fmt.Printf("  📦 Found PKG installer in ZIP, installing...\n")
		// Install the PKG
		installCmd := exec.Command("sudo", "installer", "-pkg", pkgFile, "-target", "/")
		if err := installCmd.Run(); err != nil {
			return "", fmt.Errorf("failed to install PKG from ZIP: %w", err)
		}

		// Wait for installation to complete
		time.Sleep(3 * time.Second)

		// Now find the installed app in /Applications
		return findInstalledApp(app)
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

	// Remove existing app if present
	os.RemoveAll(destPath)

	cmd = exec.Command("cp", "-R", appBundle, destPath)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to copy app: %w", err)
	}

	return destPath, nil
}

func runSantactl(appPath string) ([]byte, error) {
	fmt.Printf("  🔍 Running santactl fileinfo...\n")

	cmd := exec.Command("santactl", "fileinfo", "--json", appPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("santactl failed: %w", err)
	}

	return output, nil
}

func parseSantactlOutput(output []byte, app securityAppVersionInfo) (appSecurityInfo, error) {
	// santactl returns an array of file info objects
	var santactlArray []map[string]interface{}
	if err := json.Unmarshal(output, &santactlArray); err != nil {
		return appSecurityInfo{}, fmt.Errorf("failed to parse santactl JSON: %w", err)
	}

	if len(santactlArray) == 0 {
		return appSecurityInfo{}, fmt.Errorf("no data in santactl output")
	}

	// Use the first entry (main executable)
	santactlData := santactlArray[0]

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
