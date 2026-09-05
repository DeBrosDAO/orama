package db

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/shared"
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

	apiURL, err := getAPIURL()
	if err != nil {
		return err
	}
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

	apiURL, err := getAPIURL()
	if err != nil {
		return err
	}
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
	apiURL, err := getAPIURL()
	if err != nil {
		return err
	}
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

	out := printer.For(cmd)
	if out.JSONMode() {
		// The gateway's reply verbatim, including the exact byte counts rather
		// than the rounded sizes the table shows.
		fmt.Fprintln(out.Out(), string(body))
		return nil
	}

	databases, ok := result["databases"].([]interface{})
	if !ok || len(databases) == 0 {
		out.Printf("No databases found\n")
		return nil
	}

	rows := make([][]string, 0, len(databases))
	for _, db := range databases {
		d, _ := db.(map[string]interface{})

		size := "0 B"
		if sizeBytes, ok := d["size_bytes"].(float64); ok {
			size = printer.FormatBytes(int64(sizeBytes))
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

		rows = append(rows, []string{fmt.Sprint(d["database_name"]), size, backupCID, createdAt})
	}

	if err := out.Table([]string{"NAME", "SIZE", "BACKUP CID", "CREATED"}, rows); err != nil {
		return err
	}
	out.Printf("\nTotal: %v\n", result["total"])
	return nil
}

func backupDatabase(cmd *cobra.Command, args []string) error {
	dbName := args[0]

	fmt.Printf("📦 Backing up database '%s' to IPFS...\n", dbName)

	apiURL, err := getAPIURL()
	if err != nil {
		return err
	}
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
	fmt.Printf("Backed up:  %s\n", result["backed_up_at"])

	return nil
}

func listBackups(cmd *cobra.Command, args []string) error {
	dbName := args[0]

	apiURL, err := getAPIURL()
	if err != nil {
		return err
	}
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
			size = printer.FormatBytes(int64(sizeBytes))
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

// getAPIURL and getAuthToken resolve the gateway and its credential through
// the one shared resolver, so a request can never carry another gateway's key.
func getAPIURL() (string, error)    { return shared.GetAPIURL() }
func getAuthToken() (string, error) { return shared.GetAuthToken() }

// DeleteCmd deletes a database and its file.
var DeleteCmd = &cobra.Command{
	Use:   "delete <database_name>",
	Short: "Delete a database and its file",
	Long: `Permanently delete a database.

The file and its write-ahead log are removed from the node that holds them.
There is no undo: restore from a backup with 'orama db backups' if you need the
data again.`,
	Args: cobra.ExactArgs(1),
	RunE: deleteDatabase,
}

var dbDeleteYes bool

func init() {
	DeleteCmd.Flags().BoolVar(&dbDeleteYes, "yes", false, "Skip the confirmation prompt")
	DBCmd.AddCommand(DeleteCmd)
}

// deleteDatabase removes a database after the operator types its name back.
//
// A y/n prompt is answered reflexively; typing the name is not, and it is the
// one check that catches the case that matters here — the right command aimed
// at the wrong database.
func deleteDatabase(cmd *cobra.Command, args []string) error {
	dbName := args[0]

	if !dbDeleteYes {
		fmt.Printf("This permanently deletes %q and its file. There is no undo.\n", dbName)
		fmt.Printf("Type the database name to confirm: ")

		typed, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return fmt.Errorf("read confirmation: %w", err)
		}
		if strings.TrimSpace(typed) != dbName {
			fmt.Println("Names do not match. Nothing was deleted.")
			return nil
		}
	}

	raw, err := shared.Request("POST", "/v1/db/sqlite/delete",
		map[string]string{"database_name": dbName})
	if err != nil {
		return err
	}

	var resp struct {
		FilesRemoved []string `json:"files_removed"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse gateway response: %w", err)
	}

	fmt.Printf("✓ %s deleted (%d file(s) removed).\n", dbName, len(resp.FilesRemoved))
	return nil
}
