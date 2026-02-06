package deployments

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

// StatsCmd shows resource usage for a deployment
var StatsCmd = &cobra.Command{
	Use:   "stats <name>",
	Short: "Show resource usage for a deployment",
	Args:  cobra.ExactArgs(1),
	RunE:  statsDeployment,
}

func statsDeployment(cmd *cobra.Command, args []string) error {
	name := args[0]

	apiURL := getAPIURL()
	url := fmt.Sprintf("%s/v1/deployments/stats?name=%s", apiURL, name)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	token, err := getAuthToken()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to get stats: %s", string(body))
	}

	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return fmt.Errorf("failed to parse stats: %w", err)
	}

	// Display
	fmt.Println()
	fmt.Printf("  Name:    %s\n", stats["name"])
	fmt.Printf("  Type:    %s\n", stats["type"])
	fmt.Printf("  Status:  %s\n", stats["status"])

	if pid, ok := stats["pid"]; ok {
		pidInt := int(pid.(float64))
		if pidInt > 0 {
			fmt.Printf("  PID:     %d\n", pidInt)
		}
	}

	if uptime, ok := stats["uptime_seconds"]; ok {
		secs := uptime.(float64)
		if secs > 0 {
			fmt.Printf("  Uptime:  %s\n", formatUptime(secs))
		}
	}

	fmt.Println()

	if cpu, ok := stats["cpu_percent"]; ok {
		fmt.Printf("  CPU:     %.1f%%\n", cpu.(float64))
	}

	if mem, ok := stats["memory_rss_mb"]; ok {
		fmt.Printf("  RAM:     %s\n", formatSize(mem.(float64)))
	}

	if disk, ok := stats["disk_mb"]; ok {
		fmt.Printf("  Disk:    %s\n", formatSize(disk.(float64)))
	}

	fmt.Println()

	return nil
}

func formatUptime(seconds float64) string {
	s := int(seconds)
	days := s / 86400
	hours := (s % 86400) / 3600
	mins := (s % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func formatSize(mb float64) string {
	if mb < 0.1 {
		return fmt.Sprintf("%.1f KB", mb*1024)
	}
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.1f MB", mb)
}
