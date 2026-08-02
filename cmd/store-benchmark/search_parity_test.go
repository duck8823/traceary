package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestSearchParitySyntheticExhaustsBothChainsWithoutPrivateOutput(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "synthetic.db")
	if _, err := createSynthetic(ctx, path, 7, 1); err != nil {
		t.Fatalf("create synthetic parity fixture: %v", err)
	}
	revision := parityRevision{Commit: strings.Repeat("a", 40), Dirty: false}
	originalRevisionReader := parityRevisionReader
	parityRevisionReader = func(context.Context) (parityRevision, error) { return revision, nil }
	t.Cleanup(func() { parityRevisionReader = originalRevisionReader })
	manifest := searchParityManifest{
		DBPath: path, Query: "synthetic", Workspace: "synthetic",
		LegacyPageSize: 3, TieredPageSize: 2, SourceRows: 2,
		StoredBytes: 4 << 20, DecodedBytes: 4 << 20, TimeoutMS: 30_000,
		ExpectedRevision: revision.Commit, ExpectedDirty: boolPointer(false),
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
		SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1, ExpectedRevision: "different", ExpectedDirty: boolPointer(false),
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
		Revision: parityRevision{Commit: strings.Repeat("a", 40)}, Legacy: parityChain{Pages: 1, Members: 1, LatencyUS: 1},
		Tiered: parityChain{Pages: 1, Members: 1, LatencyUS: 1, QueryClass: "fingerprint_eligible", ObservedTier: "historical_fingerprint", Coverage: parityCoverage{Processed: 1, Examined: 1, HighWater: 1, Complete: true}}, Comparison: parityComparison{Equal: true},
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

func boolPointer(value bool) *bool { return &value }

func TestTieredProgressRejectsCyclesBacktracksAndUnstableHighWater(t *testing.T) {
	page := func(processed, high int64, complete bool, continuation string) apptypes.LiteralSearchPage {
		return apptypes.LiteralSearchPage{Coverage: apptypes.LiteralSearchCoverage{ProcessedSources: processed, HighWater: high, Complete: complete}, Continuation: continuation}
	}
	for name, pages := range map[string][]apptypes.LiteralSearchPage{
		"missing continuation":       {page(1, 10, false, "")},
		"complete with continuation": {page(10, 10, true, "x")},
		"cycle":                      {page(1, 10, false, "a"), page(2, 10, false, "a")},
		"backtrack":                  {page(2, 10, false, "a"), page(1, 10, false, "b")},
		"unstable high-water":        {page(1, 10, false, "a"), page(2, 11, false, "b")},
	} {
		t.Run(name, func(t *testing.T) {
			progress := tieredProgress{}
			var err error
			for _, p := range pages {
				err = progress.observe(p)
				if err != nil {
					break
				}
			}
			if err == nil {
				t.Fatal("invalid progress accepted")
			}
		})
	}
}

func TestSearchParityManifestRequiresExpectedCleanAndBounds(t *testing.T) {
	base := `{"db_path":"x","query":"q","legacy_page_size":1,"tiered_page_size":1,"source_rows":1,"stored_bytes":1,"decoded_bytes":1,"timeout_ms":1,"expected_revision":"r"}`
	for name, input := range map[string]string{
		"missing dirty":     base,
		"dirty true":        strings.TrimSuffix(base, "}") + `,"expected_dirty":true}`,
		"page oversized":    strings.Replace(strings.TrimSuffix(base, "}")+`,"expected_dirty":false}`, `"legacy_page_size":1`, `"legacy_page_size":10001`, 1),
		"timeout oversized": strings.Replace(strings.TrimSuffix(base, "}")+`,"expected_dirty":false}`, `"timeout_ms":1`, `"timeout_ms":86400001`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readSearchParityManifest("-", bytes.NewBufferString(input)); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestSearchParityProjectionFailureIsSanitizedAndValid(t *testing.T) {
	originalRevision, originalProjection := parityRevisionReader, parityProjectionReader
	originalLegacy, originalTiered := legacyParityCollector, tieredParityCollector
	parityRevisionReader = func(context.Context) (parityRevision, error) {
		return parityRevision{Commit: strings.Repeat("a", 40)}, nil
	}
	parityProjectionReader = func(context.Context, string) (parityProjection, error) {
		return parityProjection{}, errors.New("private query/path/id")
	}
	legacyParityCollector = func(context.Context, string, parityCriteria, int, *parityChain) (map[string]struct{}, error) {
		return map[string]struct{}{}, nil
	}
	tieredParityCollector = func(context.Context, string, parityCriteria, searchParityManifest, *parityChain) (map[string]struct{}, error) {
		return map[string]struct{}{}, nil
	}
	t.Cleanup(func() {
		parityRevisionReader, parityProjectionReader, legacyParityCollector, tieredParityCollector = originalRevision, originalProjection, originalLegacy, originalTiered
	})
	path := filepath.Join(t.TempDir(), "projection-failure.db")
	if _, err := createSynthetic(context.Background(), path, 1, 1); err != nil {
		t.Fatal(err)
	}
	a := runSearchParity(context.Background(), searchParityManifest{DBPath: path, Query: "secret", LegacyPageSize: 1, TieredPageSize: 1, SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 100, ExpectedRevision: strings.Repeat("a", 40), ExpectedDirty: boolPointer(false)})
	data, _ := json.Marshal(a)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), path) {
		t.Fatalf("private failure leaked: %s", data)
	}
	if err := validateSearchParityJSON(data); err != nil {
		t.Fatalf("runner failure invalid: %v (%s)", err, data)
	}
	if a.ErrorClass != "projection_failed" {
		t.Fatalf("error class=%s", a.ErrorClass)
	}
}

func TestSearchParityOutcomeInvariantRejectsAdversarialEvidence(t *testing.T) {
	valid := searchParityArtifact{SchemaVersion: searchParitySchema, ComparisonContract: membershipSetContract, Status: "passed", Revision: parityRevision{Commit: strings.Repeat("a", 40)}, Legacy: parityChain{Pages: 1, Members: 2, LatencyUS: 1}, Tiered: parityChain{Pages: 1, Members: 2, LatencyUS: 1, QueryClass: "fingerprint_eligible", ObservedTier: "historical_fingerprint", Coverage: parityCoverage{Processed: 1, Examined: 1, HighWater: 1, Complete: true}}, Comparison: parityComparison{Equal: true}, Budget: parityBudget{SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1}}
	for name, mutate := range map[string]func(*searchParityArtifact){
		"dirty passed":            func(a *searchParityArtifact) { a.Revision.Dirty = true },
		"zero pages":              func(a *searchParityArtifact) { a.Legacy.Pages = 0 },
		"duplicate":               func(a *searchParityArtifact) { a.Tiered.DuplicateCount = 1 },
		"membership equation":     func(a *searchParityArtifact) { a.Tiered.Members = 1 },
		"timeout claims equality": func(a *searchParityArtifact) { a.Status = "timeout"; a.Legacy.ElapsedLowerBoundUS = 1 },
		"failed claims equality":  func(a *searchParityArtifact) { a.Status = "failed"; a.ErrorClass = "search_failed" },
	} {
		t.Run(name, func(t *testing.T) {
			a := valid
			mutate(&a)
			data, _ := json.Marshal(a)
			if validateSearchParityJSON(data) == nil {
				t.Fatalf("adversarial artifact accepted: %s", data)
			}
		})
	}
}

func TestSearchParityOneMillisecondTimeoutRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timeout.db")
	if _, err := createSynthetic(context.Background(), path, 100, 1); err != nil {
		t.Fatal(err)
	}
	original := parityRevisionReader
	parityRevisionReader = func(context.Context) (parityRevision, error) {
		return parityRevision{Commit: strings.Repeat("a", 40)}, nil
	}
	t.Cleanup(func() { parityRevisionReader = original })
	a := runSearchParity(context.Background(), searchParityManifest{DBPath: path, Query: "synthetic", Workspace: "synthetic", LegacyPageSize: 1, TieredPageSize: 1, SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1, ExpectedRevision: strings.Repeat("a", 40), ExpectedDirty: boolPointer(false)})
	if a.Status != "timeout" || a.ElapsedLowerBoundUS < 1 {
		t.Fatalf("artifact=%+v", a)
	}
	data, _ := json.Marshal(a)
	if err := validateSearchParityJSON(data); err != nil {
		t.Fatalf("timeout artifact invalid: %v: %s", err, data)
	}
}

func TestSearchParityRevisionUnavailableRoundTrips(t *testing.T) {
	original := parityRevisionReader
	parityRevisionReader = func(context.Context) (parityRevision, error) { return parityRevision{}, errors.New("not a repository") }
	t.Cleanup(func() { parityRevisionReader = original })
	a := runSearchParity(context.Background(), searchParityManifest{DBPath: "private", Query: "secret", LegacyPageSize: 1, TieredPageSize: 1, SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1, ExpectedRevision: strings.Repeat("a", 40), ExpectedDirty: boolPointer(false)})
	data, _ := json.Marshal(a)
	if a.ErrorClass != "revision_unavailable" || validateSearchParityJSON(data) != nil {
		t.Fatalf("artifact=%s", data)
	}
}

func TestStrictJSONRejectsDuplicateKeys(t *testing.T) {
	input := `{"db_path":"x","db_path":"y","query":"q","legacy_page_size":1,"tiered_page_size":1,"source_rows":1,"stored_bytes":1,"decoded_bytes":1,"timeout_ms":1,"expected_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_dirty":false}`
	if _, err := readSearchParityManifest("-", bytes.NewBufferString(input)); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestSearchParityManifestRejectsOversizeAndSymlink(t *testing.T) {
	if _, err := readSearchParityManifest("-", bytes.NewReader(bytes.Repeat([]byte("x"), maxParityManifestBytes+1))); err == nil {
		t.Fatal("oversize stdin accepted")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readSearchParityManifest(link, nil); fixedErrorClass(err) != "manifest_permissions" {
		t.Fatalf("symlink error=%v", err)
	}
}
