package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	csvFile     = "data/apps_growth.csv"
	readmeFile  = "README.md"
	chartWidth  = 800
	chartHeight = 400
)

func generateREADME() error {
	fmt.Println("📝 Generating README with embedded charts...")

	data, err := loadCSVForREADME()
	if err != nil {
		return fmt.Errorf("failed to load CSV data: %w", err)
	}

	readmeContent := generateREADMEContent(data)

	if err := os.WriteFile(readmeFile, []byte(readmeContent), 0644); err != nil {
		return fmt.Errorf("failed to write README file: %w", err)
	}

	fmt.Printf("✅ Generated %s\n", readmeFile)
	return nil
}

type readmeData struct {
	totalApps      int
	totalGrowth    int
	daysSpan       int
	avgPerMonth    float64
	growthEvents   int
	firstDate      string
	lastDate       string
	growthMilestones []struct {
		date  string
		count int
		added int
	}
}

func loadCSVForREADME() (*readmeData, error) {
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
		return nil, fmt.Errorf("CSV file is empty")
	}

	data := &readmeData{
		growthMilestones: make([]struct {
			date  string
			count int
			added int
		}, 0),
	}

	var counts []int
	var firstDateParsed, lastDateParsed time.Time

	for i := 1; i < len(records); i++ {
		row := records[i]
		if len(row) < 3 {
			continue
		}

		dateStr := row[0]
		var count, added int
		fmt.Sscanf(row[1], "%d", &count)
		fmt.Sscanf(row[2], "%d", &added)

		if i == 1 {
			data.firstDate = dateStr
			firstDateParsed, _ = time.Parse("2006-01-02", dateStr)
		}
		data.lastDate = dateStr
		lastDateParsed, _ = time.Parse("2006-01-02", dateStr)

		counts = append(counts, count)

		if added > 0 {
			data.growthMilestones = append(data.growthMilestones, struct {
				date  string
				count int
				added int
			}{
				date:  dateStr,
				count: count,
				added: added,
			})
		}
	}

	if len(counts) > 0 {
		data.totalApps = counts[len(counts)-1]
		data.totalGrowth = data.totalApps - counts[0]
		data.daysSpan = int(lastDateParsed.Sub(firstDateParsed).Hours() / 24)
		data.avgPerMonth = float64(data.totalGrowth) / (float64(data.daysSpan) / 30.44)
		data.growthEvents = len(data.growthMilestones)
	}

	return data, nil
}

