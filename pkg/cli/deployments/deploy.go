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
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
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

	// Warn if source looks like it needs building
	if _, err := os.Stat(filepath.Join(sourcePath, "package.json")); err == nil {
		if _, err := os.Stat(filepath.Join(sourcePath, "index.html")); os.IsNotExist(err) {
			fmt.Printf("⚠️  Warning: %s has package.json but no index.html. You may need to build first.\n", sourcePath)
			fmt.Printf("   Try: cd %s && npm run build, then deploy the output directory (e.g. dist/ or out/)\n\n", sourcePath)
		}
	}

	fmt.Printf("📦 Creating tarball from %s...\n", sourcePath)
	tarball, err := createTarball(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	defer os.Remove(tarball)

	fmt.Printf("☁️  Uploading to Orama Network...\n")

	endpoint := "/v1/deployments/static/upload"
	if deployUpdate {
		endpoint = "/v1/deployments/static/update?name=" + deployName
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
	sourcePath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Verify it's a Next.js project
	if _, err := os.Stat(filepath.Join(sourcePath, "package.json")); os.IsNotExist(err) {
		return fmt.Errorf("no package.json found in %s", sourcePath)
	}

	// Step 1: Install dependencies if needed
	if _, err := os.Stat(filepath.Join(sourcePath, "node_modules")); os.IsNotExist(err) {
		fmt.Printf("📦 Installing dependencies...\n")
		if err := runBuildCommand(sourcePath, "npm", "install"); err != nil {
			return fmt.Errorf("npm install failed: %w", err)
		}
	}

	// Step 2: Build
	fmt.Printf("🔨 Building Next.js application...\n")
	if err := runBuildCommand(sourcePath, "npm", "run", "build"); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	var tarball string
	if deploySSR {
		// SSR: tarball the standalone output
		standalonePath := filepath.Join(sourcePath, ".next", "standalone")
		if _, err := os.Stat(standalonePath); os.IsNotExist(err) {
			return fmt.Errorf(".next/standalone/ not found. Ensure next.config.js has output: 'standalone'")
		}

		// Copy static assets into standalone
		staticSrc := filepath.Join(sourcePath, ".next", "static")
		staticDst := filepath.Join(standalonePath, ".next", "static")
		if _, err := os.Stat(staticSrc); err == nil {
			if err := copyDir(staticSrc, staticDst); err != nil {
				return fmt.Errorf("failed to copy static assets: %w", err)
			}
		}

		// Copy public directory if it exists
		publicSrc := filepath.Join(sourcePath, "public")
		publicDst := filepath.Join(standalonePath, "public")
		if _, err := os.Stat(publicSrc); err == nil {
			if err := copyDir(publicSrc, publicDst); err != nil {
				return fmt.Errorf("failed to copy public directory: %w", err)
			}
		}

		fmt.Printf("📦 Creating tarball from standalone output...\n")
		tarball, err = createTarballAll(standalonePath)
	} else {
		// Static export: tarball the out/ directory
		outPath := filepath.Join(sourcePath, "out")
		if _, err := os.Stat(outPath); os.IsNotExist(err) {
			return fmt.Errorf("out/ directory not found. For static export, ensure next.config.js has output: 'export'")
		}
		fmt.Printf("📦 Creating tarball from static export...\n")
		tarball, err = createTarball(outPath)
	}
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	defer os.Remove(tarball)

	fmt.Printf("☁️  Uploading to Orama Network...\n")

	endpoint := "/v1/deployments/nextjs/upload"
	if deployUpdate {
		endpoint = "/v1/deployments/nextjs/update?name=" + deployName
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
	sourcePath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Verify it's a Go project
	if _, err := os.Stat(filepath.Join(sourcePath, "go.mod")); os.IsNotExist(err) {
		return fmt.Errorf("no go.mod found in %s", sourcePath)
	}

	// Cross-compile for Linux amd64 (production VPS target)
	fmt.Printf("🔨 Building Go binary (linux/amd64)...\n")
	buildCmd := exec.Command("go", "build", "-o", "app", ".")
	buildCmd.Dir = sourcePath
	buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}
	defer os.Remove(filepath.Join(sourcePath, "app")) // Clean up after tarball

	fmt.Printf("📦 Creating tarball...\n")
	tarball, err := createTarballFiles(sourcePath, []string{"app"})
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	defer os.Remove(tarball)

	fmt.Printf("☁️  Uploading to Orama Network...\n")

	endpoint := "/v1/deployments/go/upload"
	if deployUpdate {
		endpoint = "/v1/deployments/go/update?name=" + deployName
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
	sourcePath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Verify it's a Node.js project
	if _, err := os.Stat(filepath.Join(sourcePath, "package.json")); os.IsNotExist(err) {
		return fmt.Errorf("no package.json found in %s", sourcePath)
	}

	// Install dependencies if needed
	if _, err := os.Stat(filepath.Join(sourcePath, "node_modules")); os.IsNotExist(err) {
		fmt.Printf("📦 Installing dependencies...\n")
		if err := runBuildCommand(sourcePath, "npm", "install", "--production"); err != nil {
			return fmt.Errorf("npm install failed: %w", err)
		}
	}

	// Run build script if it exists
	if hasBuildScript(sourcePath) {
		fmt.Printf("🔨 Building...\n")
		if err := runBuildCommand(sourcePath, "npm", "run", "build"); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
	}

	fmt.Printf("📦 Creating tarball...\n")
	tarball, err := createTarball(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to create tarball: %w", err)
	}
	defer os.Remove(tarball)

	fmt.Printf("☁️  Uploading to Orama Network...\n")

	endpoint := "/v1/deployments/nodejs/upload"
	if deployUpdate {
		endpoint = "/v1/deployments/nodejs/update?name=" + deployName
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

// runBuildCommand runs a command in the given directory with stdout/stderr streaming
func runBuildCommand(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// hasBuildScript checks if package.json has a "build" script
func hasBuildScript(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg map[string]interface{}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	scripts, ok := pkg["scripts"].(map[string]interface{})
	if !ok {
		return false
	}
	_, ok = scripts["build"]
	return ok
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
}

// createTarballFiles creates a tarball containing only specific files from a directory
func createTarballFiles(baseDir string, files []string) (string, error) {
	tmpFile, err := os.CreateTemp("", "orama-deploy-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	gzWriter := gzip.NewWriter(tmpFile)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	for _, f := range files {
		fullPath := filepath.Join(baseDir, f)
		info, err := os.Stat(fullPath)
		if err != nil {
			return "", fmt.Errorf("file %s not found: %w", f, err)
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return "", err
		}
		header.Name = f

		if err := tarWriter.WriteHeader(header); err != nil {
			return "", err
		}

		if !info.IsDir() {
			file, err := os.Open(fullPath)
			if err != nil {
				return "", err
			}
			_, err = io.Copy(tarWriter, file)
			file.Close()
			if err != nil {
				return "", err
			}
		}
	}

	return tmpFile.Name(), nil
}

func createTarball(sourcePath string) (string, error) {
	return createTarballWithOptions(sourcePath, true)
}

// createTarballAll creates a tarball including node_modules and hidden dirs (for standalone output)
func createTarballAll(sourcePath string) (string, error) {
	return createTarballWithOptions(sourcePath, false)
}

func createTarballWithOptions(sourcePath string, skipNodeModules bool) (string, error) {
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

		// Skip hidden files and node_modules (unless disabled)
		if skipNodeModules {
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.Name() == "node_modules" {
				return filepath.SkipDir
			}
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
	// Check environment variable first
	if url := os.Getenv("ORAMA_API_URL"); url != "" {
		return url
	}
	// Get from active environment config
	return auth.GetDefaultGatewayURL()
}

func getAuthToken() (string, error) {
	// Check environment variable first
	if token := os.Getenv("ORAMA_TOKEN"); token != "" {
		return token, nil
	}

	// Try to get from enhanced credentials store
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}

	gatewayURL := auth.GetDefaultGatewayURL()
	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		return "", fmt.Errorf("no credentials found for %s. Run 'orama auth login' to authenticate", gatewayURL)
	}

	if !creds.IsValid() {
		return "", fmt.Errorf("credentials expired for %s. Run 'orama auth login' to re-authenticate", gatewayURL)
	}

	return creds.APIKey, nil
}
