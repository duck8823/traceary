package main

import "testing"

// TestVerifyIntegrations_PassesOnCurrentTree is the Go equivalent of running
// scripts/verify_integrations.py against the repository: the current tree must
// be consistent. The CLI smoke (Codex removed-command stubs) is skipped here so
// the test stays fast; CI runs `go run ./cmd/repo-tooling integrations verify`
// with the smoke enabled.
func TestVerifyIntegrations_PassesOnCurrentTree(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}
	if err := verifyIntegrations(root, false); err != nil {
		t.Fatalf("verifyIntegrations() error = %v", err)
	}
}

// TestVerifyIntegrations_FailsWhenRootIncomplete pins that the verifier fails
// loudly on a tree missing the expected files rather than passing vacuously.
func TestVerifyIntegrations_FailsWhenRootIncomplete(t *testing.T) {
	t.Parallel()

	if err := verifyIntegrations(t.TempDir(), false); err == nil {
		t.Fatal("verifyIntegrations() on an empty tree = nil, want an error")
	}
}