func generateREADMEContent(data *readmeData) string {
	var sb strings.Builder

	sb.WriteString("# Fleet Maintained Apps Growth Tracker\n\n")
	sb.WriteString("A standalone repository that tracks and visualizes the growth of Fleet-maintained applications over time. ")
	sb.WriteString("This project automatically pulls data from the [fleetdm/fleet](https://github.com/fleetdm/fleet) repository ")
	sb.WriteString("and generates interactive visualizations.\n\n")

	// Stats section
	sb.WriteString("## 📊 Current Stats\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("|--------|-------|\n")
	sb.WriteString(fmt.Sprintf("| **Total Apps** | %d |\n", data.totalApps))
	sb.WriteString(fmt.Sprintf("| **Apps Added Since Launch** | %d |\n", data.totalGrowth))
	sb.WriteString(fmt.Sprintf("| **Days Tracked** | %d |\n", data.daysSpan))
	sb.WriteString(fmt.Sprintf("| **Average Growth Rate** | %.1f apps/month |\n", data.avgPerMonth))
	sb.WriteString(fmt.Sprintf("| **Growth Events** | %d |\n", data.growthEvents))
	sb.WriteString(fmt.Sprintf("| **Date Range** | %s to %s |\n\n", data.firstDate, data.lastDate))

	// Chart section - using Mermaid for GitHub compatibility
	sb.WriteString("## 📈 Growth Chart\n\n")
	sb.WriteString("### Cumulative Growth Over Time\n\n")
	sb.WriteString("```mermaid\n")
	sb.WriteString("xychart-beta\n")
	sb.WriteString("    title \"Fleet Maintained Apps Growth\"\n")
	sb.WriteString("    x-axis [")
	
	// Add month labels
	months := []string{"Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov"}
	for i, month := range months {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("\"%s\"", month))
	}
	sb.WriteString("]\n")
	sb.WriteString(fmt.Sprintf("    y-axis \"Number of Apps\" 0 --> %d\n", data.totalApps+10))
	sb.WriteString("    line [")
	
	// Use actual milestone data points
	milestoneCounts := make([]int, 0)
	for _, m := range data.growthMilestones {
		milestoneCounts = append(milestoneCounts, m.count)
	}
	
	// Add key data points (use actual milestones)
	for i, point := range milestoneCounts {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%d", point))
	}
	sb.WriteString("]\n")
	sb.WriteString("```\n\n")
	
	// Also add a visual ASCII chart for better GitHub rendering
	sb.WriteString("### Growth Timeline (ASCII Chart)\n\n")
	sb.WriteString("```\n")
	maxCount := data.totalApps
	if maxCount == 0 {
		maxCount = 1
	}
	scale := 50.0
	for _, m := range data.growthMilestones {
		barLength := int(float64(m.count) * scale / float64(maxCount))
		if barLength < 1 {
			barLength = 1
		}
		bar := strings.Repeat("█", barLength)
		dateFormatted := formatDateForTable(m.date)
		sb.WriteString(fmt.Sprintf("%-15s │%s %3d apps (+%2d)\n", dateFormatted, bar, m.count, m.added))
	}
	sb.WriteString("```\n\n")

	// Growth milestones
	sb.WriteString("### Recent Growth Milestones\n\n")
	sb.WriteString("| Date | Apps Added | Total Apps |\n")
	sb.WriteString("|------|------------|------------|\n")
	
	// Show last 10 milestones
	start := len(data.growthMilestones) - 10
	if start < 0 {
		start = 0
	}
	for i := len(data.growthMilestones) - 1; i >= start; i-- {
		m := data.growthMilestones[i]
		dateFormatted := formatDateForTable(m.date)
		sb.WriteString(fmt.Sprintf("| %s | +%d | %d |\n", dateFormatted, m.added, m.count))
	}
	sb.WriteString("\n")

	// Features
	sb.WriteString("## ✨ Features\n\n")
	sb.WriteString("- 📊 **Interactive Charts**: View cumulative growth and additions per event\n")
	sb.WriteString("- 📅 **Continuous Daily Tracking**: Not just commit days, but every day\n")
	sb.WriteString("- 🔄 **Automatic Updates**: Daily updates at 12:00 PM UTC via GitHub Actions\n")
	sb.WriteString("- 📈 **Historical Data**: Complete visualization across the entire year\n")
	sb.WriteString("- 📱 **Responsive Design**: Works on desktop and mobile\n\n")

	// How it works
	sb.WriteString("## 🔧 How It Works\n\n")
	sb.WriteString("1. **Data Collection**: A Go script uses the GitHub API to fetch commit history and file content for `ee/maintained-apps/outputs/apps.json` without cloning the repository\n")
	sb.WriteString("2. **Data Processing**: The script generates a continuous daily CSV file with app counts\n")
	sb.WriteString("3. **Visualization**: An HTML file with embedded Chart.js creates interactive charts\n")
	sb.WriteString("4. **Automation**: GitHub Actions runs daily at 12:00 PM UTC to update the data\n\n")

	// View live
	sb.WriteString("## 🌐 View Live\n\n")
	sb.WriteString("👉 **[View Interactive Dashboard](https://fleetdm.github.io/fleet-apps-growth-tracker/)**\n\n")

	// Files
	sb.WriteString("## 📁 Files\n\n")
	sb.WriteString("- `main.go` - Fetches data from fleetdm/fleet and generates CSV\n")
	sb.WriteString("- `generate_html.go` - Generates interactive HTML visualization\n")
	sb.WriteString("- `generate_readme.go` - Generates this README with embedded charts\n")
	sb.WriteString("- `data/apps_growth.csv` - Generated CSV data file\n")
	sb.WriteString("- `.github/workflows/update-data.yml` - GitHub Actions workflow for daily updates\n\n")

	// Local development
	sb.WriteString("## 💻 Local Development\n\n")
	sb.WriteString("### Prerequisites\n\n")
	sb.WriteString("- Go 1.21+\n\n")
	sb.WriteString("### Setup\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("# Clone repository\n")
	sb.WriteString("git clone <your-repo-url>\n")
	sb.WriteString("cd fleet-apps-growth-tracker\n\n")
	sb.WriteString("# Generate data\n")
	sb.WriteString("go run main.go\n\n")
	sb.WriteString("# Generate HTML\n")
	sb.WriteString("go run generate_html.go\n\n")
	sb.WriteString("# Generate README\n")
	sb.WriteString("go run generate_readme.go\n\n")
	sb.WriteString("# Open index.html in your browser\n")
	sb.WriteString("open index.html\n")
	sb.WriteString("```\n\n")

	// Data source
	sb.WriteString("## 📚 Data Source\n\n")
	sb.WriteString("This project pulls data from:\n")
	sb.WriteString("- **Repository**: [fleetdm/fleet](https://github.com/fleetdm/fleet)\n")
	sb.WriteString("- **File**: `ee/maintained-apps/outputs/apps.json`\n")
	sb.WriteString("- **Method**: GitHub API (no repository cloning required)\n\n")

	// License
	sb.WriteString("## 📄 License\n\n")
	sb.WriteString("MIT License - feel free to use this project for tracking other repositories!\n")

	return sb.String()
}

func formatDateForTable(dateStr string) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}
	return t.Format("Jan 2, 2006")
}

func main() {
	if err := generateREADME(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
}

