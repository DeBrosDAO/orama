package deployments

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// DeployCmd is the root deploy command
var DeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy applications",
	Long:  "Deploy static sites, Next.js apps, Go backends, and Node.js backends",
}

// DeployStaticCmd deploys a static site
var DeployStaticCmd = &cobra.Command{
	Use:   "static <source_path>",
	Short: "Deploy a static site (React, Vue, etc.)",
	Args:  cobra.ExactArgs(1),
	RunE:  deployStatic,
}

// DeployNextJSCmd deploys a Next.js application
var DeployNextJSCmd = &cobra.Command{
	Use:   "nextjs <source_path>",
	Short: "Deploy a Next.js application",
	Args:  cobra.ExactArgs(1),
	RunE:  deployNextJS,
}

// DeployGoCmd deploys a Go backend
var DeployGoCmd = &cobra.Command{
	Use:   "go <source_path>",
	Short: "Deploy a Go backend",
	Args:  cobra.ExactArgs(1),
	RunE:  deployGo,
}

// DeployNodeJSCmd deploys a Node.js backend
var DeployNodeJSCmd = &cobra.Command{
	Use:   "nodejs <source_path>",
	Short: "Deploy a Node.js backend",
	Args:  cobra.ExactArgs(1),
	RunE:  deployNodeJS,
}

var (
	deployName      string
	deploySubdomain string
	deploySSR       bool
	deployUpdate    bool
)

func init() {
	DeployStaticCmd.Flags().StringVar(&deployName, "name", "", "Deployment name (required)")
	DeployStaticCmd.Flags().StringVar(&deploySubdomain, "subdomain", "", "Custom subdomain")
	DeployStaticCmd.Flags().BoolVar(&deployUpdate, "update", false, "Update existing deployment")
	DeployStaticCmd.MarkFlagRequired("name")

	DeployNextJSCmd.Flags().StringVar(&deployName, "name", "", "Deployment name (required)")
	DeployNextJSCmd.Flags().StringVar(&deploySubdomain, "subdomain", "", "Custom subdomain")
	DeployNextJSCmd.Flags().BoolVar(&deploySSR, "ssr", false, "Deploy with SSR (server-side rendering)")
	DeployNextJSCmd.Flags().BoolVar(&deployUpdate, "update", false, "Update existing deployment")
	DeployNextJSCmd.MarkFlagRequired("name")

	DeployGoCmd.Flags().StringVar(&deployName, "name", "", "Deployment name (required)")
	DeployGoCmd.Flags().StringVar(&deploySubdomain, "subdomain", "", "Custom subdomain")
	DeployGoCmd.Flags().BoolVar(&deployUpdate, "update", false, "Update existing deployment")
	DeployGoCmd.MarkFlagRequired("name")

	DeployNodeJSCmd.Flags().StringVar(&deployName, "name", "", "Deployment name (required)")
	DeployNodeJSCmd.Flags().StringVar(&deploySubdomain, "subdomain", "", "Custom subdomain")
	DeployNodeJSCmd.Flags().BoolVar(&deployUpdate, "update", false, "Update existing deployment")
	DeployNodeJSCmd.MarkFlagRequired("name")

	DeployCmd.AddCommand(DeployStaticCmd)
	DeployCmd.AddCommand(DeployNextJSCmd)
	DeployCmd.AddCommand(DeployGoCmd)
	DeployCmd.AddCommand(DeployNodeJSCmd)
}

