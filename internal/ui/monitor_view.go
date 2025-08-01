package ui

import (
	"fmt"
	"redis-cli-dashboard/internal/logger"
	"redis-cli-dashboard/internal/redis"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// MonitorView represents the monitoring view
type MonitorView struct {
	redis     *redis.Client
	component *tview.TextView

	// Monitoring state
	monitoring bool
	ticker     *time.Ticker
	stopChan   chan bool
}

// NewMonitorView creates a new monitor view
func NewMonitorView(redisClient *redis.Client) *MonitorView {
	logger.Logger.Println("Initializing MonitorView...")
	view := &MonitorView{
		redis:    redisClient,
		stopChan: make(chan bool),
	}

	view.setupUI()
	view.loadMetrics()
	logger.Logger.Println("MonitorView initialized")

	return view
}

// setupUI initializes the UI components
func (v *MonitorView) setupUI() {
	v.component = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true)

	v.component.SetInputCapture(v.handleInput)

	v.component.SetBorder(true).
		SetTitle("Real-time Monitoring").
		SetBorderPadding(0, 0, 1, 1)
}

// GetComponent returns the main component
func (v *MonitorView) GetComponent() tview.Primitive {
	return v.component
}

// handleInput handles input for the monitor view
func (v *MonitorView) handleInput(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 's', 'S':
		v.toggleMonitoring()
		return nil
	case 'c', 'C':
		v.clearScreen()
		return nil
	case 'r', 'R':
		v.Refresh()
		return nil
	}

	// Let all other keys pass through to global handler (including 1-6, ?, etc.)
	return event
}

// toggleMonitoring starts or stops monitoring
func (v *MonitorView) toggleMonitoring() {
	if v.monitoring {
		v.stopMonitoring()
	} else {
		v.startMonitoring()
	}
}

// startMonitoring starts real-time monitoring
func (v *MonitorView) startMonitoring() {
	if v.monitoring {
		return
	}

	v.monitoring = true
	v.ticker = time.NewTicker(1 * time.Second)

	go func() {
		for {
			select {
			case <-v.ticker.C:
				v.loadMetrics()
			case <-v.stopChan:
				return
			}
		}
	}()

	v.updateTitle()
}

// stopMonitoring stops real-time monitoring
func (v *MonitorView) stopMonitoring() {
	if !v.monitoring {
		return
	}

	v.monitoring = false
	if v.ticker != nil {
		v.ticker.Stop()
	}
	v.stopChan <- true

	v.updateTitle()
}

// updateTitle updates the title based on monitoring state
func (v *MonitorView) updateTitle() {
	if v.monitoring {
		v.component.SetTitle("Real-time Monitoring [ACTIVE]")
	} else {
		v.component.SetTitle("Real-time Monitoring [STOPPED]")
	}
}

// clearScreen clears the screen
func (v *MonitorView) clearScreen() {
	v.component.SetText("")
}

