// merge-security-info merges two app_security_info.json files, resolving conflicts
// by preferring non-empty fields and more recent lastUpdated per app.
// Used when git merge conflicts occur between macOS and Windows security info workflows.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type appInfo struct {
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Sha256       string     `json:"sha256,omitempty"`
	Cdhash       string     `json:"cdhash,omitempty"`
	SigningID    string     `json:"signingId,omitempty"`
	TeamID       string     `json:"teamId,omitempty"`
	Publisher    string     `json:"publisher,omitempty"`
	Issuer       string     `json:"issuer,omitempty"`
	SerialNumber string     `json:"serialNumber,omitempty"`
	Thumbprint   string     `json:"thumbprint,omitempty"`
	Timestamp    string     `json:"timestamp,omitempty"`
	LastUpdated  string     `json:"lastUpdated"`
	Apps         []appInfo  `json:"apps,omitempty"`
}

type securityInfoData struct {
	LastUpdated string    `json:"lastUpdated"`
	Apps        []appInfo `json:"apps"`
}

func mergeApp(a, b appInfo) appInfo {
	out := a
	if out.Slug == "" {
		out.Slug = b.Slug
	}
	if out.Name == "" {
		out.Name = b.Name
	}
	if out.Version == "" {
		out.Version = b.Version
	}
	if out.Sha256 == "" {
		out.Sha256 = b.Sha256
	}
	if out.Cdhash == "" {
		out.Cdhash = b.Cdhash
	}
	if out.SigningID == "" {
		out.SigningID = b.SigningID
	}
	if out.TeamID == "" {
		out.TeamID = b.TeamID
	}
	if out.Publisher == "" {
		out.Publisher = b.Publisher
	}
	if out.Issuer == "" {
		out.Issuer = b.Issuer
	}
	if out.SerialNumber == "" {
		out.SerialNumber = b.SerialNumber
	}
	if out.Thumbprint == "" {
		out.Thumbprint = b.Thumbprint
	}
	if out.Timestamp == "" {
		out.Timestamp = b.Timestamp
	}
	// If b has more recent lastUpdated, prefer b's versioned fields (but keep a's security fields if b is empty)
	if a.LastUpdated != "" && b.LastUpdated != "" {
		ta, _ := time.Parse(time.RFC3339, a.LastUpdated)
		tb, _ := time.Parse(time.RFC3339, b.LastUpdated)
		if tb.After(ta) {
			if b.Version != "" {
				out.Version = b.Version
			}
			if b.Sha256 != "" {
				out.Sha256 = b.Sha256
			}
			out.LastUpdated = b.LastUpdated
			if len(b.Apps) > 0 {
				out.Apps = b.Apps
			}
			// b's platform-specific fields
			if strings.HasSuffix(out.Slug, "/darwin") {
				if b.Cdhash != "" {
					out.Cdhash = b.Cdhash
				}
				if b.SigningID != "" {
					out.SigningID = b.SigningID
				}
				if b.TeamID != "" {
					out.TeamID = b.TeamID
				}
			} else {
				if b.Publisher != "" {
					out.Publisher = b.Publisher
				}
				if b.Issuer != "" {
					out.Issuer = b.Issuer
				}
				if b.SerialNumber != "" {
					out.SerialNumber = b.SerialNumber
				}
				if b.Thumbprint != "" {
					out.Thumbprint = b.Thumbprint
				}
				if b.Timestamp != "" {
					out.Timestamp = b.Timestamp
				}
			}
		}
	} else if b.LastUpdated != "" && a.LastUpdated == "" {
		out.LastUpdated = b.LastUpdated
	}
	if len(out.Apps) == 0 && len(b.Apps) > 0 {
		out.Apps = b.Apps
	}
	return out
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <ours.json> <theirs.json>\n", os.Args[0])
		os.Exit(1)
	}
	oursPath := os.Args[1]
	theirsPath := os.Args[2]

	oursData, err := os.ReadFile(oursPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", oursPath, err)
		os.Exit(1)
	}
	theirsData, err := os.ReadFile(theirsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", theirsPath, err)
		os.Exit(1)
	}

	var ours, theirs securityInfoData
	if err := json.Unmarshal(oursData, &ours); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", oursPath, err)
		os.Exit(1)
	}
	if err := json.Unmarshal(theirsData, &theirs); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", theirsPath, err)
		os.Exit(1)
	}

	merged := make(map[string]appInfo)
	for _, app := range ours.Apps {
		merged[app.Slug] = app
	}
	for _, app := range theirs.Apps {
		if existing, ok := merged[app.Slug]; ok {
			merged[app.Slug] = mergeApp(existing, app)
		} else {
			merged[app.Slug] = app
		}
	}

	var apps []appInfo
	for _, app := range merged {
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool {
		return strings.Compare(apps[i].Slug, apps[j].Slug) < 0
	})

	// Use the more recent lastUpdated from top-level
	lastUpdated := ours.LastUpdated
	if theirs.LastUpdated != "" {
		to, _ := time.Parse(time.RFC3339, ours.LastUpdated)
		tt, _ := time.Parse(time.RFC3339, theirs.LastUpdated)
		if tt.After(to) {
			lastUpdated = theirs.LastUpdated
		}
	}

	result := securityInfoData{
		LastUpdated: lastUpdated,
		Apps:        apps,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding: %v\n", err)
		os.Exit(1)
	}
}
