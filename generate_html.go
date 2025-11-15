package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
)

const (
	csvFile      = "data/apps_growth.csv"
	outputHTML   = "index.html"
)

type csvData struct {
	Dates           []string `json:"dates"`
	Counts          []int    `json:"counts"`
	Additions       []int    `json:"additions"`
	GrowthDates     []string `json:"growthDates"`
	GrowthCounts    []int    `json:"growthCounts"`
	GrowthAdditions []int    `json:"growthAdditions"`
}

func generateHTML() error {
	fmt.Println("🎨 Generating HTML visualization...")

	data, err := loadCSVData()
	if err != nil {
		return fmt.Errorf("failed to load CSV data: %w", err)
	}

	htmlContent := generateHTMLContent(data)

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
		var count, added int
		fmt.Sscanf(row[1], "%d", &count)
		fmt.Sscanf(row[2], "%d", &added)

		data.Dates = append(data.Dates, dateStr)
		data.Counts = append(data.Counts, count)
		data.Additions = append(data.Additions, added)

		if added > 0 {
			data.GrowthDates = append(data.GrowthDates, dateStr)
			data.GrowthCounts = append(data.GrowthCounts, count)
			data.GrowthAdditions = append(data.GrowthAdditions, added)
		}
	}

	return data, nil
}

func main() {
	if err := generateHTML(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: %v\n", err)
		os.Exit(1)
	}
}

func generateHTMLContent(data *csvData) string {
	dataJSON, _ := json.MarshalIndent(data, "        ", "  ")
	dataJSONStr := string(dataJSON)

	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
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
        }
        h1 {
            color: #1e293b;
            margin-bottom: 10px;
        }
        .subtitle {
            color: #64748b;
            margin-bottom: 30px;
        }
        .chart-container {
            position: relative;
            height: 450px;
            margin-bottom: 40px;
        }
        .chart-container.bar {
            height: 300px;
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
    </style>
</head>
<body>
    <div class="container">
        <h1>Fleet Maintained Apps Growth Over Time</h1>
        <p class="subtitle">Continuous daily tracking across the entire year (not just commit days)</p>
        
        <div class="chart-container">
            <canvas id="cumulativeChart"></canvas>
        </div>
        
        <div class="chart-container bar">
            <canvas id="additionsChart"></canvas>
        </div>
        
        <div class="stats" id="stats">
            <!-- Stats will be populated by JavaScript -->
        </div>
        
        <div class="footer">
            <p>Data source: <a href="https://github.com/fleetdm/fleet" target="_blank">fleetdm/fleet</a> | 
            Last updated: <span id="lastUpdated"></span></p>
        </div>
    </div>

    <script>
        // Embedded CSV data
        const csvData = ` + dataJSONStr + `;
        
        // Process data into format needed for charts
        function processData() {
            const data = {
                dates: csvData.dates.map(d => new Date(d + 'T00:00:00')),
                counts: csvData.counts,
                additions: csvData.additions,
                growthDates: csvData.growthDates.map(d => new Date(d + 'T00:00:00')),
                growthCounts: csvData.growthCounts,
                growthAdditions: csvData.growthAdditions
            };
            return data;
        }
        
        function createCharts() {
            const data = processData();
            
            // Calculate stats
            const totalGrowth = data.counts[data.counts.length - 1] - data.counts[0];
            const daysSpan = Math.ceil((data.dates[data.dates.length - 1] - data.dates[0]) / (1000 * 60 * 60 * 24));
            const avgPerMonth = totalGrowth / (daysSpan / 30.44);
            const growthEvents = data.growthDates.length;
            
            // Update last updated time
            document.getElementById('lastUpdated').textContent = new Date().toLocaleString();
            
            // Update stats cards
            document.getElementById('stats').innerHTML = 
                '<div class="stat-card">' +
                    '<div class="stat-value">' + data.counts[data.counts.length - 1] + '</div>' +
                    '<div class="stat-label">Total Apps</div>' +
                '</div>' +
                '<div class="stat-card">' +
                    '<div class="stat-value">' + totalGrowth + '</div>' +
                    '<div class="stat-label">Apps Added Since Launch</div>' +
                '</div>' +
                '<div class="stat-card">' +
                    '<div class="stat-value">' + daysSpan + '</div>' +
                    '<div class="stat-label">Days Tracked</div>' +
                '</div>' +
                '<div class="stat-card">' +
                    '<div class="stat-value">' + avgPerMonth.toFixed(1) + '</div>' +
                    '<div class="stat-label">Apps Per Month</div>' +
                '</div>' +
                '<div class="stat-card">' +
                    '<div class="stat-value">' + growthEvents + '</div>' +
                    '<div class="stat-label">Growth Events</div>' +
                '</div>';
            
            // Cumulative Growth Chart
            const ctx1 = document.getElementById('cumulativeChart').getContext('2d');
            new Chart(ctx1, {
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
                    }, {
                        label: 'Growth Events',
                        data: data.growthDates.map((date, i) => ({x: date, y: data.growthCounts[i]})),
                        borderColor: '#10b981',
                        backgroundColor: '#10b981',
                        borderWidth: 0,
                        pointRadius: 6,
                        pointBackgroundColor: '#10b981',
                        pointBorderColor: '#fff',
                        pointBorderWidth: 2,
                        showLine: false
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
                                    if (context.datasetIndex === 0) {
                                        const idx = data.dates.findIndex(d => 
                                            d.getTime() === context.raw.x.getTime());
                                        const added = idx > 0 ? data.counts[idx] - data.counts[idx - 1] : data.counts[idx];
                                        return 'Total: ' + context.parsed.y + ' apps' + (added > 0 ? ' (+' + added + ' added)' : '');
                                    } else {
                                        const idx = data.growthDates.findIndex(d => 
                                            d.getTime() === context.raw.x.getTime());
                                        return 'Growth: +' + data.growthAdditions[idx] + ' apps (total: ' + context.parsed.y + ')';
                                    }
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
            
            // Additions Per Event Chart
            const ctx2 = document.getElementById('additionsChart').getContext('2d');
            new Chart(ctx2, {
                type: 'bar',
                data: {
                    datasets: [{
                        label: 'Apps Added',
                        data: data.growthDates.map((date, i) => ({x: date, y: data.growthAdditions[i]})),
                        backgroundColor: data.growthAdditions.map(val => 
                            val > 5 ? '#10b981' : val > 2 ? '#3b82f6' : '#60a5fa'),
                        borderColor: data.growthAdditions.map(val => 
                            val > 5 ? '#059669' : val > 2 ? '#2563eb' : '#3b82f6'),
                        borderWidth: 1,
                        borderRadius: 4
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        title: {
                            display: true,
                            text: 'Additions Per Event',
                            font: { size: 16, weight: 'bold' }
                        },
                        legend: {
                            display: false
                        },
                        tooltip: {
                            callbacks: {
                                label: function(context) {
                                    const idx = data.growthDates.findIndex(d => 
                                        d.getTime() === context.raw.x.getTime());
                                    return '+' + context.parsed.y + ' apps (total: ' + data.growthCounts[idx] + ')';
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
                                text: 'Apps Added',
                                font: { weight: 'bold' }
                            },
                            ticks: {
                                stepSize: 2
                            }
                        }
                    }
                }
            });
        }
        
        createCharts();
    </script>
</body>
</html>`
}

