package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Config from .release.local.json
type Config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

// UploadResult for concurrent uploads
type UploadResult struct {
	File    string
	Success bool
	Error   error
}

var (
	token      string
	version    string
	skipGit    bool
	platforms  string
	channel    string
	configFile = ".release.local.json"
)

// SPLICE_1

func main() {
	flag.StringVar(&token, "token", "", "Upload token (required)")
	flag.StringVar(&version, "version", "", "Version to release (required)")
	flag.BoolVar(&skipGit, "skip-git", false, "Skip Git operations")
	flag.StringVar(&platforms, "platforms", "", "Platforms to build (comma-separated: linux-x64,linux-arm64,darwin-x64,darwin-arm64,windows-x64)")
	flag.Parse()

	if token == "" || version == "" {
		fmt.Println("Usage: go run release.go -token <token> -version <version> [-skip-git] [-platforms linux-x64,darwin-arm64]")
		os.Exit(1)
	}

	// Determine channel from version
	if strings.Contains(version, "beta") {
		channel = "beta"
	} else {
		channel = "stable"
	}

	// Load config
	config, err := loadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("=== NarraFork Release %s (%s channel) ===\n", version, channel)

	// Step 1: Update version in package.json
	if err := updatePackageVersion(version); err != nil {
		fmt.Printf("Error updating version: %v\n", err)
		os.Exit(1)
	}

	// Step 2: Build
	fmt.Println("\n→ Building...")
	if err := build(); err != nil {
		fmt.Printf("Build failed: %v\n", err)
		rollbackVersion()
		os.Exit(1)
	}

	// Step 3: Upload
	fmt.Println("\n→ Uploading...")
	if err := uploadAll(config); err != nil {
		fmt.Printf("Upload failed: %v\n", err)
		rollbackVersion()
		os.Exit(1)
	}

	// Step 4: Git operations
	if !skipGit {
		fmt.Println("\n→ Git operations...")
		if err := gitOperations(); err != nil {
			fmt.Printf("Git operations failed: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("\n✅ Release %s completed successfully!\n", version)
}

// SPLICE_2

func loadConfig() (*Config, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", configFile, err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", configFile, err)
	}
	if config.Server == "" {
		return nil, fmt.Errorf("server not configured in %s", configFile)
	}
	return &config, nil
}

var originalVersion string

func updatePackageVersion(newVersion string) error {
	data, err := os.ReadFile("package.json")
	if err != nil {
		return err
	}

	var pkg map[string]interface{}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return err
	}

	originalVersion = pkg["version"].(string)
	pkg["version"] = newVersion

	newData, err := json.MarshalIndent(pkg, "", "\t")
	if err != nil {
		return err
	}

	return os.WriteFile("package.json", newData, 0644)
}

func rollbackVersion() {
	if originalVersion == "" {
		return
	}
	fmt.Printf("Rolling back version to %s...\n", originalVersion)
	data, err := os.ReadFile("package.json")
	if err != nil {
		return
	}
	var pkg map[string]interface{}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return
	}
	pkg["version"] = originalVersion
	newData, _ := json.MarshalIndent(pkg, "", "\t")
	os.WriteFile("package.json", newData, 0644)
}

func build() error {
	var targetPlatforms []string

	if platforms == "all" {
		// Build all platforms
		targetPlatforms = []string{
			"linux-x64",
			"linux-arm64",
			"linux-x64-baseline",
			"darwin-x64",
			"darwin-arm64",
			"windows-x64",
		}
	} else if platforms != "" {
		// Use specified platforms
		targetPlatforms = strings.Split(platforms, ",")
	} else {
		// Default to current platform
		platform := runtime.GOOS
		arch := runtime.GOARCH
		if platform == "darwin" {
			if arch == "arm64" {
				targetPlatforms = []string{"darwin-arm64"}
			} else {
				targetPlatforms = []string{"darwin-x64"}
			}
		} else if platform == "windows" {
			targetPlatforms = []string{"windows-x64"}
		} else {
			if arch == "arm64" {
				targetPlatforms = []string{"linux-arm64"}
			} else {
				targetPlatforms = []string{"linux-x64"}
			}
		}
	}

	// Map platform names to build script targets
	platformToTarget := map[string]string{
		"linux-x64":         "build:linux-x64",
		"linux-arm64":       "build:linux-arm64",
		"linux-x64-baseline": "build:linux-x64-baseline",
		"darwin-x64":        "build:macos-x64",
		"darwin-arm64":      "build:macos-arm64",
		"macos-x64":         "build:macos-x64",
		"macos-arm64":       "build:macos-arm64",
		"windows-x64":       "build:windows",
	}

	for _, p := range targetPlatforms {
		p = strings.TrimSpace(p)
		target, ok := platformToTarget[p]
		if !ok {
			return fmt.Errorf("unknown platform: %s", p)
		}
		
		fmt.Printf("Building for %s...\n", p)
		cmd := exec.Command("bun", "run", target)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build failed for %s: %w", p, err)
		}
	}

	return nil
}

