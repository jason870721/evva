package lsp

import (
	"os"
	"os/exec"
	"path/filepath"
)

// knownServer describes a well-known LSP server for auto-detection.
type knownServer struct {
	Name        string
	Command     string
	Args        []string
	Extensions  map[string]string
	ProjectFile string // marker file in project root (e.g. "go.mod")
}

// knownServers is the catalog of servers DiscoverServers checks for.
var knownServers = []knownServer{
	{
		Name: "gopls", Command: "gopls",
		Extensions:  map[string]string{".go": "go"},
		ProjectFile: "go.mod",
	},
	{
		Name: "typescript", Command: "typescript-language-server",
		Args:        []string{"--stdio"},
		Extensions:  map[string]string{".ts": "typescript", ".tsx": "typescriptreact", ".js": "javascript", ".jsx": "javascriptreact"},
		ProjectFile: "package.json",
	},
	{
		Name: "rust-analyzer", Command: "rust-analyzer",
		Extensions:  map[string]string{".rs": "rust"},
		ProjectFile: "Cargo.toml",
	},
}

// DiscoverServers scans the workdir for project markers and checks PATH
// for matching LSP servers. Returns configs for usable servers.
func DiscoverServers(workdir string) map[string]LspServerConfig {
	result := make(map[string]LspServerConfig)

	for _, ks := range knownServers {
		// Check if the project marker exists.
		if ks.ProjectFile != "" && !fileExists(filepath.Join(workdir, ks.ProjectFile)) {
			continue
		}

		// Check if the server binary is on PATH.
		if _, err := exec.LookPath(ks.Command); err != nil {
			continue
		}

		exts := make(map[string]string, len(ks.Extensions))
		for ext, lang := range ks.Extensions {
			exts[ext] = lang
		}

		result[ks.Name] = LspServerConfig{
			Command:        ks.Command,
			Args:           append([]string{}, ks.Args...),
			Extensions:     exts,
			StartupTimeout: "30s",
			MaxRestarts:    3,
		}
	}

	return result
}

// SuggestServerForExt returns the name of a known server that handles the
// given extension, or "" if none is known.
func SuggestServerForExt(ext string) string {
	ext = normalizeExt(ext)
	for _, ks := range knownServers {
		if _, ok := ks.Extensions[ext]; ok {
			return ks.Name
		}
	}
	return ""
}

// installHint returns a human-readable install instruction for a command,
// or a generic "not found" message.
func installHint(command string) string {
	hints := map[string]string{
		"gopls":                      "go install golang.org/x/tools/gopls@latest",
		"typescript-language-server": "npm install -g typescript-language-server typescript",
		"rust-analyzer":              "rustup component add rust-analyzer",
	}
	if hint, ok := hints[command]; ok {
		return command + " not found in PATH. Install with: " + hint
	}
	base := filepath.Base(command)
	if hint, ok := hints[base]; ok {
		return command + " not found in PATH. Install with: " + hint
	}
	return command + " not found in PATH"
}

// IsNotFoundError reports whether err indicates the server binary was not found.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*exec.Error); ok && e.Err == exec.ErrNotFound {
		return true
	}
	return false
}

// Ensure we don't import os without using it.
var _ = os.Getenv