func deployStatic(cmd *cobra.Command, args []string) error {
	sourcePath := args[0]

	fmt.Printf("📦 Creating tarball from %s...\n", sourcePath)
	tarball, err := createTarball(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	defer os.Remove(tarball)

	fmt.Printf("☁️  Uploading to Orama Network...\n")

	endpoint := "/v1/deployments/static/upload"
	if deployUpdate {
		endpoint = "/v1/deployments/static/update"
	}

	resp, err := uploadDeployment(endpoint, tarball, map[string]string{
		"name":      deployName,
		"subdomain": deploySubdomain,
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n✅ Deployment successful!\n\n")
	printDeploymentInfo(resp)

	return nil
}

func deployNextJS(cmd *cobra.Command, args []string) error {
	sourcePath := args[0]

	fmt.Printf("📦 Creating tarball from %s...\n", sourcePath)
	tarball, err := createTarball(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	defer os.Remove(tarball)

	fmt.Printf("☁️  Uploading to Orama Network...\n")

	endpoint := "/v1/deployments/nextjs/upload"
	if deployUpdate {
		endpoint = "/v1/deployments/nextjs/update"
	}

	resp, err := uploadDeployment(endpoint, tarball, map[string]string{
		"name":      deployName,
		"subdomain": deploySubdomain,
		"ssr":       fmt.Sprintf("%t", deploySSR),
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n✅ Deployment successful!\n\n")
	printDeploymentInfo(resp)

	if deploySSR {
		fmt.Printf("⚠️  Note: SSR deployment may take a minute to start. Check status with: orama deployments get %s\n", deployName)
	}

	return nil
}

func deployGo(cmd *cobra.Command, args []string) error {
	sourcePath := args[0]

	fmt.Printf("📦 Creating tarball from %s...\n", sourcePath)
	tarball, err := createTarball(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	defer os.Remove(tarball)

	fmt.Printf("☁️  Uploading to Orama Network...\n")

	endpoint := "/v1/deployments/go/upload"
	if deployUpdate {
		endpoint = "/v1/deployments/go/update"
	}

	resp, err := uploadDeployment(endpoint, tarball, map[string]string{
		"name":      deployName,
		"subdomain": deploySubdomain,
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n✅ Deployment successful!\n\n")
	printDeploymentInfo(resp)

	return nil
}

func deployNodeJS(cmd *cobra.Command, args []string) error {
	sourcePath := args[0]

	fmt.Printf("📦 Creating tarball from %s...\n", sourcePath)
	tarball, err := createTarball(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	defer os.Remove(tarball)

	fmt.Printf("☁️  Uploading to Orama Network...\n")

	endpoint := "/v1/deployments/nodejs/upload"
	if deployUpdate {
		endpoint = "/v1/deployments/nodejs/update"
	}

	resp, err := uploadDeployment(endpoint, tarball, map[string]string{
		"name":      deployName,
		"subdomain": deploySubdomain,
	})
	if err != nil {
		return err
	}

	fmt.Printf("\n✅ Deployment successful!\n\n")
	printDeploymentInfo(resp)

	return nil
}

func createTarball(sourcePath string) (string, error) {
	// Create temp file
	tmpFile, err := os.CreateTemp("", "orama-deploy-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	// Create gzip writer
	gzWriter := gzip.NewWriter(tmpFile)
	defer gzWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Walk directory and add files
	err = filepath.Walk(sourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden files and node_modules
		if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Name() == "node_modules" {
			return filepath.SkipDir
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Update header name to be relative to source
		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		header.Name = relPath

		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		// Write file content if not a directory
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(tarWriter, file)
			return err
		}

		return nil
	})

	return tmpFile.Name(), err
}

func uploadDeployment(endpoint, tarballPath string, formData map[string]string) (map[string]interface{}, error) {
	// Open tarball
	file, err := os.Open(tarballPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Create multipart request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add form fields
	for key, value := range formData {
		writer.WriteField(key, value)
	}

	// Add file
	part, err := writer.CreateFormFile("tarball", filepath.Base(tarballPath))
	if err != nil {
		return nil, err
	}

	_, err = io.Copy(part, file)
	if err != nil {
		return nil, err
	}

	writer.Close()

	// Get API URL from config
	apiURL := getAPIURL()
	url := apiURL + endpoint

	// Create request
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Add auth header
	token, err := getAuthToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deployment failed: %s", string(respBody))
	}

	// Parse response
	var result map[string]interface{}
	err = json.Unmarshal(respBody, &result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func printDeploymentInfo(resp map[string]interface{}) {
	fmt.Printf("Name:         %s\n", resp["name"])
	fmt.Printf("Type:         %s\n", resp["type"])
	fmt.Printf("Status:       %s\n", resp["status"])
	fmt.Printf("Version:      %v\n", resp["version"])
	if contentCID, ok := resp["content_cid"]; ok && contentCID != "" {
		fmt.Printf("Content CID:  %s\n", contentCID)
	}

	if urls, ok := resp["urls"].([]interface{}); ok && len(urls) > 0 {
		fmt.Printf("\nURLs:\n")
		for _, url := range urls {
			fmt.Printf("  • %s\n", url)
		}
	}
}

func getAPIURL() string {
	// TODO: Read from config file
	if url := os.Getenv("ORAMA_API_URL"); url != "" {
		return url
	}
	return "https://gateway.orama.network"
}

func getAuthToken() (string, error) {
	// TODO: Read from config file
	if token := os.Getenv("ORAMA_TOKEN"); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("no authentication token found. Set ORAMA_TOKEN environment variable")
}
