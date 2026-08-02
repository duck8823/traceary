package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchParitySyntheticExhaustsBothChainsWithoutPrivateOutput(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "synthetic.db")
	if _, err := createSynthetic(ctx, path, 7, 1); err != nil {
		t.Fatalf("create synthetic parity fixture: %v", err)
	}
	revision, err := repositoryRevision(ctx)
	if err != nil {
		t.Fatalf("read revision: %v", err)
	}
	manifest := searchParityManifest{
		DBPath: path, Query: "synthetic", Workspace: "synthetic",
		LegacyPageSize: 3, TieredPageSize: 2, SourceRows: 2,
		StoredBytes: 4 << 20, DecodedBytes: 4 << 20, TimeoutMS: 30_000,
		ExpectedRevision: revision.Commit, ExpectedDirty: revision.Dirty,
	}
	artifact := runSearchParity(ctx, manifest)
	if artifact.Status != "passed" || !artifact.Comparison.Equal {
		t.Fatalf("parity artifact = %+v", artifact)
	}
	if artifact.Legacy.Pages < 2 || artifact.Tiered.Pages < 2 || artifact.Tiered.ContinuationCount < 1 {
		t.Fatalf("chains were not exhausted across pages: %+v", artifact)
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSearchParityJSON(data); err != nil {
		t.Fatalf("validate generated artifact: %v", err)
	}
	for _, private := range []string{manifest.Query, manifest.Workspace, path, "synthetic-keep", `"continuation":`, `"cursor":`} {
		if strings.Contains(string(data), private) {
			t.Fatalf("artifact exposed private value/field %q: %s", private, data)
		}
	}
}

func TestSearchParityRejectsRevisionMismatchBeforeStoreAccess(t *testing.T) {
	artifact := runSearchParity(context.Background(), searchParityManifest{
		DBPath: "/private/path-must-not-be-opened", Query: "private-query", LegacyPageSize: 1, TieredPageSize: 1,
		SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1, ExpectedRevision: "different", ExpectedDirty: false,
	})
	if artifact.Status != "failed" || artifact.ErrorClass != "revision_mismatch" {
		t.Fatalf("artifact=%+v", artifact)
	}
}

func TestSearchParityManifestRequiresPrivateFileAndRejectsUnknownFields(t *testing.T) {
	valid := `{"db_path":"x","query":"q","legacy_page_size":1,"tiered_page_size":1,"source_rows":1,"stored_bytes":1,"decoded_bytes":1,"timeout_ms":1,"expected_revision":"r","expected_dirty":false}`
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSearchParityManifest(path, nil); fixedErrorClass(err) != "manifest_permissions" {
		t.Fatalf("permission error=%v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSearchParityManifest(path, nil); err != nil {
		t.Fatalf("read private manifest: %v", err)
	}
	unknown := strings.TrimSuffix(valid, "}") + `,"unexpected":1}`
	if _, err := readSearchParityManifest("-", bytes.NewBufferString(unknown)); fixedErrorClass(err) != "manifest_invalid" {
		t.Fatalf("unknown-field error=%v", err)
	}
}

func TestSearchParityArtifactValidatorIsStrictAndPrivacyFailClosed(t *testing.T) {
	base := searchParityArtifact{
		SchemaVersion: searchParitySchema, ComparisonContract: membershipSetContract, Status: "passed",
		Revision: parityRevision{Commit: "revision"}, Legacy: parityChain{Pages: 1, Members: 1, LatencyUS: 1},
		Tiered: parityChain{Pages: 1, Members: 1, LatencyUS: 1}, Comparison: parityComparison{Equal: true},
		Projection: parityProjection{LogicalBytes: 1, PhysicalBytes: 1}, Budget: parityBudget{SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1},
	}
	data, _ := json.Marshal(base)
	if err := validateSearchParityJSON(data); err != nil {
		t.Fatalf("valid artifact: %v", err)
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"unknown": func(b []byte) []byte { return append(bytes.TrimSuffix(b, []byte("}")), []byte(`,"extra":1}`)...) },
		"forbidden": func(b []byte) []byte {
			return append(bytes.TrimSuffix(b, []byte("}")), []byte(`,"query":"secret"}`)...)
		},
		"trailing": func(b []byte) []byte { return append(b, []byte(` {}`)...) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateSearchParityJSON(mutate(data)); err == nil {
				t.Fatal("invalid artifact accepted")
			}
		})
	}
}

func TestSearchParityStatusPrecedence(t *testing.T) {
	if got := statusPrecedence(true, true, true); got != "failed" {
		t.Fatalf("failed precedence=%s", got)
	}
	if got := statusPrecedence(false, true, true); got != "timeout" {
		t.Fatalf("timeout precedence=%s", got)
	}
	if got := statusPrecedence(false, false, true); got != "mismatch" {
		t.Fatalf("mismatch precedence=%s", got)
	}
	if got := statusPrecedence(false, false, false); got != "passed" {
		t.Fatalf("passed precedence=%s", got)
	}
}
