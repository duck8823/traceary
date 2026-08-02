package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
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
		t.Fatalf("validate generated artifact: %v: %+v", err, artifact)
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
		SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1, ExpectedRevision: strings.Repeat("b", 40), ExpectedDirty: boolPointer(false),
	})
	if artifact.Status != "failed" || artifact.ErrorClass != "revision_mismatch" {
		t.Fatalf("artifact=%+v", artifact)
	}
}

func TestSearchParityDirtyRevisionMismatchRoundTrips(t *testing.T) {
	originalRevisionReader := parityRevisionReader
	parityRevisionReader = func(context.Context) (parityRevision, error) {
		return parityRevision{Commit: strings.Repeat("a", 40), Dirty: true}, nil
	}
	t.Cleanup(func() { parityRevisionReader = originalRevisionReader })

	artifact := runSearchParity(context.Background(), searchParityManifest{
		DBPath: "/private/path-must-not-be-opened", Query: "private-query", LegacyPageSize: 1, TieredPageSize: 1,
		SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1, ExpectedRevision: strings.Repeat("a", 40), ExpectedDirty: boolPointer(false),
	})
	if artifact.Status != "failed" || artifact.ErrorClass != "revision_mismatch" || !artifact.Revision.Dirty {
		t.Fatalf("artifact=%+v", artifact)
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSearchParityJSON(data); err != nil {
		t.Fatalf("validate dirty revision mismatch: %v (%s)", err, data)
	}
}

func TestSearchParityManifestRequiresPrivateFileAndRejectsUnknownFields(t *testing.T) {
	valid := `{"db_path":"x","query":"q","legacy_page_size":1,"tiered_page_size":1,"source_rows":1,"stored_bytes":1,"decoded_bytes":1,"timeout_ms":1,"expected_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_dirty":false}`
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
		Projection: parityProjection{Revision: 1, HighWater: 1, LogicalBytes: 1, PhysicalBytes: 1}, Budget: parityBudget{SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1},
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

func TestSearchParityArtifactRequiresEmittedScalarFields(t *testing.T) {
	data, _ := json.Marshal(validSearchParityArtifact())
	for _, field := range []string{`"dirty":false`, `"duplicate_count":0`, `"continuation_count":0`} {
		t.Run(field, func(t *testing.T) {
			without := bytes.Replace(data, append([]byte(","), []byte(field)...), nil, 1)
			if bytes.Equal(without, data) {
				without = bytes.Replace(data, append([]byte(field), ','), nil, 1)
			}
			if validateSearchParityJSON(without) == nil {
				t.Fatalf("artifact without %s accepted: %s", field, without)
			}
		})
	}
}

func TestParityBudgetValidationUsesManifestBounds(t *testing.T) {
	valid := parityBudget{SourceRows: apptypes.MaxLiteralSearchSourceRows, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: maxParityTimeoutMS}
	if err := valid.Validate(); err != nil {
		t.Fatalf("boundary budget rejected: %v", err)
	}
	for name, budget := range map[string]parityBudget{
		"source rows oversized": {SourceRows: apptypes.MaxLiteralSearchSourceRows + 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1},
		"timeout oversized":     {SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: maxParityTimeoutMS + 1},
		"stored bytes zero":     {SourceRows: 1, DecodedBytes: 1, TimeoutMS: 1},
		"decoded bytes zero":    {SourceRows: 1, StoredBytes: 1, TimeoutMS: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if budget.Validate() == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
}

func TestSearchParityProjectionAllowsZeroLogicalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zero-logical.db")
	if _, err := createSynthetic(context.Background(), path, 1, 1); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE events SET body='', body_plaintext_bytes=0; UPDATE command_audits SET command_text='', input_text='', output_text='', command_plaintext_bytes=0, input_plaintext_bytes=0, output_plaintext_bytes=0`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	projection, err := readParityProjection(context.Background(), path)
	if err != nil {
		t.Fatalf("read zero logical projection: %v", err)
	}
	if projection.LogicalBytes != 0 || projection.PhysicalBytes <= 0 || projection.Revision <= 0 {
		t.Fatalf("projection=%+v", projection)
	}
	artifact := validSearchParityArtifact()
	artifact.Projection.LogicalBytes = 0
	data, _ := json.Marshal(artifact)
	if err := validateSearchParityJSON(data); err != nil {
		t.Fatalf("zero logical artifact rejected: %v", err)
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

func TestStrictJSONRejectsCaseAliasesAtEveryObject(t *testing.T) {
	manifest := `{"DB_PATH":"x","query":"q","legacy_page_size":1,"tiered_page_size":1,"source_rows":1,"stored_bytes":1,"decoded_bytes":1,"timeout_ms":1,"expected_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_dirty":false}`
	if _, err := readSearchParityManifest("-", bytes.NewBufferString(manifest)); err == nil {
		t.Fatal("manifest alias accepted")
	}
	base := validSearchParityArtifact()
	data, _ := json.Marshal(base)
	for _, alias := range []struct{ old, replacement string }{
		{`"status":`, `"STATUS":`}, {`"commit":`, `"COMMIT":`}, {`"pages":`, `"PAGES":`}, {`"logical_bytes":`, `"LOGICAL_BYTES":`},
	} {
		if validateSearchParityJSON(bytes.Replace(data, []byte(alias.old), []byte(alias.replacement), 1)) == nil {
			t.Fatalf("artifact alias accepted: %s", alias.replacement)
		}
	}
}

func validSearchParityArtifact() searchParityArtifact {
	return searchParityArtifact{SchemaVersion: searchParitySchema, ComparisonContract: membershipSetContract, Status: "passed", Revision: parityRevision{Commit: strings.Repeat("a", 40)}, Legacy: parityChain{Pages: 1, Members: 2, LatencyUS: 1}, Tiered: parityChain{Pages: 1, Members: 2, LatencyUS: 1, QueryClass: "fingerprint_eligible", ObservedTier: "historical_fingerprint", Coverage: parityCoverage{Processed: 1, Examined: 1, HighWater: 1, Complete: true}}, Comparison: parityComparison{Equal: true}, Projection: parityProjection{Revision: 1, HighWater: 1, LogicalBytes: 1, PhysicalBytes: 1}, Budget: parityBudget{SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1}}
}

func TestSearchParitySemanticValidatorRejectsCraftedEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*searchParityArtifact){
		"failed without lower bound": func(a *searchParityArtifact) {
			a.Status = "failed"
			a.ErrorClass = "search_failed"
			a.Comparison = parityComparison{}
			a.Legacy.LatencyUS, a.Tiered.LatencyUS = 0, 0
		},
		"failed with latency": func(a *searchParityArtifact) {
			a.Status = "failed"
			a.ErrorClass = "search_failed"
			a.Comparison = parityComparison{}
			a.ElapsedLowerBoundUS = 1
		},
		"impossible mismatch": func(a *searchParityArtifact) {
			a.Status = "mismatch"
			a.Comparison = parityComparison{LegacyOnly: 3, Equal: false}
		},
		"negative continuation": func(a *searchParityArtifact) { a.Tiered.ContinuationCount = -1 },
		"huge continuation":     func(a *searchParityArtifact) { a.Tiered.ContinuationCount = 100 },
		"negative coverage":     func(a *searchParityArtifact) { a.Tiered.Coverage.Examined = -1 },
		"incomplete coverage":   func(a *searchParityArtifact) { a.Tiered.Coverage.Complete = false },
		"zero projection":       func(a *searchParityArtifact) { a.Projection = parityProjection{} },
		"negative projection":   func(a *searchParityArtifact) { a.Projection.Revision = -1 },
		"partial complete":      func(a *searchParityArtifact) { a.Tiered.PartialReason = "source_rows" },
		"invalid class tier":    func(a *searchParityArtifact) { a.Tiered.QueryClass = "bounded_verification" },
		"empty revision mismatch": func(a *searchParityArtifact) {
			a.Status, a.ErrorClass = "failed", "revision_mismatch"
			a.Revision = parityRevision{}
			a.Comparison = parityComparison{}
			a.Legacy.LatencyUS, a.Tiered.LatencyUS = 0, 0
		},
		"populated revision unavailable": func(a *searchParityArtifact) {
			a.Status, a.ErrorClass = "failed", "revision_unavailable"
			a.Comparison = parityComparison{}
			a.Legacy.LatencyUS, a.Tiered.LatencyUS = 0, 0
		},
		"revision mismatch claims store evidence": func(a *searchParityArtifact) {
			a.Status, a.ErrorClass = "failed", "revision_mismatch"
			a.Revision.Dirty = true
			a.Comparison = parityComparison{}
			a.Legacy.LatencyUS, a.Tiered.LatencyUS = 0, 0
		},
		"manifest failure claims revision": func(a *searchParityArtifact) {
			a.Status, a.ErrorClass = "failed", "manifest_invalid"
			a.Legacy, a.Tiered = parityChain{}, parityChain{}
			a.Comparison, a.Projection = parityComparison{}, parityProjection{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := validSearchParityArtifact()
			mutate(&a)
			data, _ := json.Marshal(a)
			if validateSearchParityJSON(data) == nil {
				t.Fatalf("crafted evidence accepted: %s", data)
			}
		})
	}
}

func TestSearchParityValidatorAcceptsUnchangedZeroRevisionProjection(t *testing.T) {
	artifact := validSearchParityArtifact()
	artifact.Projection.Revision = 0
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSearchParityJSON(data); err != nil {
		t.Fatalf("zero source revision should be valid: %v", err)
	}
}

func TestTieredProgressRejectsTerminalCoverageViolations(t *testing.T) {
	for name, page := range map[string]apptypes.LiteralSearchPage{
		"negative processed":          {Coverage: apptypes.LiteralSearchCoverage{ProcessedSources: -1, HighWater: 1}},
		"processed beyond high-water": {Coverage: apptypes.LiteralSearchCoverage{ProcessedSources: 2, HighWater: 1}},
		"complete before high-water":  {Coverage: apptypes.LiteralSearchCoverage{ProcessedSources: 1, HighWater: 2, Complete: true}},
		"complete partial":            {Coverage: apptypes.LiteralSearchCoverage{ProcessedSources: 1, HighWater: 1, Complete: true}, PartialReason: "source_rows"},
	} {
		t.Run(name, func(t *testing.T) {
			if (&tieredProgress{}).observe(page) == nil {
				t.Fatal("invalid terminal progress accepted")
			}
		})
	}
}

func TestSearchParityManifestValidationPrecedesRevisionAndStore(t *testing.T) {
	base := searchParityManifest{DBPath: "private", Query: "q", LegacyPageSize: 1, TieredPageSize: 1, SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 1, ExpectedRevision: strings.Repeat("a", 40), ExpectedDirty: boolPointer(false)}
	for name, mutate := range map[string]func(*searchParityManifest){
		"query":       func(m *searchParityManifest) { m.Query = strings.Repeat("x", apptypes.MaxLiteralSearchQueryBytes+1) },
		"source rows": func(m *searchParityManifest) { m.SourceRows = apptypes.MaxLiteralSearchSourceRows + 1 },
		"timeout":     func(m *searchParityManifest) { m.TimeoutMS = maxParityTimeoutMS + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			original := parityRevisionReader
			called := false
			parityRevisionReader = func(context.Context) (parityRevision, error) { called = true; return parityRevision{}, nil }
			t.Cleanup(func() { parityRevisionReader = original })
			manifest := base
			mutate(&manifest)
			a := runSearchParity(context.Background(), manifest)
			data, _ := json.Marshal(a)
			if called || a.ErrorClass != "manifest_invalid" || validateSearchParityJSON(data) != nil {
				t.Fatalf("artifact=%s called=%v", data, called)
			}
		})
	}
}

func TestSearchParityRunnerMixedTimeoutAndFailureRoundTripsAsFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.db")
	if _, err := createSynthetic(context.Background(), path, 1, 1); err != nil {
		t.Fatal(err)
	}
	originalRevision, originalProjection := parityRevisionReader, parityProjectionReader
	originalLegacy, originalTiered := legacyParityCollector, tieredParityCollector
	parityRevisionReader = func(context.Context) (parityRevision, error) {
		return parityRevision{Commit: strings.Repeat("a", 40)}, nil
	}
	legacyParityCollector = func(context.Context, string, parityCriteria, int, *parityChain) (map[string]struct{}, error) {
		return nil, context.DeadlineExceeded
	}
	tieredParityCollector = func(context.Context, string, parityCriteria, searchParityManifest, *parityChain) (map[string]struct{}, error) {
		return nil, errors.New("private failure")
	}
	parityProjectionReader = func(context.Context, string) (parityProjection, error) { return parityProjection{}, nil }
	t.Cleanup(func() {
		parityRevisionReader, parityProjectionReader, legacyParityCollector, tieredParityCollector = originalRevision, originalProjection, originalLegacy, originalTiered
	})
	a := runSearchParity(context.Background(), searchParityManifest{DBPath: path, Query: "q", LegacyPageSize: 1, TieredPageSize: 1, SourceRows: 1, StoredBytes: 1, DecodedBytes: 1, TimeoutMS: 100, ExpectedRevision: strings.Repeat("a", 40), ExpectedDirty: boolPointer(false)})
	data, _ := json.Marshal(a)
	if a.Status != "failed" || a.ErrorClass != "search_failed" || validateSearchParityJSON(data) != nil {
		t.Fatalf("artifact=%s", data)
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

type distinctParityReader struct {
	legacyCalls int
	tieredCalls int
}

func (r *distinctParityReader) SearchLegacyPage(context.Context, apptypes.EventSearchCriteria) ([]*model.Event, error) {
	r.legacyCalls++
	return nil, nil
}

func (r *distinctParityReader) SearchLiteralPage(context.Context, apptypes.LiteralSearchRequest) (apptypes.LiteralSearchPage, error) {
	r.tieredCalls++
	return apptypes.LiteralSearchPage{}, nil
}

func TestParityReadersDispatchDistinctAuthorities(t *testing.T) {
	reader := &distinctParityReader{}
	criteria := apptypes.NewEventSearchCriteriaBuilder(1).Query("needle").Build()
	if _, err := readLegacyParityPage(context.Background(), reader, criteria); err != nil {
		t.Fatal(err)
	}
	if reader.legacyCalls != 1 || reader.tieredCalls != 0 {
		t.Fatalf("legacy dispatch calls = legacy:%d tiered:%d", reader.legacyCalls, reader.tieredCalls)
	}
	if _, err := readTieredParityPage(context.Background(), reader, apptypes.LiteralSearchRequest{Criteria: criteria}); err != nil {
		t.Fatal(err)
	}
	if reader.legacyCalls != 1 || reader.tieredCalls != 1 {
		t.Fatalf("tiered dispatch calls = legacy:%d tiered:%d", reader.legacyCalls, reader.tieredCalls)
	}
}

func TestParityV2BindingIsKeyedAndRetirementAuthorizationIsCriterionScoped(t *testing.T) {
	fields := []string{"aggregate=events:12,audits:4", "revision=" + strings.Repeat("a", 40), "projection=3:16"}
	first, err := keyedParityBinding([]byte("first-store-cursor-key"), "target-store", fields...)
	if err != nil {
		t.Fatal(err)
	}
	second, err := keyedParityBinding([]byte("second-store-cursor-key"), "target-store", fields...)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || strings.Contains(first, fields[0]) {
		t.Fatalf("binding must be opaque and store-owned: first=%q second=%q", first, second)
	}
	criterionA, _ := keyedParityBinding([]byte("first-store-cursor-key"), "criterion", "fingerprint_eligible", "fixed-criterion-a")
	criterionB, _ := keyedParityBinding([]byte("first-store-cursor-key"), "criterion", "bounded_verification", "fixed-criterion-b")
	suite := parityV2EvidenceSuite{
		SchemaVersion: searchParityV2Schema, TargetStoreBinding: first,
		Revision:   parityRevision{Commit: strings.Repeat("a", 40)},
		Projection: parityProjection{Revision: 3, HighWater: 16, LogicalBytes: 1, PhysicalBytes: 1},
		Criteria: []parityCriterionEvidence{
			{QueryClass: "fingerprint_eligible", CriterionBinding: criterionA, Status: "passed", ComparisonEqual: true, CoverageComplete: true},
			{QueryClass: "bounded_verification", CriterionBinding: criterionB, Status: "passed", ComparisonEqual: true, CoverageComplete: true},
		},
	}
	if !suite.AuthorizesStartRetire() {
		t.Fatal("complete store-bound v2 suite did not authorize retirement")
	}
	encoded, err := json.Marshal(suite)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSearchParityJSON(encoded); err != nil {
		t.Fatalf("validateSearchParityJSON(v2) error = %v", err)
	}
	suite.Criteria[1].QueryClass = "fingerprint_eligible"
	if suite.AuthorizesStartRetire() {
		t.Fatal("duplicated query class authorized retirement")
	}
	if (parityV2EvidenceSuite{SchemaVersion: searchParitySchema}).AuthorizesStartRetire() {
		t.Fatal("v1 evidence authorized retirement")
	}
}
