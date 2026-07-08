// Command gentailwind builds internal/gui/static/style.css from
// internal/gui/ui/static-src/input.css using the Tailwind v4 standalone CLI —
// no Node/npm involved. It downloads the CLI binary for the host GOOS/GOARCH
// into ./bin/ on first run (skipped if already present) and re-execs it.
//
// Invoked via internal/gui/ui/gen.go's //go:generate directive.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const tailwindBaseURL = "https://github.com/tailwindlabs/tailwindcss/releases/latest/download"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gentailwind:", err)
		os.Exit(1)
	}
}

func run() error {
	repoRoot, err := repoRoot()
	if err != nil {
		return err
	}

	binPath, err := ensureTailwindBinary(filepath.Join(repoRoot, "bin"))
	if err != nil {
		return err
	}

	input := filepath.Join(repoRoot, "internal", "gui", "ui", "static-src", "input.css")
	output := filepath.Join(repoRoot, "internal", "gui", "static", "style.css")

	cmd := exec.CommandContext(context.Background(), binPath, "-i", input, "-o", output, "--minify")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run tailwindcss: %w", err)
	}
	fmt.Println("gentailwind: wrote", output)
	return nil
}

// repoRootModule is the root module's declaration line, used to recognize the
// repo root directory regardless of where `go generate` set the working
// directory (it runs each directive from the directory of the file containing
// it, i.e. internal/gui/ui — not necessarily the repo root).
const repoRootModule = "module github.com/mallardduck/BrambleGate"

// repoRoot walks up from the working directory to find the repo root: the
// directory whose go.mod declares repoRootModule (every sibling module in
// this workspace has its own go.mod, so matching by module name — not merely
// "a go.mod exists" — is required).
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), repoRootModule) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repo root (%q) above %s", repoRootModule, dir)
		}
		dir = parent
	}
}

func ensureTailwindBinary(binDir string) (string, error) {
	name := "tailwindcss"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(binDir, name)

	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	artifact, err := releaseArtifact()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}

	url := tailwindBaseURL + "/" + artifact
	fmt.Println("gentailwind: downloading", url)
	if err := downloadFile(url, path); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o755); err != nil {
			return "", err
		}
	}
	return path, nil
}

func releaseArtifact() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return "tailwindcss-windows-x64.exe", nil
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "tailwindcss-macos-arm64", nil
		}
		return "tailwindcss-macos-x64", nil
	case "linux":
		if runtime.GOARCH == "arm64" {
			return "tailwindcss-linux-arm64", nil
		}
		return "tailwindcss-linux-x64", nil
	default:
		return "", fmt.Errorf("unsupported GOOS %q for the Tailwind standalone CLI", runtime.GOOS)
	}
}

func downloadFile(url, dest string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}
