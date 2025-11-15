# Fleet Maintained Apps Growth Tracker

A standalone repository that tracks and visualizes the growth of Fleet-maintained applications over time. This project automatically pulls data from the [fleetdm/fleet](https://github.com/fleetdm/fleet) repository and generates interactive visualizations.

## 🌐 View Live Dashboard

👉 **[View Interactive Dashboard](./index.html)**

The dashboard provides real-time statistics, interactive charts, and detailed growth metrics.

## 🔧 How It Works

1. **Data Collection**: A Go script uses the GitHub API to fetch commit history and file content for `ee/maintained-apps/outputs/apps.json` without cloning the repository
2. **Data Processing**: The script generates a continuous daily CSV file with app counts
3. **Visualization**: An HTML file with embedded Chart.js creates interactive charts
4. **Automation**: GitHub Actions runs daily at 12:00 PM UTC to update the data

## 📁 Files

- `main.go` - Fetches data from fleetdm/fleet and generates CSV
- `generate_html.go` - Generates interactive HTML visualization
- `generate_readme.go` - Generates this README with embedded charts
- `data/apps_growth.csv` - Generated CSV data file
- `.github/workflows/update-data.yml` - GitHub Actions workflow for daily updates

## 💻 Local Development

### Prerequisites

- Go 1.21+

### Setup

```bash
# Clone repository
git clone <your-repo-url>
cd fleet-apps-growth-tracker

# Generate data
go run main.go

# Generate HTML
go run generate_html.go

# Generate README
go run generate_readme.go

# Open index.html in your browser
open index.html
```

## 📚 Data Source

This project pulls data from:
- **Repository**: [fleetdm/fleet](https://github.com/fleetdm/fleet)
- **File**: `ee/maintained-apps/outputs/apps.json`
- **Method**: GitHub API (no repository cloning required)

## 📄 License

MIT License - feel free to use this project for tracking other repositories!
