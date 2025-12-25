package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	tempDir              = "C:\\temp\\fleet-app-install"
	programFilesDir      = "C:\\Program Files"
	programFilesX86Dir   = "C:\\Program Files (x86)"
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
	Slug         string            `json:"slug"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Sha256       string            `json:"sha256,omitempty"`
	Publisher    string            `json:"publisher,omitempty"`
	Issuer       string            `json:"issuer,omitempty"`
	SerialNumber string            `json:"serialNumber,omitempty"`
	Thumbprint   string            `json:"thumbprint,omitempty"`
	Timestamp    string            `json:"timestamp,omitempty"`
	LastUpdated  string            `json:"lastUpdated"`
	Apps         []appSecurityInfo `json:"apps,omitempty"`
}

type securityInfoData struct {
	LastUpdated string            `json:"lastUpdated"`
	Apps        []appSecurityInfo `json:"apps"`
}

func main() {
	fmt.Println("🔒 Collecting Windows App Security Information")
	fmt.Println("=============================================")
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

	// Filter to Windows apps only
	var windowsApps []securityAppVersionInfo
	for _, app := range versions.Apps {
		if app.Platform == "windows" && app.InstallerURL != "" {
			// Check if we need to update this app
			existing, exists := existingMap[app.Slug]
			if !exists || existing.Version != app.Version {
				windowsApps = append(windowsApps, app)
			}
		}
	}

	if len(windowsApps) == 0 {
		fmt.Println("✅ All Windows apps are up to date. No security info collection needed.")
		return
	}

	// Check for test mode (limit to first app)
	testMode := len(os.Args) > 1 && os.Args[1] == "--test"
	if testMode && len(windowsApps) > 0 {
		fmt.Printf("🧪 TEST MODE: Processing only first app: %s\n\n", windowsApps[0].Name)
		windowsApps = windowsApps[:1]
	}

	fmt.Printf("📦 Found %d Windows apps to process\n\n", len(windowsApps))

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
					if v.Slug == slug && v.Platform == "windows" {
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
		fmt.Printf("✅ Progress saved. Processed %d/%d apps before interruption.\n", processedCount, len(windowsApps))
		os.Exit(0)
	}()

	// Process each app
	for i, app := range windowsApps {
		fmt.Printf("[%d/%d] Processing %s (%s)...\n", i+1, len(windowsApps), app.Name, app.Version)

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
			fmt.Printf("  💾 Progress saved (%d/%d apps)\n", processedCount, len(windowsApps))
		}

		// Commit changes periodically
		shouldCommit := processedCount == 1 || processedCount%10 == 0 || processedCount == len(windowsApps)
		if shouldCommit {
			if err := commitProgress(processedCount, len(windowsApps)); err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠️  Warning: Failed to commit progress: %v\n", err)
			} else {
				fmt.Printf("  📝 Progress committed to repo (%d/%d apps)\n", processedCount, len(windowsApps))
			}
		}

		// Clean up after each app
		cleanupTempFiles()
	}

	// Final save
	if err := saveSecurityInfo(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error saving final security info: %v\n", err)
		os.Exit(1)
	}

	// Final commit
	if err := commitProgress(processedCount, len(windowsApps)); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to commit final progress: %v\n", err)
	}

	fmt.Printf("\n✅ Successfully processed %d/%d apps\n", processedCount, len(windowsApps))
	fmt.Printf("✅ Security info saved to: %s\n", securityInfoJSON)
}

func commitProgress(processedCount, totalApps int) error {
	// Check if we're in a git repository
	if err := exec.Command("git", "rev-parse", "--git-dir").Run(); err != nil {
		return nil
	}

	// Check if there are changes
	statusCmd := exec.Command("git", "status", "--porcelain", securityInfoJSON)
	output, err := statusCmd.Output()
	if err != nil {
		return fmt.Errorf("checking git status: %w", err)
	}

	if len(output) == 0 {
		return nil
	}

	// Configure git
	exec.Command("git", "config", "--local", "user.email", "action@github.com").Run()
	exec.Command("git", "config", "--local", "user.name", "GitHub Action").Run()

	// Add the file
	if err := exec.Command("git", "add", securityInfoJSON).Run(); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	// Commit
	commitMsg := fmt.Sprintf("Update Windows app security info - %d/%d apps processed", processedCount, totalApps)
	if err := exec.Command("git", "commit", "-m", commitMsg).Run(); err != nil {
		return nil
	}

	// Push (non-blocking)
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

	// Extract/install app to get the executable
	exePath, err := extractOrInstallApp(installerPath, app)
	if err != nil {
		return securityInfo, fmt.Errorf("failed to extract/install app: %w", err)
	}

	// Calculate SHA-256
	sha256, err := calculateSHA256(exePath)
	if err != nil {
		return securityInfo, fmt.Errorf("failed to calculate SHA-256: %w", err)
	}

	// Get Authenticode signature info using PowerShell
	sigInfo, err := getAuthenticodeSignature(exePath)
	if err != nil {
		// Log warning but continue - app may be unsigned or tools unavailable
		// This is acceptable - we still have SHA-256 which is the most important
		fmt.Printf("  ⚠️  Note: Could not extract signature info (app may be unsigned): %v\n", err)
		// Continue with just SHA-256 - this is acceptable for unsigned apps
	} else {
		fmt.Printf("  ✓ Extracted signature info\n")
	}

	securityInfo = appSecurityInfo{
		Slug:         app.Slug,
		Name:         app.Name,
		Version:      app.Version,
		Sha256:       sha256,
		Publisher:    sigInfo.Publisher,
		Issuer:       sigInfo.Issuer,
		SerialNumber: sigInfo.SerialNumber,
		Thumbprint:   sigInfo.Thumbprint,
		Timestamp:    sigInfo.Timestamp,
		LastUpdated:  time.Now().UTC().Format(time.RFC3339),
	}

	// Clean up
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
		ext = ".exe" // Default to .exe
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
		os.Remove(filename)
		return "", err
	}
	out.Close()

	// Verify file was written
	if info, err := os.Stat(filename); err != nil || info.Size() == 0 {
		if err == nil {
			os.Remove(filename)
			return "", fmt.Errorf("downloaded file is empty")
		}
		return "", fmt.Errorf("downloaded file not found: %w", err)
	}

	return filename, nil
}

func extractOrInstallApp(installerPath string, app securityAppVersionInfo) (string, error) {
	fmt.Printf("  📦 Extracting/installing app...\n")

	ext := strings.ToLower(filepath.Ext(installerPath))

	switch ext {
	case ".msi":
		// For MSI, we can extract files without installing
		return extractFromMSI(installerPath, app)
	case ".exe":
		// For EXE, try to extract or install
		return extractFromEXE(installerPath, app)
	case ".zip":
		// Extract ZIP
		return extractFromZIP(installerPath, app)
	default:
		return "", fmt.Errorf("unsupported installer type: %s", ext)
	}
}

func extractFromMSI(msiPath string, app securityAppVersionInfo) (string, error) {
	// Use msiexec to extract files
	extractDir := filepath.Join(tempDir, "extracted")
	os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", err
	}

	// Try to extract using msiexec
	cmd := exec.Command("msiexec", "/a", msiPath, "/qn", "TARGETDIR="+extractDir)
	if err := cmd.Run(); err != nil {
		// If extraction fails, try to find the main executable in the installer
		// For now, return the MSI path itself as a fallback
		return msiPath, nil
	}

	// Find the main executable
	return findMainExecutable(extractDir, app)
}

func extractFromEXE(exePath string, app securityAppVersionInfo) (string, error) {
	// Many Windows installers are self-extracting archives
	// For now, we'll use the installer itself as the executable
	// In a full implementation, you might want to use tools like 7-Zip to extract
	
	// Check if it's a signed executable we can analyze directly
	if _, err := getAuthenticodeSignature(exePath); err == nil {
		return exePath, nil
	}

	// Try to find if it extracts to a temp location
	// For now, return the exe itself
	return exePath, nil
}

func extractFromZIP(zipPath string, app securityAppVersionInfo) (string, error) {
	extractDir := filepath.Join(tempDir, "extracted")
	os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return "", err
	}

	// Use PowerShell to extract ZIP
	psScript := fmt.Sprintf("Expand-Archive -Path '%s' -DestinationPath '%s' -Force", zipPath, extractDir)
	cmd := exec.Command("powershell", "-Command", psScript)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to extract ZIP: %w", err)
	}

	return findMainExecutable(extractDir, app)
}

func findMainExecutable(dir string, app securityAppVersionInfo) (string, error) {
	// Look for .exe files
	var exeFiles []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".exe") && !info.IsDir() {
			exeFiles = append(exeFiles, path)
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	if len(exeFiles) == 0 {
		return "", fmt.Errorf("no executable found in %s", dir)
	}

	// Prefer executables that match the app name
	appNameLower := strings.ToLower(app.Name)
	for _, exe := range exeFiles {
		exeName := strings.ToLower(filepath.Base(exe))
		if strings.Contains(exeName, appNameLower) || strings.Contains(appNameLower, exeName) {
			return exe, nil
		}
	}

	// Return the first executable found
	return exeFiles[0], nil
}

func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

type signatureInfo struct {
	Publisher    string
	Issuer       string
	SerialNumber string
	Thumbprint   string
	Timestamp    string
}

func getAuthenticodeSignature(exePath string) (signatureInfo, error) {
	var sigInfo signatureInfo

	// Try PowerShell first
	psResult, psErr := getSignatureViaPowerShell(exePath)
	if psErr == nil {
		return psResult, nil
	}

	// Fallback to signtool.exe if available
	signtoolResult, signtoolErr := getSignatureViaSigntool(exePath)
	if signtoolErr == nil {
		return signtoolResult, nil
	}

	// Try certutil as another fallback
	certutilResult, certutilErr := getSignatureViaCertutil(exePath)
	if certutilErr == nil {
		return certutilResult, nil
	}

	// If all methods fail, return a combined error
	return sigInfo, fmt.Errorf("all signature extraction methods failed: PowerShell: %v, signtool: %v, certutil: %v", psErr, signtoolErr, certutilErr)
}

func getSignatureViaPowerShell(exePath string) (signatureInfo, error) {
	var sigInfo signatureInfo

	// Use a file-based approach to avoid PowerShell type conflicts
	// Create a temporary PowerShell script file
	psScriptFile := filepath.Join(tempDir, "get-signature.ps1")
	defer os.Remove(psScriptFile)

	// Escape backslashes and quotes for PowerShell
	escapedPath := strings.ReplaceAll(exePath, "`", "``")
	escapedPath = strings.ReplaceAll(escapedPath, "$", "`$")
	
	psScript := fmt.Sprintf(`$sig = Get-AuthenticodeSignature -FilePath '%s'
if ($sig -and $sig.SignerCertificate) {
    $cert = $sig.SignerCertificate
    $publisher = $cert.Subject
    $issuer = $cert.Issuer
    $serial = $cert.SerialNumber
    $thumbprint = $cert.Thumbprint
    $timestamp = if ($sig.TimeStamperCertificate) { $sig.TimeStamperCertificate.Subject } else { "" }
    Write-Output "$publisher|$issuer|$serial|$thumbprint|$timestamp"
} else {
    Write-Error "No certificate found"
    exit 1
}`, escapedPath)

	if err := os.WriteFile(psScriptFile, []byte(psScript), 0644); err != nil {
		return sigInfo, fmt.Errorf("failed to create PowerShell script: %w", err)
	}

	// Run the script with minimal profile to avoid type conflicts
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", psScriptFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return sigInfo, fmt.Errorf("PowerShell failed: %w (output: %s)", err, string(output))
	}

	// Parse output
	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return sigInfo, fmt.Errorf("empty output from PowerShell")
	}

	// Filter out error messages from output
	lines := strings.Split(outputStr, "\n")
	var dataLine string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "|") && !strings.Contains(line, "Error") && !strings.Contains(line, "Exception") {
			dataLine = line
			break
		}
	}

	if dataLine == "" {
		return sigInfo, fmt.Errorf("no valid data in output: %s", outputStr)
	}

	parts := strings.Split(dataLine, "|")
	if len(parts) >= 4 {
		sigInfo.Publisher = strings.TrimSpace(parts[0])
		sigInfo.Issuer = strings.TrimSpace(parts[1])
		sigInfo.SerialNumber = strings.TrimSpace(parts[2])
		sigInfo.Thumbprint = strings.TrimSpace(parts[3])
		if len(parts) >= 5 && strings.TrimSpace(parts[4]) != "" {
			sigInfo.Timestamp = strings.TrimSpace(parts[4])
		}
	} else {
		return sigInfo, fmt.Errorf("unexpected output format: %s", dataLine)
	}

	return sigInfo, nil
}

