package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFoldGatesRefusesLiveStore(t *testing.T) {
	t.Parallel()
	live, err := defaultLiveStorePath()
	if err != nil {
		t.Fatalf("defaultLiveStorePath() error = %v", err)
	}
	err = runFoldGates(context.Background(), live, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "refusing the default live store") {
		t.Fatalf("runFoldGates(live) error = %v, want refusal", err)
	}
}

func TestPathsReferToSameStoreFollowsSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "live.db")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	same, err := pathsReferToSameStore(link, target)
	if err != nil {
		t.Fatalf("pathsReferToSameStore() error = %v", err)
	}
	if !same {
		t.Fatal("symlink and target must be treated as the same store")
	}
	other := filepath.Join(dir, "copy.db")
	if err := os.WriteFile(other, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	same, err = pathsReferToSameStore(other, target)
	if err != nil {
		t.Fatalf("pathsReferToSameStore(copy) error = %v", err)
	}
	if same {
		t.Fatal("distinct files must not be treated as the same store")
	}
}
