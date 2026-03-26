package deployments

import (
	"bufio"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

// LogsCmd streams deployment logs
var LogsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Stream deployment logs",
	Args:  cobra.ExactArgs(1),
	RunE:  streamLogs,
}

var (
	logsFollow bool
	logsLines  int
)

func init() {
	LogsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
	LogsCmd.Flags().IntVarP(&logsLines, "lines", "n", 100, "Number of lines to show")
}

func streamLogs(cmd *cobra.Command, args []string) error {
	name := args[0]

	apiURL := getAPIURL()
	url := fmt.Sprintf("%s/v1/deployments/logs?name=%s&lines=%d&follow=%t",
		apiURL, name, logsLines, logsFollow)

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
		return fmt.Errorf("failed to get logs: %s", string(body))
	}

	// Stream logs
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if !logsFollow {
					break
				}
				continue
			}
			return err
		}

		fmt.Print(line)
	}

	return nil
}