func getSignatureViaSigntool(exePath string) (signatureInfo, error) {
	var sigInfo signatureInfo

	// Try to find signtool.exe in common locations
	signtoolPaths := []string{
		"C:\\Program Files (x86)\\Windows Kits\\10\\bin\\x64\\signtool.exe",
		"C:\\Program Files (x86)\\Windows Kits\\10\\bin\\10.0.22621.0\\x64\\signtool.exe",
		"C:\\Program Files\\Windows Kits\\10\\bin\\x64\\signtool.exe",
	}

	var signtoolPath string
	for _, path := range signtoolPaths {
		if _, err := os.Stat(path); err == nil {
			signtoolPath = path
			break
		}
	}

	if signtoolPath == "" {
		return sigInfo, fmt.Errorf("signtool.exe not found")
	}

	// Use signtool to verify and get certificate info
	cmd := exec.Command(signtoolPath, "verify", "/pa", "/v", exePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return sigInfo, fmt.Errorf("signtool verify failed: %w", err)
	}

	// Parse signtool output for certificate information
	outputStr := string(output)
	
	// Extract certificate info from signtool output
	// This is a simplified parser - signtool output format can vary
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Subject:") {
			sigInfo.Publisher = strings.TrimPrefix(line, "Subject:")
			sigInfo.Publisher = strings.TrimSpace(sigInfo.Publisher)
		}
		if strings.Contains(line, "Issuer:") {
			sigInfo.Issuer = strings.TrimPrefix(line, "Issuer:")
			sigInfo.Issuer = strings.TrimSpace(sigInfo.Issuer)
		}
		if strings.Contains(line, "Serial Number:") {
			sigInfo.SerialNumber = strings.TrimPrefix(line, "Serial Number:")
			sigInfo.SerialNumber = strings.TrimSpace(sigInfo.SerialNumber)
		}
		if strings.Contains(line, "Thumbprint:") {
			sigInfo.Thumbprint = strings.TrimPrefix(line, "Thumbprint:")
			sigInfo.Thumbprint = strings.TrimSpace(sigInfo.Thumbprint)
		}
	}

	if sigInfo.Publisher == "" {
		return sigInfo, fmt.Errorf("could not extract certificate info from signtool output")
	}

	return sigInfo, nil
}

