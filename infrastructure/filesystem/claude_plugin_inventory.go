package filesystem

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ClaudeLocalLeftoverScan reports the enabled, local-scope Traceary plugin
// rows in Claude's installed plugin inventory whose project directory no
// longer exists on disk (for example, deleted worktrees).
type ClaudeLocalLeftoverScan struct {
	// InventoryPath is the installed_plugins.json file that was read.
	InventoryPath string
	// LeftoverPaths lists the projectPath values of enabled local Traceary
	// installs whose directory is missing (or exists but is not a
	// directory), sorted for stable diagnostics.
	LeftoverPaths []string
}

// ListClaudeLocalPluginLeftovers reads
// ~/.claude/plugins/installed_plugins.json (for the given home) and
// returns the enabled local-scope Traceary rows whose projectPath
// directory no longer exists. The inventory is parsed read-only and only
// the projectPath values it lists are statted — no worktree trees are
// walked. A missing inventory file returns an error matching
// os.IsNotExist so callers can omit the diagnostic entirely; an
// unreadable or malformed file returns a non-nil error so the caller can
// surface a WARN.
func ListClaudeLocalPluginLeftovers(home string) (ClaudeLocalLeftoverScan, error) {
	inventoryPath := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	scan := ClaudeLocalLeftoverScan{InventoryPath: inventoryPath}
	content, err := os.ReadFile(inventoryPath)
	if err != nil {
		return scan, fmt.Errorf("failed to read %s: %w", inventoryPath, err)
	}

	var inventory struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
			Enabled     *bool  `json:"enabled"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		return scan, fmt.Errorf("failed to parse %s: %w", inventoryPath, err)
	}

	for key, rows := range inventory.Plugins {
		// The plugin key is `<plugin>@<marketplace>`; Traceary's canonical
		// plugin name is `traceary`, so match any marketplace that hosts
		// it (same prefix rule as settings.json detection).
		if !strings.HasPrefix(key, "traceary@") {
			continue
		}
		for _, row := range rows {
			if row.Scope != "local" {
				continue
			}
			// Claude omits `enabled` on enabled rows; only an explicit
			// `enabled: false` disables an install.
			if row.Enabled != nil && !*row.Enabled {
				continue
			}
			projectPath := strings.TrimSpace(row.ProjectPath)
			if projectPath == "" {
				continue
			}
			if info, statErr := os.Stat(filepath.Clean(projectPath)); statErr == nil && info.IsDir() {
				continue
			}
			scan.LeftoverPaths = append(scan.LeftoverPaths, projectPath)
		}
	}
	sort.Strings(scan.LeftoverPaths)
	return scan, nil
}