// loadMetrics loads and displays metrics
func (v *MonitorView) loadMetrics() {
	metrics, err := v.redis.GetMetrics()
	if err != nil {
		v.component.SetText(fmt.Sprintf("[red]Error loading metrics: %s", err))
		return
	}

	// Get Redis info for additional details
	info, err := v.redis.Info()
	if err != nil {
		v.component.SetText(fmt.Sprintf("[red]Error loading Redis info: %s", err))
		return
	}

	// Get current text and append new metrics
	currentText := v.component.GetText(false)
	timestamp := time.Now().Format("15:04:05")

	// Calculate hit rate
	hitRate := float64(0)
	if metrics.KeyspaceHits+metrics.KeyspaceMisses > 0 {
		hitRate = float64(metrics.KeyspaceHits) / float64(metrics.KeyspaceHits+metrics.KeyspaceMisses) * 100
	}

	// Parse additional metrics from info
	slowlogLen := getInfoValue(info, "slowlog_len", "0")
	totalConnections := getInfoValue(info, "total_connections_received", "0")
	rejectedConnections := getInfoValue(info, "rejected_connections", "0")

	// Get cluster information and node details
	clusterEnabled := getInfoValue(info, "cluster_enabled", "0")
	nodeTable := ""

	if clusterEnabled == "1" {
		// Get cluster nodes information
		nodeTable = v.getClusterNodesTable()
	} else {
		// For standalone, show current node info
		role := getInfoValue(info, "role", "master")
		nodeTable = fmt.Sprintf("  [green]%-15s %-10s %-15s %-10s[white]\n  %-15s %-10s %-15s %-10s",
			"Node ID", "Role", "Host:Port", "Status",
			"localhost", role, "127.0.0.1:6379", "connected")
	}

	// Format metrics with enhanced information
	metricsText := fmt.Sprintf(`[yellow]%s[white] - Redis Metrics:

[cyan]━━━ Client Connections ━━━[white]
  [green]Connected Clients:[white] %d
  [green]Total Connections:[white] %s
  [green]Rejected Connections:[white] %s

[cyan]━━━ Memory Usage ━━━[white]
  [green]Used Memory:[white] %s
  [green]Used Memory RSS:[white] %s

[cyan]━━━ Command Statistics ━━━[white]
  [green]Total Commands:[white] %d
  [green]Ops/sec:[white] %d
  [green]Keyspace Hits:[white] %d
  [green]Keyspace Misses:[white] %d
  [green]Hit Rate:[white] %.2f%%

[cyan]━━━ Slow Queries Log ━━━[white]
  [green]Slow Log Length:[white] %s

[cyan]━━━ Nodes ━━━[white]
%s

`,
		timestamp,
		metrics.ConnectedClients,
		totalConnections,
		rejectedConnections,
		humanize.Bytes(uint64(metrics.UsedMemory)),
		humanize.Bytes(uint64(metrics.UsedMemoryRss)),
		metrics.TotalCommandsProcessed,
		metrics.InstantaneousOpsPerSec,
		metrics.KeyspaceHits,
		metrics.KeyspaceMisses,
		hitRate,
		slowlogLen,
		nodeTable,
	)

	// Append to existing text
	newText := currentText + metricsText

	// Keep only last 50 lines to prevent memory issues
	lines := splitLines(newText)
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
		newText = joinLines(lines)
	}

	v.component.SetText(newText)

	// Scroll to bottom
	v.component.ScrollToEnd()
}

// getClusterNodesTable returns formatted cluster nodes information
func (v *MonitorView) getClusterNodesTable() string {
	// Try to get cluster nodes information
	result, err := v.redis.ClusterNodes()
	if err != nil {
		return "  [red]Error getting cluster nodes info[white]"
	}

	// Parse cluster nodes output
	lines := splitLines(result)
	if len(lines) == 0 {
		return "  [yellow]No cluster nodes found[white]"
	}

	table := fmt.Sprintf("  [green]%-40s %-10s %-20s %-10s[white]\n", "Node ID", "Role", "Host:Port", "Status")
	table += fmt.Sprintf("  [green]%s[white]\n", "────────────────────────────────────────────────────────────────────────────────")

	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse node line format: <id> <ip:port@cport> <flags> <master> <ping-sent> <pong-recv> <config-epoch> <link-state> <slot> <slot> ... <slot>
		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}

		nodeID := parts[0][:8] + "..."              // Truncate node ID for display
		hostPort := strings.Split(parts[1], "@")[0] // Remove cluster port
		flags := parts[2]
		linkState := parts[7]

		// Determine role from flags
		role := "slave"
		if strings.Contains(flags, "master") {
			role = "master"
		}
		if strings.Contains(flags, "myself") {
			role += " (self)"
		}

		// Color code status
		status := linkState
		if linkState == "connected" {
			status = "[green]connected[white]"
		} else {
			status = "[red]" + linkState + "[white]"
		}

		table += fmt.Sprintf("  %-40s %-10s %-20s %s\n", nodeID, role, hostPort, status)
	}

	return table
}

// getInfoValue safely extracts a value from info map
func getInfoValue(info map[string]interface{}, key, defaultValue string) string {
	if val, ok := info[key]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return defaultValue
}

// Refresh refreshes the monitor view
func (v *MonitorView) Refresh() {
	v.loadMetrics()
}

// getFormattedUptime formats uptime in seconds to human readable format with minutes
func getFormattedUptime(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
	}

	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := ((seconds % 86400) % 3600) / 60
	return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
}

// splitLines splits text into lines
func splitLines(text string) []string {
	if text == "" {
		return []string{}
	}

	lines := []string{}
	current := ""

	for _, char := range text {
		if char == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(char)
		}
	}

	if current != "" {
		lines = append(lines, current)
	}

	return lines
}

// joinLines joins lines with newlines
func joinLines(lines []string) string {
	result := ""
	for i, line := range lines {
		result += line
		if i < len(lines)-1 {
			result += "\n"
		}
	}
	return result
}