func getSignatureViaCertutil(exePath string) (signatureInfo, error) {
	var sigInfo signatureInfo

	// certutil is built into Windows and can verify signatures
	// Use certutil to dump the certificate
	cmd := exec.Command("certutil", "-verify", "-v", exePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return sigInfo, fmt.Errorf("certutil verify failed: %w", err)
	}

	// Parse certutil output for certificate information
	outputStr := string(output)
	lines := strings.Split(outputStr, "\n")
	
	for i, line := range lines {
		line = strings.TrimSpace(line)
		
		// Look for certificate subject (Publisher)
		if strings.Contains(line, "Subject:") || strings.Contains(line, "Issuer:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				value := strings.TrimSpace(parts[1])
				if strings.Contains(line, "Subject:") && sigInfo.Publisher == "" {
					sigInfo.Publisher = value
				} else if strings.Contains(line, "Issuer:") && sigInfo.Issuer == "" {
					sigInfo.Issuer = value
				}
			}
		}
		
		// Look for serial number
		if strings.Contains(line, "Serial Number:") || strings.Contains(line, "Serial:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && sigInfo.SerialNumber == "" {
				sigInfo.SerialNumber = strings.TrimSpace(parts[1])
			}
		}
		
		// Look for thumbprint (SHA1 hash)
		if strings.Contains(line, "Cert Hash(sha1):") || strings.Contains(line, "Thumbprint:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && sigInfo.Thumbprint == "" {
				sigInfo.Thumbprint = strings.TrimSpace(parts[1])
				// Remove spaces from thumbprint
				sigInfo.Thumbprint = strings.ReplaceAll(sigInfo.Thumbprint, " ", "")
			}
		}
		
		// Look for timestamp info in subsequent lines
		if strings.Contains(line, "Time Stamp") && i+1 < len(lines) {
			nextLine := strings.TrimSpace(lines[i+1])
			if nextLine != "" {
				sigInfo.Timestamp = nextLine
			}
		}
	}

	if sigInfo.Publisher == "" && sigInfo.Thumbprint == "" {
		return sigInfo, fmt.Errorf("could not extract certificate info from certutil output")
	}

	return sigInfo, nil
}

func uninstallApp(app securityAppVersionInfo) error {
	fmt.Printf("  🗑️  Cleaning up...\n")
	// For Windows, we typically don't need to uninstall since we extract to temp
	// But we can clean up temp files
	return nil
}

func cleanupTempFiles() {
	os.RemoveAll(tempDir)
	os.MkdirAll(tempDir, 0755)
}

