package namespacecmd

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/cli/shared"
	"github.com/spf13/cobra"
)

var rqliteCmd = &cobra.Command{
	Use:   "rqlite",
	Short: "Manage the namespace's internal RQLite database",
	Long:  "Export and import the namespace's internal RQLite database (stores deployments, DNS records, API keys, etc.).",
}

var rqliteExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the namespace's RQLite database to a local SQLite file",
	Long:  "Downloads a consistent SQLite snapshot of the namespace's internal RQLite database.",
	RunE:  rqliteExport,
}

var rqliteImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a SQLite dump into the namespace's RQLite (DESTRUCTIVE)",
	Long: `Replaces the namespace's entire RQLite database with the contents of the provided SQLite file.

WARNING: This is a destructive operation. All existing data in the namespace's RQLite
(deployments, DNS records, API keys, etc.) will be replaced with the imported file.`,
	RunE: rqliteImport,
}

func init() {
	rqliteExportCmd.Flags().StringP("output", "o", "", "Output file path (default: rqlite-export.db)")

	rqliteImportCmd.Flags().StringP("input", "i", "", "Input SQLite file path")
	_ = rqliteImportCmd.MarkFlagRequired("input")

	rqliteCmd.AddCommand(rqliteExportCmd)
	rqliteCmd.AddCommand(rqliteImportCmd)

	Cmd.AddCommand(rqliteCmd)
}

func rqliteExport(cmd *cobra.Command, args []string) error {
	output, _ := cmd.Flags().GetString("output")
	if output == "" {
		output = "rqlite-export.db"
	}

	apiURL, err := nsRQLiteAPIURL()
	if err != nil {
		return err
	}
	token, err := nsRQLiteAuthToken()
	if err != nil {
		return err
	}

	url := apiURL + "/v1/rqlite/export"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}

	fmt.Printf("Exporting RQLite database to %s...\n", output)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to gateway: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("export failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	outFile, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	written, err := io.Copy(outFile, resp.Body)
	if err != nil {
		os.Remove(output)
		return fmt.Errorf("failed to write export file: %w", err)
	}

	fmt.Printf("Export complete: %s (%d bytes)\n", output, written)
	return nil
}

func rqliteImport(cmd *cobra.Command, args []string) error {
	input, _ := cmd.Flags().GetString("input")

	info, err := os.Stat(input)
	if err != nil {
		return fmt.Errorf("cannot access input file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("input path is a directory, not a file")
	}

	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}
	gatewayURL, err := shared.GetAPIURL()
	if err != nil {
		return err
	}
	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil || !creds.IsValid() {
		return fmt.Errorf("not authenticated. Run 'orama auth login' first")
	}

	namespace := creds.Namespace
	if namespace == "" {
		namespace = "default"
	}

	fmt.Printf("WARNING: This will REPLACE the entire RQLite database for namespace '%s'.\n", namespace)
	fmt.Printf("All existing data (deployments, DNS records, API keys, etc.) will be lost.\n")
	fmt.Printf("Importing from: %s (%d bytes)\n\n", input, info.Size())
	fmt.Printf("Type the namespace name '%s' to confirm: ", namespace)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	confirmation := strings.TrimSpace(scanner.Text())
	if confirmation != namespace {
		return fmt.Errorf("aborted - namespace name did not match")
	}

	apiURL, err := nsRQLiteAPIURL()
	if err != nil {
		return err
	}
	token, err := nsRQLiteAuthToken()
	if err != nil {
		return err
	}

	file, err := os.Open(input)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer file.Close()

	url := apiURL + "/v1/rqlite/import"

	req, err := http.NewRequest(http.MethodPost, url, file)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = info.Size()

	client := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}

	fmt.Printf("Importing database...\n")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to gateway: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("import failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	fmt.Printf("Import complete. The namespace '%s' RQLite database has been replaced.\n", namespace)
	return nil
}

// nsRQLiteAPIURL and nsRQLiteAuthToken resolve the gateway and its credential
// through the one shared resolver, so a request can never carry another
// gateway's key.
func nsRQLiteAPIURL() (string, error)    { return shared.GetAPIURL() }
func nsRQLiteAuthToken() (string, error) { return shared.GetAuthToken() }
