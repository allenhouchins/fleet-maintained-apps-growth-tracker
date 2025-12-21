# Fleet Maintained Apps Growth Tracker

A standalone repository that tracks and visualizes the growth of Fleet-maintained applications over time. This project automatically pulls data from the [fleetdm/fleet](https://github.com/fleetdm/fleet) repository and generates interactive visualizations.

## 🌐 View Live Dashboard

👉 **[View Interactive Dashboard](https://allenhouchins.github.io/fleet-maintained-apps-growth-tracker/)**

The dashboard provides real-time statistics, interactive charts, and detailed growth metrics.

## 📡 RSS Feed

Subscribe to the RSS feed to get notified when apps are updated with new versions or when new apps are added to the library:

👉 **[RSS Feed](https://fmalibrary.com/feed.xml)**

The feed tracks version updates and new app additions, keeping you informed about the latest changes to Fleet-maintained apps.

## 🔧 How It Works

1. **Data Collection**: A Go script uses the GitHub API to fetch commit history and file content for `ee/maintained-apps/outputs/apps.json` without cloning the repository
2. **Data Processing**: The script generates a continuous daily CSV file with app counts and tracks app versions
3. **Visualization**: An HTML file with embedded Chart.js creates interactive charts
4. **RSS Feed**: Generates an RSS feed (`feed.xml`) that tracks version updates and new app additions
5. **Automation**: GitHub Actions runs every hour to update the data

## 📁 Files

- `main.go` - Fetches data from fleetdm/fleet and generates CSV and version tracking data
- `generate_html.go` - Generates interactive HTML visualization
- `generate_readme.go` - Generates this README with embedded charts
- `generate_rss.go` - Generates RSS feed for version updates and new app additions
- `data/apps_growth.csv` - Generated CSV data file with daily app counts
- `data/app_versions.json` - Current app versions data
- `data/version_history.json` - Historical version change tracking
- `feed.xml` - RSS feed for version updates (generated)
- `index.html` - Interactive dashboard (generated)
- `.github/workflows/update-data.yml` - GitHub Actions workflow for hourly updates

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

# Generate RSS feed
go run generate_rss.go

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
