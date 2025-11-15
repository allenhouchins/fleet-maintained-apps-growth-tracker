# Setup Instructions

## Initial Setup

1. **Create a new GitHub repository** for this project (e.g., `fleet-apps-growth-tracker`)

2. **Clone and push this code**:
   ```bash
   git clone <your-new-repo-url>
   cd fleet-apps-growth-tracker
   # Copy all files from this directory
   git add .
   git commit -m "Initial commit"
   git push -u origin main
   ```

3. **Enable GitHub Pages**:
   - Go to your repository Settings
   - Navigate to Pages
   - Select "GitHub Actions" as the source
   - The workflow will automatically deploy when you push

4. **Test locally** (optional):
   ```bash
   # Generate data
   go run main.go
   
   # Generate HTML
   go run generate_html.go
   
   # Generate README with charts
   go run generate_readme.go
   
   # Open index.html in your browser
   open index.html
   ```

## How It Works

1. **Daily Updates**: The `.github/workflows/update-data.yml` workflow runs every day at 12:00 PM UTC
2. **Data Collection**: It clones the fleetdm/fleet repo and analyzes Git history
3. **HTML Generation**: Creates an updated `index.html` with embedded data
4. **Auto-Deploy**: GitHub Pages automatically deploys when files change

## Manual Updates

You can manually trigger an update by:
- Going to Actions → Update Growth Data → Run workflow
- Or running locally: `go run main.go && go run generate_html.go && go run generate_readme.go`

## Customization

To track a different repository:
1. Update `fleetRepoURL` in `main.go`
2. Update `appsJSONPath` if the file path is different
3. Update the title and links in `generate_html.go` and `generate_readme.go`

