package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// DBCmd is the root database command
var DBCmd = &cobra.Command{
	Use:   "db",
	Short: "Manage SQLite databases",
	Long:  "Create and manage per-namespace SQLite databases",
}

// CreateCmd creates a new database
var CreateCmd = &cobra.Command{
	Use:   "create <database_name>",
	Short: "Create a new SQLite database",
	Args:  cobra.ExactArgs(1),
	RunE:  createDatabase,
}

// QueryCmd executes a SQL query
var QueryCmd = &cobra.Command{
	Use:   "query <database_name> <sql>",
	Short: "Execute a SQL query",
	Args:  cobra.ExactArgs(2),
	RunE:  queryDatabase,
}

// ListCmd lists all databases
var ListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all databases",
	RunE:  listDatabases,
}

// BackupCmd backs up a database to IPFS
var BackupCmd = &cobra.Command{
	Use:   "backup <database_name>",
	Short: "Backup database to IPFS",
	Args:  cobra.ExactArgs(1),
	RunE:  backupDatabase,
}

// BackupsCmd lists backups for a database
var BackupsCmd = &cobra.Command{
	Use:   "backups <database_name>",
	Short: "List backups for a database",
	Args:  cobra.ExactArgs(1),
	RunE:  listBackups,
}

func init() {
	DBCmd.AddCommand(CreateCmd)
	DBCmd.AddCommand(QueryCmd)
	DBCmd.AddCommand(ListCmd)
	DBCmd.AddCommand(BackupCmd)
	DBCmd.AddCommand(BackupsCmd)
}

func createDatabase(cmd *cobra.Command, args []string) error {
	dbName := args[0]

	apiURL := getAPIURL()
	url := apiURL + "/v1/db/sqlite/create"

	payload := map[string]string{
		"database_name": dbName,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to create database: %s", string(body))
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}

	fmt.Printf("✅ Database created successfully!\n\n")
	fmt.Printf("Name:      %s\n", result["database_name"])
	fmt.Printf("Home Node: %s\n", result["home_node_id"])
	fmt.Printf("Created:   %s\n", result["created_at"])

	return nil
}

func queryDatabase(cmd *cobra.Command, args []string) error {
	dbName := args[0]
	sql := args[1]

	apiURL := getAPIURL()
	url := apiURL + "/v1/db/sqlite/query"

	payload := map[string]interface{}{
		"database_name": dbName,
		"query":         sql,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("query failed: %s", string(body))
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}

	// Print results
	if rows, ok := result["rows"].([]interface{}); ok && len(rows) > 0 {
		// Print as table
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

		// Print headers
		firstRow := rows[0].(map[string]interface{})
		for col := range firstRow {
			fmt.Fprintf(w, "%s\t", col)
		}
		fmt.Fprintln(w)

		// Print rows
		for _, row := range rows {
			r := row.(map[string]interface{})
			for _, val := range r {
				fmt.Fprintf(w, "%v\t", val)
			}
			fmt.Fprintln(w)
		}

		w.Flush()

		fmt.Printf("\nRows returned: %d\n", len(rows))
	} else if rowsAffected, ok := result["rows_affected"].(float64); ok {
		fmt.Printf("✅ Query executed successfully\n")
		fmt.Printf("Rows affected: %d\n", int(rowsAffected))
	}

	return nil
}

func listDatabases(cmd *cobra.Command, args []string) error {
	apiURL := getAPIURL()
	url := apiURL + "/v1/db/sqlite/list"

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to list databases: %s", string(body))
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}

	databases, ok := result["databases"].([]interface{})
	if !ok || len(databases) == 0 {
		fmt.Println("No databases found")
		return nil
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE\tBACKUP CID\tCREATED")

	for _, db := range databases {
		d := db.(map[string]interface{})

		size := "0 B"
		if sizeBytes, ok := d["size_bytes"].(float64); ok {
			size = formatBytes(int64(sizeBytes))
		}

		backupCID := "-"
		if cid, ok := d["backup_cid"].(string); ok && cid != "" {
			if len(cid) > 12 {
				backupCID = cid[:12] + "..."
			} else {
				backupCID = cid
			}
		}

		createdAt := ""
		if created, ok := d["created_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339, created); err == nil {
				createdAt = t.Format("2006-01-02 15:04")
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			d["database_name"],
			size,
			backupCID,
			createdAt,
		)
	}

	w.Flush()

	fmt.Printf("\nTotal: %v\n", result["total"])

	return nil
}

func backupDatabase(cmd *cobra.Command, args []string) error {
	dbName := args[0]

	fmt.Printf("📦 Backing up database '%s' to IPFS...\n", dbName)

	apiURL := getAPIURL()
	url := apiURL + "/v1/db/sqlite/backup"

	payload := map[string]string{
		"database_name": dbName,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("backup failed: %s", string(body))
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}

	fmt.Printf("\n✅ Backup successful!\n\n")
	fmt.Printf("Database:   %s\n", result["database_name"])
	fmt.Printf("Backup CID: %s\n", result["backup_cid"])
	fmt.Printf("IPFS URL:   %s\n", result["ipfs_url"])
	fmt.Printf("Backed up:  %s\n", result["backed_up_at"])

	return nil
}

func listBackups(cmd *cobra.Command, args []string) error {
	dbName := args[0]

	apiURL := getAPIURL()
	url := fmt.Sprintf("%s/v1/db/sqlite/backups?database_name=%s", apiURL, dbName)

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to list backups: %s", string(body))
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return err
	}

	backups, ok := result["backups"].([]interface{})
	if !ok || len(backups) == 0 {
		fmt.Println("No backups found")
		return nil
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "CID\tSIZE\tBACKED UP")

	for _, backup := range backups {
		b := backup.(map[string]interface{})

		cid := b["backup_cid"].(string)
		if len(cid) > 20 {
			cid = cid[:20] + "..."
		}

		size := "0 B"
		if sizeBytes, ok := b["size_bytes"].(float64); ok {
			size = formatBytes(int64(sizeBytes))
		}

		backedUpAt := ""
		if backed, ok := b["backed_up_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339, backed); err == nil {
				backedUpAt = t.Format("2006-01-02 15:04")
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\n", cid, size, backedUpAt)
	}

	w.Flush()

	fmt.Printf("\nTotal: %v\n", result["total"])

	return nil
}

func getAPIURL() string {
	if url := os.Getenv("ORAMA_API_URL"); url != "" {
		return url
	}
	return "https://gateway.orama.network"
}

func getAuthToken() (string, error) {
	if token := os.Getenv("ORAMA_TOKEN"); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("no authentication token found. Set ORAMA_TOKEN environment variable")
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