// SPLICE_3

func uploadAll(config *Config) error {
	// Find all build artifacts in dist/
	files, err := findBuildArtifacts()
	if err != nil {
		return err
	}

	if len(files) == 0 {
		return fmt.Errorf("no build artifacts found in dist/")
	}

	fmt.Printf("Found %d files to upload\n", len(files))

	// Upload concurrently
	var wg sync.WaitGroup
	results := make(chan UploadResult, len(files))

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			err := uploadFile(config, f)
			results <- UploadResult{File: f, Success: err == nil, Error: err}
		}(file)
	}

	wg.Wait()
	close(results)

	// Check results
	var failed []string
	for r := range results {
		if r.Success {
			fmt.Printf("  ✓ %s\n", filepath.Base(r.File))
		} else {
			fmt.Printf("  ✗ %s: %v\n", filepath.Base(r.File), r.Error)
			failed = append(failed, r.File)
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d files failed to upload", len(failed))
	}

	return nil
}

func findBuildArtifacts() ([]string, error) {
	var files []string
	
	distDir := "dist"
	entries, err := os.ReadDir(distDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Match narrafork-{version}-* files
		if strings.HasPrefix(name, "narrafork-") && strings.Contains(name, version) {
			files = append(files, filepath.Join(distDir, name))
		}
		// Also match latest*.yml files
		if strings.HasPrefix(name, "latest") && strings.HasSuffix(name, ".yml") {
			files = append(files, filepath.Join(distDir, name))
		}
	}

	return files, nil
}

func uploadFile(config *Config, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Determine OS and arch from filename
	filename := filepath.Base(filePath)
	osName, arch := parseOSArch(filename)

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add file
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}

	// Add form fields
	writer.WriteField("version", version)
	writer.WriteField("os", osName)
	writer.WriteField("arch", arch)
	
	// Set as latest for main binary (not blockmap/yml)
	if !strings.HasSuffix(filename, ".blockmap") && !strings.HasSuffix(filename, ".yml") {
		writer.WriteField("set_latest", "true")
	}

	writer.Close()

	// Upload
	url := fmt.Sprintf("%s/api/v1/narrafork/releases/%s", config.Server, channel)
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: %s - %s", resp.Status, string(body))
	}

	return nil
}

func parseOSArch(filename string) (string, string) {
	lower := strings.ToLower(filename)
	
	var osName, arch string
	
	switch {
	case strings.Contains(lower, "linux"):
		osName = "linux"
	case strings.Contains(lower, "darwin") || strings.Contains(lower, "macos") || strings.Contains(lower, "mac"):
		osName = "darwin"
	case strings.Contains(lower, "windows") || strings.Contains(lower, "win") || strings.HasSuffix(lower, ".exe"):
		osName = "windows"
	default:
		osName = "linux"
	}

	switch {
	case strings.Contains(lower, "arm64") || strings.Contains(lower, "aarch64"):
		arch = "arm64"
	case strings.Contains(lower, "x64-baseline"):
		arch = "x64-baseline"
	case strings.Contains(lower, "x64") || strings.Contains(lower, "amd64") || strings.Contains(lower, "x86_64"):
		arch = "x64"
	default:
		arch = "x64"
	}

	return osName, arch
}

// SPLICE_4

func gitOperations() error {
	// Commit version change
	if err := runGit("add", "package.json"); err != nil {
		return err
	}
	if err := runGit("commit", "-m", fmt.Sprintf("chore: release v%s", version)); err != nil {
		// Ignore if nothing to commit
		fmt.Println("  (no changes to commit)")
	}

	// Create and push tag
	tagName := fmt.Sprintf("v%s", version)
	if err := runGit("tag", "-a", tagName, "-m", fmt.Sprintf("Release %s", version)); err != nil {
		return fmt.Errorf("failed to create tag: %w", err)
	}

	if err := runGit("push", "origin", "HEAD"); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	if err := runGit("push", "origin", tagName); err != nil {
		return fmt.Errorf("failed to push tag: %w", err)
	}

	fmt.Printf("  ✓ Created and pushed tag %s\n", tagName)
	return nil
}

func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
