# Data Directory

This directory contains the generated CSV file with growth data.

- `apps_growth.csv` - Generated daily by GitHub Actions workflow
  - Contains: date, app_count, apps_added_since_previous
- `app_security_info.json` - Santa/code-signing info for macOS and Windows apps (collected by CI)

## Restoring security info from git history

If `app_security_info.json` was overwritten or lost but app versions haven't changed, the collectors skip as "up to date." To restore without re-collecting from scratch:

```bash
# Restore from the most recent commit that had darwin (macOS) entries
./scripts/restore-security-info.sh

# Or restore from a specific commit
./scripts/restore-security-info.sh <commit-sha-or-ref>
```

Then commit the restored file and regenerate the site if needed (`go run generate_html.go`). Use `--force` when running the collectors to re-collect all apps anyway (e.g. after a restore that was incomplete).
