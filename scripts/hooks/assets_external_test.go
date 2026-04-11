package hooks_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/scripts/hooks"
)

func TestAssets_returnsAllCanonicalScripts(t *testing.T) {
	t.Parallel()

	assets, err := hooks.Assets()
	if err != nil {
		t.Fatalf("Assets() error = %v", err)
	}

	if len(assets) == 0 {
		t.Fatal("Assets() returned empty slice")
	}

	expectedScripts := map[string]bool{
		"common.sh":           false,
		"traceary-session.sh": false,
		"traceary-audit.sh":   false,
	}

	for _, asset := range assets {
		if _, ok := expectedScripts[asset.Name]; ok {
			expectedScripts[asset.Name] = true
		}

		if asset.Content == "" {
			t.Errorf("asset %q has empty content", asset.Name)
		}

		if strings.Contains(asset.Content, "\r\n") {
			t.Errorf("asset %q contains CRLF line endings", asset.Name)
		}

		if !strings.HasPrefix(asset.Content, "#!/bin/bash") {
			t.Errorf("asset %q does not start with shebang", asset.Name)
		}
	}

	for name, found := range expectedScripts {
		if !found {
			t.Errorf("expected script %q not found in assets", name)
		}
	}
}

func TestAssets_contentContainsExpectedFunctions(t *testing.T) {
	t.Parallel()

	assets, err := hooks.Assets()
	if err != nil {
		t.Fatalf("Assets() error = %v", err)
	}

	assetMap := make(map[string]string)
	for _, a := range assets {
		assetMap[a.Name] = a.Content
	}

	// common.sh should have key helper functions
	common := assetMap["common.sh"]
	expectedFunctions := []string{
		"traceary_read_hook_input",
		"traceary_json_get",
		"traceary_resolve_workspace",
		"traceary_resolve_agent",
		"traceary_write_workspace_state",
		"traceary_read_workspace_state",
	}
	for _, fn := range expectedFunctions {
		if !strings.Contains(common, fn) {
			t.Errorf("common.sh missing function %q", fn)
		}
	}
}
