package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

const (
	searchParitySchema     = "traceary.tiered-search-parity/v1"
	searchParityV2Schema   = "traceary.tiered-search-parity/v2"
	membershipSetContract  = "membership_set/v1"
	maxParityManifestBytes = 1 << 20
	maxParityArtifactBytes = 1 << 20
	maxParityCriteriaCount = 16
	maxParityJSONDepth     = 16
	maxParityPageSize      = 10_000
	maxParityTimeoutMS     = int64((24 * time.Hour) / time.Millisecond)
)

// parityV2EvidenceSuite is a strictly parsed evidence envelope. Authorization
// is deliberately owned by the later application policy, which can compare
// it with a fresh current-store snapshot and key.
type parityV2EvidenceSuite struct {
	SchemaVersion      string                    `json:"schema_version"`
	AuthorizationScope string                    `json:"authorization_scope"`
	TargetStoreBinding string                    `json:"target_store_binding"`
	Revision           parityRevision            `json:"revision"`
	Projection         parityProjection          `json:"projection"`
	Criteria           []parityCriterionEvidence `json:"criteria"`
}

type parityAuthorizationManifest struct {
	DBPath              string `json:"db_path"`
	FingerprintArtifact string `json:"fingerprint_artifact"`
	BoundedArtifact     string `json:"bounded_artifact"`
}

func buildActualTargetParityEvidence(ctx context.Context, path string) (parityV2EvidenceSuite, error) {
	data, err := readPrivateParityFile(path, maxParityManifestBytes)
	if err != nil {
		return parityV2EvidenceSuite{}, errors.New("authorization_manifest_invalid")
	}
	var manifest parityAuthorizationManifest
	if decodeStrictJSON(data, &manifest) != nil || manifest.DBPath == "" || manifest.FingerprintArtifact == "" || manifest.BoundedArtifact == "" {
		return parityV2EvidenceSuite{}, errors.New("authorization_manifest_invalid")
	}
	read := func(artifactPath string) (searchParityArtifact, error) {
		raw, readErr := readPrivateParityFile(artifactPath, maxParityArtifactBytes)
		if readErr != nil || validateSearchParityJSON(raw) != nil {
			return searchParityArtifact{}, errors.New("authorization_artifact_invalid")
		}
		var artifact searchParityArtifact
		if decodeStrictJSON(raw, &artifact) != nil || artifact.Status != "passed" || !artifact.Comparison.Equal || !artifact.Tiered.Coverage.Complete {
			return artifact, errors.New("authorization_artifact_invalid")
		}
		return artifact, nil
	}
	fingerprint, err := read(manifest.FingerprintArtifact)
	if err != nil {
		return parityV2EvidenceSuite{}, err
	}
	bounded, err := read(manifest.BoundedArtifact)
	if err != nil {
		return parityV2EvidenceSuite{}, err
	}
	if fingerprint.Tiered.QueryClass != "fingerprint_eligible" || bounded.Tiered.QueryClass != "bounded_verification" || fingerprint.Revision != bounded.Revision || fingerprint.Projection != bounded.Projection {
		return parityV2EvidenceSuite{}, errors.New("authorization_artifact_mismatch")
	}
	database, err := infra.NewImmutableReadDatabase(ctx, manifest.DBPath)
	if err != nil {
		return parityV2EvidenceSuite{}, errors.New("authorization_store_invalid")
	}
	defer database.CloseSharedReadOnly()
	snapshot, err := database.SearchRetirementSnapshot(ctx)
	if err != nil || !snapshot.TargetAdopted || snapshot.ProjectionState != "complete" || snapshot.ProjectionRevision != fingerprint.Projection.Revision || snapshot.ProjectionHighWater != fingerprint.Projection.HighWater {
		return parityV2EvidenceSuite{}, errors.New("authorization_store_invalid")
	}
	revision := apptypes.SearchParityRevision{Commit: fingerprint.Revision.Commit, Dirty: fingerprint.Revision.Dirty}
	projection := apptypes.SearchParityProjection{Revision: snapshot.ProjectionRevision, HighWater: snapshot.ProjectionHighWater, LogicalBytes: snapshot.CanonicalLogicalBytes, PhysicalBytes: snapshot.PhysicalBytes}
	binding, err := apptypes.KeyedSearchParityBinding(snapshot.CursorKey, "target-store", apptypes.SearchParityTargetFields(revision, projection, snapshot.EventCount, snapshot.AuditCount, snapshot.CanonicalLogicalBytes)...)
	if err != nil {
		return parityV2EvidenceSuite{}, errors.New("authorization_store_invalid")
	}
	criterion := func(class string, artifact searchParityArtifact) (parityCriterionEvidence, error) {
		b, e := apptypes.KeyedSearchParityBinding(snapshot.CursorKey, "criterion", class, binding)
		return parityCriterionEvidence{QueryClass: class, CriterionBinding: b, Status: "passed", ComparisonEqual: true, CoverageComplete: true, LegacyLatencyUS: artifact.Legacy.LatencyUS, TieredLatencyUS: artifact.Tiered.LatencyUS}, e
	}
	a, err := criterion("fingerprint_eligible", fingerprint)
	if err != nil {
		return parityV2EvidenceSuite{}, err
	}
	b, err := criterion("bounded_verification", bounded)
	if err != nil {
		return parityV2EvidenceSuite{}, err
	}
	return parityV2EvidenceSuite{SchemaVersion: searchParityV2Schema, AuthorizationScope: "actual_target", TargetStoreBinding: binding, Revision: fingerprint.Revision, Projection: parityProjection{Revision: projection.Revision, HighWater: projection.HighWater, LogicalBytes: projection.LogicalBytes, PhysicalBytes: projection.PhysicalBytes}, Criteria: []parityCriterionEvidence{a, b}}, nil
}

func readPrivateParityFile(path string, limit int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > int64(limit) {
		return nil, errors.New("private parity file permissions")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("private parity file changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("private parity file exceeds limit")
	}
	return data, nil
}

type parityCriterionEvidence struct {
	QueryClass       string `json:"query_class"`
	CriterionBinding string `json:"criterion_binding"`
	Status           string `json:"status"`
	ComparisonEqual  bool   `json:"comparison_equal"`
	CoverageComplete bool   `json:"coverage_complete"`
	LegacyLatencyUS  int64  `json:"legacy_latency_us"`
	TieredLatencyUS  int64  `json:"tiered_latency_us"`
}

func keyedParityBinding(key []byte, purpose string, fields ...string) (string, error) {
	if len(key) < 16 || purpose == "" {
		return "", errors.New("binding_unavailable")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, "traceary:search-parity:v2\x00"+purpose)
	for _, field := range fields {
		_, _ = io.WriteString(mac, fmt.Sprintf("\x00%d:", len(field)))
		_, _ = io.WriteString(mac, field)
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func validParityV2EvidenceShape(s parityV2EvidenceSuite) bool {
	if s.SchemaVersion != searchParityV2Schema || (s.AuthorizationScope != "actual_target" && s.AuthorizationScope != "compatibility_only") || !validOpaqueBinding(s.TargetStoreBinding) ||
		!validCommit(s.Revision.Commit) || s.Revision.Dirty || !validPassedProjection(searchParityArtifact{Projection: s.Projection, Tiered: parityChain{Coverage: parityCoverage{HighWater: s.Projection.HighWater}}}) || len(s.Criteria) != 2 {
		return false
	}
	required := map[string]bool{"fingerprint_eligible": false, "bounded_verification": false}
	for _, criterion := range s.Criteria {
		seen, known := required[criterion.QueryClass]
		if !known || seen || !validOpaqueBinding(criterion.CriterionBinding) || criterion.Status != "passed" || !criterion.ComparisonEqual || !criterion.CoverageComplete || criterion.LegacyLatencyUS <= 0 || criterion.TieredLatencyUS <= 0 {
			return false
		}
		required[criterion.QueryClass] = true
	}
	return required["fingerprint_eligible"] && required["bounded_verification"]
}

func validOpaqueBinding(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

type searchParityManifest struct {
	DBPath           string `json:"db_path"`
	Query            string `json:"query"`
	Workspace        string `json:"workspace,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	Client           string `json:"client,omitempty"`
	Agent            string `json:"agent,omitempty"`
	Kind             string `json:"kind,omitempty"`
	From             string `json:"from,omitempty"`
	To               string `json:"to,omitempty"`
	FailuresOnly     bool   `json:"failures_only,omitempty"`
	LegacyPageSize   int    `json:"legacy_page_size"`
	TieredPageSize   int    `json:"tiered_page_size"`
	SourceRows       int    `json:"source_rows"`
	StoredBytes      int64  `json:"stored_bytes"`
	DecodedBytes     int64  `json:"decoded_bytes"`
	TimeoutMS        int64  `json:"timeout_ms"`
	ExpectedRevision string `json:"expected_revision"`
	ExpectedDirty    *bool  `json:"expected_dirty"`
}

type parityRevision struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

type parityChain struct {
	Pages               int            `json:"pages"`
	ContinuationCount   int            `json:"continuation_count"`
	Members             int            `json:"members"`
	DuplicateCount      int            `json:"duplicate_count"`
	LatencyUS           int64          `json:"latency_us"`
	ElapsedLowerBoundUS int64          `json:"elapsed_lower_bound_us"`
	QueryClass          string         `json:"query_class"`
	ObservedTier        string         `json:"observed_tier"`
	Coverage            parityCoverage `json:"coverage"`
	PartialReason       string         `json:"partial_reason"`
}

type parityCoverage struct {
	Processed int64 `json:"processed"`
	Examined  int64 `json:"examined"`
	HighWater int64 `json:"high_water"`
	Complete  bool  `json:"complete"`
}

type parityProjection struct {
	Revision      int64 `json:"revision"`
	HighWater     int64 `json:"high_water"`
	LogicalBytes  int64 `json:"logical_bytes"`
	PhysicalBytes int64 `json:"physical_bytes"`
}

type parityBudget struct {
	SourceRows   int   `json:"source_rows"`
	StoredBytes  int64 `json:"stored_bytes"`
	DecodedBytes int64 `json:"decoded_bytes"`
	TimeoutMS    int64 `json:"timeout_ms"`
}

func (b parityBudget) Validate() error {
	if b.SourceRows < 1 || b.SourceRows > apptypes.MaxLiteralSearchSourceRows || b.StoredBytes < 1 || b.DecodedBytes < 1 || b.TimeoutMS < 1 || b.TimeoutMS > maxParityTimeoutMS {
		return errors.New("invalid parity budget")
	}
	return nil
}

type validatedSearchParityManifest struct {
	manifest searchParityManifest
	criteria parityCriteria
}

func validateSearchParityManifest(m searchParityManifest) (validatedSearchParityManifest, error) {
	budget := parityBudget{SourceRows: m.SourceRows, StoredBytes: m.StoredBytes, DecodedBytes: m.DecodedBytes, TimeoutMS: m.TimeoutMS}
	if m.DBPath == "" || strings.TrimSpace(m.Query) == "" || len(m.Query) > apptypes.MaxLiteralSearchQueryBytes || !validCommit(m.ExpectedRevision) || m.ExpectedDirty == nil || *m.ExpectedDirty ||
		m.LegacyPageSize < 1 || m.LegacyPageSize > maxParityPageSize || m.TieredPageSize < 1 || m.TieredPageSize > apptypes.MaxLiteralSearchLimit || budget.Validate() != nil {
		return validatedSearchParityManifest{}, errors.New("manifest_invalid")
	}
	criteria, err := parseParityCriteria(m)
	if err != nil {
		return validatedSearchParityManifest{}, err
	}
	return validatedSearchParityManifest{manifest: m, criteria: criteria}, nil
}

type parityComparison struct {
	LegacyOnly int  `json:"legacy_only"`
	TieredOnly int  `json:"tiered_only"`
	Equal      bool `json:"equal"`
}

type searchParityArtifact struct {
	SchemaVersion       string           `json:"schema_version"`
	ComparisonContract  string           `json:"comparison_contract"`
	Status              string           `json:"status"`
	Revision            parityRevision   `json:"revision"`
	Legacy              parityChain      `json:"legacy"`
	Tiered              parityChain      `json:"tiered"`
	Comparison          parityComparison `json:"comparison"`
	Projection          parityProjection `json:"projection"`
	Budget              parityBudget     `json:"budget"`
	ErrorClass          string           `json:"error_class"`
	ElapsedLowerBoundUS int64            `json:"elapsed_lower_bound_us"`
}

type parityCriteria struct {
	query, workspace, sessionID, client, agent, kind string
	from, to                                         time.Time
	failuresOnly                                     bool
}

func readSearchParityManifest(path string, stdin io.Reader) (searchParityManifest, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = readBoundedManifest(stdin)
	} else {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return searchParityManifest{}, errors.New("manifest_access")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > maxParityManifestBytes {
			return searchParityManifest{}, errors.New("manifest_permissions")
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return searchParityManifest{}, errors.New("manifest_access")
		}
		defer func() { _ = file.Close() }()
		opened, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, opened) {
			return searchParityManifest{}, errors.New("manifest_access")
		}
		data, err = readBoundedManifest(file)
	}
	if err != nil {
		return searchParityManifest{}, errors.New("manifest_access")
	}
	var manifest searchParityManifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return searchParityManifest{}, errors.New("manifest_invalid")
	}
	if err := validateJSONObjectKeys(data, manifestJSONKeys()); err != nil {
		return searchParityManifest{}, errors.New("manifest_invalid")
	}
	if _, err := validateSearchParityManifest(manifest); err != nil {
		return searchParityManifest{}, errors.New("manifest_invalid")
	}
	return manifest, nil
}

func manifestJSONKeys() jsonObjectSchema {
	required := map[string]any{}
	optional := map[string]any{}
	for _, key := range []string{"db_path", "query", "legacy_page_size", "tiered_page_size", "source_rows", "stored_bytes", "decoded_bytes", "timeout_ms", "expected_revision", "expected_dirty"} {
		required[key] = nil
	}
	for _, key := range []string{"workspace", "session_id", "client", "agent", "kind", "from", "to", "failures_only"} {
		optional[key] = nil
	}
	return jsonObjectSchema{required: required, optional: optional}
}

// validateJSONObjectKeys closes encoding/json's case-insensitive field-name
// matching. Each object is checked against its exact, case-sensitive schema.
type jsonObjectSchema struct {
	required     map[string]any
	optional     map[string]any
	arrayElement *jsonObjectSchema
}

func validateJSONObjectKeys(data []byte, schema jsonObjectSchema) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode JSON key schema: %w", err)
	}
	var walk func(any, jsonObjectSchema) error
	walk = func(node any, allowed jsonObjectSchema) error {
		object, ok := node.(map[string]any)
		if !ok {
			return errors.New("JSON object required")
		}
		for key := range allowed.required {
			if _, exists := object[key]; !exists {
				return errors.New("required JSON field missing")
			}
		}
		for key, child := range object {
			nested, exists := allowed.required[key]
			if !exists {
				nested, exists = allowed.optional[key]
			}
			if !exists {
				return errors.New("unknown JSON field")
			}
			if nestedSchema, ok := nested.(jsonObjectSchema); ok {
				if nestedSchema.arrayElement != nil {
					array, ok := child.([]any)
					if !ok {
						return errors.New("JSON array required")
					}
					for _, element := range array {
						if err := walk(element, *nestedSchema.arrayElement); err != nil {
							return err
						}
					}
				} else if err := walk(child, nestedSchema); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(value, schema)
}

func readBoundedManifest(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxParityManifestBytes+1))
	if err != nil || len(data) > maxParityManifestBytes {
		return nil, errors.New("manifest_access")
	}
	return data, nil
}

func decodeStrictJSON(data []byte, target any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode strict JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var parse func() error
	parse = func() error {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read JSON token: %w", err)
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("read JSON object key: %w", err)
				}
				key := keyToken.(string)
				if _, exists := seen[key]; exists {
					return errors.New("duplicate JSON field")
				}
				seen[key] = struct{}{}
				if err := parse(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			if err != nil {
				return fmt.Errorf("close JSON object: %w", err)
			}
			return nil
		case '[':
			for decoder.More() {
				if err := parse(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			if err != nil {
				return fmt.Errorf("close JSON array: %w", err)
			}
			return nil
		}
		return nil
	}
	return parse()
}

func parseParityCriteria(m searchParityManifest) (parityCriteria, error) {
	c := parityCriteria{query: m.Query, workspace: m.Workspace, sessionID: m.SessionID, client: m.Client, agent: m.Agent, kind: m.Kind, failuresOnly: m.FailuresOnly}
	var err error
	if m.From != "" {
		c.from, err = time.Parse(time.RFC3339Nano, m.From)
		if err != nil {
			return parityCriteria{}, errors.New("manifest_invalid")
		}
	}
	if m.To != "" {
		c.to, err = time.Parse(time.RFC3339Nano, m.To)
		if err != nil {
			return parityCriteria{}, errors.New("manifest_invalid")
		}
	}
	if !c.from.IsZero() && !c.to.IsZero() && c.from.After(c.to) {
		return parityCriteria{}, errors.New("manifest_invalid")
	}
	return c, nil
}

func repositoryRevision(ctx context.Context) (parityRevision, error) {
	// Only fixed Git state is returned; command stderr is deliberately discarded.
	head, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return parityRevision{}, errors.New("revision_unavailable")
	}
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=normal")
	status, err := cmd.Output()
	if err != nil {
		return parityRevision{}, errors.New("revision_unavailable")
	}
	return parityRevision{Commit: strings.TrimSpace(string(head)), Dirty: len(bytes.TrimSpace(status)) != 0}, nil
}

var parityRevisionReader = repositoryRevision
var parityProjectionReader = readParityProjection
var legacyParityCollector = collectLegacyParity
var tieredParityCollector = collectTieredParity

func fixedErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	for _, class := range []string{"manifest_access", "manifest_permissions", "manifest_invalid", "revision_unavailable", "revision_mismatch", "progress", "duplicate", "store_unavailable"} {
		if err.Error() == class {
			return class
		}
	}
	return "search_failed"
}

func statusPrecedence(failed, timeout, mismatch bool) string {
	if failed {
		return "failed"
	}
	if timeout {
		return "timeout"
	}
	if mismatch {
		return "mismatch"
	}
	return "passed"
}

func validCommit(commit string) bool {
	if len(commit) != 40 {
		return false
	}
	for _, r := range commit {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func runSearchParity(ctx context.Context, manifest searchParityManifest) searchParityArtifact {
	started := time.Now()
	artifact := searchParityArtifact{SchemaVersion: searchParitySchema, ComparisonContract: membershipSetContract,
		Budget: parityBudget{SourceRows: manifest.SourceRows, StoredBytes: manifest.StoredBytes, DecodedBytes: manifest.DecodedBytes, TimeoutMS: manifest.TimeoutMS}}
	validated, err := validateSearchParityManifest(manifest)
	if err != nil {
		artifact.Budget = parityBudget{
			SourceRows:   min(max(manifest.SourceRows, 1), apptypes.MaxLiteralSearchSourceRows),
			StoredBytes:  max(manifest.StoredBytes, 1),
			DecodedBytes: max(manifest.DecodedBytes, 1),
			TimeoutMS:    min(max(manifest.TimeoutMS, 1), maxParityTimeoutMS),
		}
		artifact.Status = "failed"
		artifact.ErrorClass = fixedErrorClass(err)
		return finalizeParityOutcome(artifact)
	}
	criteria := validated.criteria
	revision, err := parityRevisionReader(ctx)
	artifact.Revision = revision
	if err != nil {
		artifact.Status = "failed"
		artifact.ErrorClass = "revision_unavailable"
		return finalizeParityOutcome(artifact)
	}
	if revision.Commit != manifest.ExpectedRevision || revision.Dirty || manifest.ExpectedDirty == nil || *manifest.ExpectedDirty {
		artifact.Status = "failed"
		artifact.ErrorClass = "revision_mismatch"
		return finalizeParityOutcome(artifact)
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, time.Duration(manifest.TimeoutMS)*time.Millisecond)
	defer cancel()
	probe, err := infra.NewImmutableReadDatabase(deadlineCtx, manifest.DBPath)
	if err != nil {
		artifact.ElapsedLowerBoundUS = max(time.Since(started).Microseconds(), 1)
		if errors.Is(err, context.DeadlineExceeded) {
			artifact.Status = "timeout"
		} else {
			artifact.Status = "failed"
			artifact.ErrorClass = "store_unavailable"
		}
		return finalizeParityOutcome(artifact)
	}
	_ = probe.CloseSharedReadOnly()
	legacySet, legacyErr := legacyParityCollector(deadlineCtx, manifest.DBPath, criteria, manifest.LegacyPageSize, &artifact.Legacy)
	tieredSet, tieredErr := tieredParityCollector(deadlineCtx, manifest.DBPath, criteria, manifest, &artifact.Tiered)
	projection, projectionErr := parityProjectionReader(deadlineCtx, manifest.DBPath)
	artifact.Projection = projection
	timeout := errors.Is(legacyErr, context.DeadlineExceeded) || errors.Is(tieredErr, context.DeadlineExceeded)
	timeout = timeout || errors.Is(projectionErr, context.DeadlineExceeded)
	failed := (legacyErr != nil && !errors.Is(legacyErr, context.DeadlineExceeded)) || (tieredErr != nil && !errors.Is(tieredErr, context.DeadlineExceeded)) || (projectionErr != nil && !errors.Is(projectionErr, context.DeadlineExceeded))
	if failed {
		if projectionErr != nil && legacyErr == nil && tieredErr == nil {
			artifact.ErrorClass = "projection_failed"
		} else if legacyErr != nil && !errors.Is(legacyErr, context.DeadlineExceeded) {
			artifact.ErrorClass = fixedErrorClass(legacyErr)
		} else if tieredErr != nil && !errors.Is(tieredErr, context.DeadlineExceeded) {
			artifact.ErrorClass = fixedErrorClass(tieredErr)
		} else {
			artifact.ErrorClass = "projection_failed"
		}
	}
	if !failed && !timeout {
		for id := range legacySet {
			if _, ok := tieredSet[id]; !ok {
				artifact.Comparison.LegacyOnly++
			}
		}
		for id := range tieredSet {
			if _, ok := legacySet[id]; !ok {
				artifact.Comparison.TieredOnly++
			}
		}
		artifact.Comparison.Equal = artifact.Comparison.LegacyOnly == 0 && artifact.Comparison.TieredOnly == 0
	}
	artifact.Status = statusPrecedence(failed, timeout, !artifact.Comparison.Equal)
	if failed || timeout {
		artifact.ElapsedLowerBoundUS = max(time.Since(started).Microseconds(), 1)
	}
	return finalizeParityOutcome(artifact)
}

func finalizeParityOutcome(artifact searchParityArtifact) searchParityArtifact {
	if artifact.Status == "failed" || artifact.Status == "timeout" {
		artifact.Comparison = parityComparison{}
		artifact.Legacy.LatencyUS = 0
		artifact.Tiered.LatencyUS = 0
		if artifact.ErrorClass != "manifest_invalid" && artifact.ErrorClass != "manifest_access" && artifact.ErrorClass != "manifest_permissions" && artifact.ErrorClass != "revision_unavailable" && artifact.ErrorClass != "revision_mismatch" {
			artifact.ElapsedLowerBoundUS = max(artifact.ElapsedLowerBoundUS, 1)
		}
	}
	return artifact
}

type tieredProgress struct {
	previousProcessed int64
	highWater         int64
	seen              map[string]struct{}
	initialized       bool
	tier              apptypes.LiteralSearchTier
}

type legacyParityPageReader interface {
	SearchLegacyPage(context.Context, apptypes.EventSearchCriteria) ([]*model.Event, error)
}

type tieredParityPageReader interface {
	SearchLiteralPage(context.Context, apptypes.LiteralSearchRequest) (apptypes.LiteralSearchPage, error)
}

func readLegacyParityPage(ctx context.Context, reader legacyParityPageReader, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	page, err := reader.SearchLegacyPage(ctx, criteria)
	if err != nil {
		return nil, fmt.Errorf("read explicit legacy parity page: %w", err)
	}
	return page, nil
}

func readTieredParityPage(ctx context.Context, reader tieredParityPageReader, request apptypes.LiteralSearchRequest) (apptypes.LiteralSearchPage, error) {
	page, err := reader.SearchLiteralPage(ctx, request)
	if err != nil {
		return apptypes.LiteralSearchPage{}, fmt.Errorf("read tiered parity page: %w", err)
	}
	return page, nil
}

func (p *tieredProgress) observe(page apptypes.LiteralSearchPage) error {
	if page.Coverage.ProcessedSources < 0 || page.Coverage.ExaminedSources < 0 || page.Coverage.HighWater < 0 || page.Coverage.ProcessedSources > page.Coverage.HighWater {
		return errors.New("progress")
	}
	if page.Coverage.Complete && (page.Coverage.ProcessedSources != page.Coverage.HighWater || page.Continuation != "" || page.PartialReason != "") {
		return errors.New("progress")
	}
	if p.seen == nil {
		p.seen = make(map[string]struct{})
	}
	if !p.initialized {
		p.highWater = page.Coverage.HighWater
		p.initialized = true
		p.tier = page.Tier
	} else if page.Coverage.HighWater != p.highWater {
		return errors.New("progress")
	}
	if page.Tier != p.tier {
		return errors.New("progress")
	}
	if page.Coverage.Complete {
		return nil
	}
	if page.Continuation == "" || page.Coverage.ProcessedSources <= p.previousProcessed {
		return errors.New("progress")
	}
	if _, exists := p.seen[page.Continuation]; exists {
		return errors.New("progress")
	}
	p.seen[page.Continuation] = struct{}{}
	p.previousProcessed = page.Coverage.ProcessedSources
	return nil
}

func collectLegacyParity(ctx context.Context, path string, c parityCriteria, pageSize int, metrics *parityChain) (map[string]struct{}, error) {
	started := time.Now()
	members := make(map[string]struct{})
	for offset := 0; ; {
		database, openErr := infra.NewImmutableReadDatabase(ctx, path)
		if openErr != nil {
			return members, fmt.Errorf("open immutable legacy page: %w", openErr)
		}
		datasource := infra.NewEventDatasource(database)
		criteria := apptypes.NewEventSearchCriteriaBuilder(pageSize).Query(c.query).
			Workspace(types.Workspace(c.workspace)).SessionID(types.SessionID(c.sessionID)).Client(types.Client(c.client)).
			Agent(types.Agent(c.agent)).Kind(types.EventKind(c.kind)).From(c.from).To(c.to).Offset(offset).
			FailuresOnly(c.failuresOnly).Build()
		page, err := readLegacyParityPage(ctx, datasource, criteria)
		_ = database.CloseSharedReadOnly()
		if err != nil {
			setCensoredLatency(metrics, started, err)
			return members, fmt.Errorf("read legacy page: %w", err)
		}
		metrics.Pages++
		if len(page) == 0 {
			break
		}
		for _, event := range page {
			id := event.EventID().String()
			if _, exists := members[id]; exists {
				metrics.DuplicateCount++
				return members, errors.New("duplicate")
			}
			members[id] = struct{}{}
		}
		next := offset + len(page)
		if next <= offset {
			return members, errors.New("progress")
		}
		offset = next
		if len(page) < pageSize {
			break
		}
	}
	metrics.Members = len(members)
	metrics.LatencyUS = max(time.Since(started).Microseconds(), 1)
	return members, nil
}

func collectTieredParity(ctx context.Context, path string, c parityCriteria, m searchParityManifest, metrics *parityChain) (map[string]struct{}, error) {
	started := time.Now()
	members := make(map[string]struct{})
	continuation := ""
	progress := tieredProgress{}
	if apptypes.CharacterizeLiteralQuery(c.query).Filterable() {
		metrics.QueryClass = "fingerprint_eligible"
	} else {
		metrics.QueryClass = "bounded_verification"
	}
	for {
		database, openErr := infra.NewImmutableReadDatabase(ctx, path)
		if openErr != nil {
			return members, fmt.Errorf("open immutable tiered page: %w", openErr)
		}
		datasource := infra.NewEventDatasource(database)
		builder := apptypes.NewEventSearchCriteriaBuilder(m.TieredPageSize).Query(c.query).
			Workspace(types.Workspace(c.workspace)).SessionID(types.SessionID(c.sessionID)).Client(types.Client(c.client)).
			Agent(types.Agent(c.agent)).Kind(types.EventKind(c.kind)).From(c.from).To(c.to).FailuresOnly(c.failuresOnly)
		page, err := readTieredParityPage(ctx, datasource, apptypes.LiteralSearchRequest{
			Criteria:     builder.Build(),
			Budget:       apptypes.LiteralSearchBudget{SourceRows: m.SourceRows, StoredBytes: m.StoredBytes, DecodedBytes: m.DecodedBytes},
			Continuation: continuation,
		})
		_ = database.CloseSharedReadOnly()
		if err != nil {
			setCensoredLatency(metrics, started, err)
			return members, fmt.Errorf("read tiered page: %w", err)
		}
		metrics.Pages++
		if progressErr := progress.observe(page); progressErr != nil {
			return members, progressErr
		}
		metrics.ObservedTier = string(page.Tier)
		metrics.Coverage.Processed = page.Coverage.ProcessedSources
		metrics.Coverage.Examined += page.Coverage.ExaminedSources
		metrics.Coverage.HighWater = page.Coverage.HighWater
		metrics.Coverage.Complete = page.Coverage.Complete
		metrics.PartialReason = page.PartialReason
		for _, event := range page.Events {
			id := event.Metadata().EventID().String()
			if _, exists := members[id]; exists {
				metrics.DuplicateCount++
				return members, errors.New("duplicate")
			}
			members[id] = struct{}{}
		}
		if page.Continuation == "" {
			break
		}
		continuation = page.Continuation
		metrics.ContinuationCount++
	}
	metrics.Members = len(members)
	metrics.LatencyUS = max(time.Since(started).Microseconds(), 1)
	return members, nil
}

func setCensoredLatency(metrics *parityChain, started time.Time, err error) {
	if err != nil {
		metrics.ElapsedLowerBoundUS = max(time.Since(started).Microseconds(), 1)
	}
}

func readParityProjection(ctx context.Context, path string) (parityProjection, error) {
	db, err := openCompatibleReadOnly(ctx, path)
	if err != nil {
		return parityProjection{}, fmt.Errorf("open parity projection: %w", err)
	}
	defer func() { _ = db.Close() }()
	var p parityProjection
	if err := db.QueryRowContext(ctx, `SELECT query_revision, high_water FROM literal_search_projection_state WHERE singleton=1`).Scan(&p.Revision, &p.HighWater); err != nil {
		return parityProjection{}, fmt.Errorf("read projection revision: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&p.HighWater); err != nil {
		return parityProjection{}, fmt.Errorf("read projection high-water: %w", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT SUM(COALESCE(body_plaintext_bytes,length(body),0)) FROM events),0)+COALESCE((SELECT SUM(COALESCE(command_plaintext_bytes,length(command_text),0)+COALESCE(input_plaintext_bytes,length(input_text),0)+COALESCE(output_plaintext_bytes,length(output_text),0)) FROM command_audits),0)`).Scan(&p.LogicalBytes); err != nil {
		return parityProjection{}, fmt.Errorf("read projection logical bytes: %w", err)
	}
	var pageCount, pageSize int64
	if db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount) == nil && db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize) == nil {
		p.PhysicalBytes = pageCount * pageSize
	}
	if p.PhysicalBytes == 0 {
		return parityProjection{}, errors.New("read projection physical bytes")
	}
	return p, nil
}

func validateSearchParityFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("read search parity artifact")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxParityArtifactBytes+1))
	if err != nil || len(data) > maxParityArtifactBytes {
		return errors.New("read search parity artifact")
	}
	return validateSearchParityJSON(data)
}

func validateSearchParityJSON(data []byte) error {
	if len(data) > maxParityArtifactBytes || rejectDuplicateJSONKeys(data) != nil || validateParityJSONBounds(data) != nil {
		return errors.New("invalid search parity JSON")
	}
	forbidden := map[string]bool{"query": true, "id": true, "event_id": true, "path": true, "db_path": true, "cursor": true, "continuation": true, "error": true, "error_message": true}
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return errors.New("invalid search parity JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing search parity JSON")
	}
	var walk func(any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if forbidden[strings.ToLower(key)] {
					return errors.New("privacy-forbidden artifact field")
				}
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(raw); err != nil {
		return err
	}
	if object, ok := raw.(map[string]any); ok && object["schema_version"] == searchParityV2Schema {
		return validateParityV2JSON(data)
	}
	leaf := func(keys ...string) jsonObjectSchema {
		m := map[string]any{}
		for _, key := range keys {
			m[key] = nil
		}
		return jsonObjectSchema{required: m, optional: map[string]any{}}
	}
	coverageSchema := leaf("processed", "examined", "high_water", "complete")
	chainSchema := leaf("pages", "continuation_count", "members", "duplicate_count", "latency_us", "elapsed_lower_bound_us", "query_class", "observed_tier", "partial_reason")
	chainSchema.required["coverage"] = coverageSchema
	artifactSchema := jsonObjectSchema{required: map[string]any{
		"schema_version": nil, "comparison_contract": nil, "status": nil, "error_class": nil, "elapsed_lower_bound_us": nil,
		"revision": leaf("commit", "dirty"), "legacy": chainSchema, "tiered": chainSchema,
		"comparison": leaf("legacy_only", "tiered_only", "equal"),
		"projection": leaf("revision", "high_water", "logical_bytes", "physical_bytes"),
		"budget":     leaf("source_rows", "stored_bytes", "decoded_bytes", "timeout_ms"),
	}, optional: map[string]any{}}
	if err := validateJSONObjectKeys(data, artifactSchema); err != nil {
		return errors.New("invalid search parity schema")
	}
	var artifact searchParityArtifact
	if err := decodeStrictJSON(data, &artifact); err != nil {
		return errors.New("invalid search parity schema")
	}
	if artifact.SchemaVersion != searchParitySchema || artifact.ComparisonContract != membershipSetContract {
		return errors.New("unsupported search parity contract")
	}
	if artifact.Budget.Validate() != nil {
		return errors.New("incomplete search parity evidence")
	}
	commitValid := validCommit(artifact.Revision.Commit)
	if !validCounters(artifact) {
		return errors.New("negative search parity metric")
	}
	switch artifact.Status {
	case "passed":
		if !commitValid || artifact.Revision.Dirty || artifact.ErrorClass != "" || !completedParityChains(artifact) || !validTieredDiagnostics(artifact.Tiered) || !artifact.Comparison.Equal || artifact.Comparison.LegacyOnly != 0 || artifact.Comparison.TieredOnly != 0 || artifact.ElapsedLowerBoundUS != 0 || !validPassedProjection(artifact) {
			return errors.New("inconsistent passed parity evidence")
		}
	case "mismatch":
		commonLegacy := artifact.Legacy.Members - artifact.Comparison.LegacyOnly
		commonTiered := artifact.Tiered.Members - artifact.Comparison.TieredOnly
		if !commitValid || artifact.Revision.Dirty || artifact.ErrorClass != "" || !completedParityChains(artifact) || !validTieredDiagnostics(artifact.Tiered) || artifact.Comparison.Equal || artifact.Comparison.LegacyOnly+artifact.Comparison.TieredOnly < 1 || commonLegacy < 0 || commonLegacy != commonTiered || artifact.ElapsedLowerBoundUS != 0 || !validPassedProjection(artifact) {
			return errors.New("inconsistent mismatch evidence")
		}
	case "timeout":
		if !commitValid || artifact.Revision.Dirty || artifact.ErrorClass != "" || comparisonClaimsProof(artifact) || !validCensoredEvidence(artifact) {
			return errors.New("inconsistent timeout evidence")
		}
	case "failed":
		allowed := map[string]bool{"manifest_access": true, "manifest_permissions": true, "manifest_invalid": true, "revision_unavailable": true, "revision_mismatch": true, "progress": true, "duplicate": true, "store_unavailable": true, "search_failed": true, "projection_failed": true}
		preflight := artifact.ErrorClass == "manifest_access" || artifact.ErrorClass == "manifest_permissions" || artifact.ErrorClass == "manifest_invalid"
		revisionUnavailable := artifact.ErrorClass == "revision_unavailable"
		revisionMismatch := artifact.ErrorClass == "revision_mismatch"
		revisionException := revisionUnavailable || revisionMismatch
		started := !preflight && !revisionException && artifact.ErrorClass != "revision_mismatch"
		revisionInvalid := (preflight && artifact.Revision != (parityRevision{})) || (revisionUnavailable && artifact.Revision != (parityRevision{})) || (revisionMismatch && !commitValid) || (!preflight && !revisionException && (!commitValid || artifact.Revision.Dirty))
		if !allowed[artifact.ErrorClass] || comparisonClaimsProof(artifact) || revisionInvalid || (started && !validCensoredEvidence(artifact)) || (!started && !pristinePreStoreEvidence(artifact)) {
			return errors.New("invalid fixed error class")
		}
	default:
		return errors.New("invalid search parity status")
	}
	return nil
}

func validateParityV2JSON(data []byte) error {
	leaf := func(keys ...string) jsonObjectSchema {
		required := make(map[string]any, len(keys))
		for _, key := range keys {
			required[key] = nil
		}
		return jsonObjectSchema{required: required, optional: map[string]any{}}
	}
	criterion := leaf("query_class", "criterion_binding", "status", "comparison_equal", "coverage_complete", "legacy_latency_us", "tiered_latency_us")
	criteriaArray := jsonObjectSchema{required: map[string]any{}, optional: map[string]any{}, arrayElement: &criterion}
	schema := jsonObjectSchema{required: map[string]any{
		"schema_version": nil, "authorization_scope": nil, "target_store_binding": nil,
		"revision":   leaf("commit", "dirty"),
		"projection": leaf("revision", "high_water", "logical_bytes", "physical_bytes"),
		// Array element semantics are checked after strict decoding.
		"criteria": criteriaArray,
	}, optional: map[string]any{}}
	if err := validateJSONObjectKeys(data, schema); err != nil {
		return errors.New("invalid search parity v2 schema")
	}
	var suite parityV2EvidenceSuite
	if err := decodeStrictJSON(data, &suite); err != nil {
		return errors.New("invalid search parity v2 schema")
	}
	if !validParityV2EvidenceShape(suite) {
		return errors.New("invalid search parity v2 evidence")
	}
	return nil
}

func validateParityJSONBounds(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	criteriaDepth := -1
	criteriaCount := 0
	var walk func(depth int) error
	walk = func(depth int) error {
		if depth > maxParityJSONDepth {
			return errors.New("JSON nesting exceeds limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read bounded JSON token: %w", err)
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			for decoder.More() {
				key, keyErr := decoder.Token()
				if keyErr != nil {
					return fmt.Errorf("read bounded JSON key: %w", keyErr)
				}
				if key == "criteria" {
					criteriaDepth = depth + 1
				}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if depth == criteriaDepth {
					criteriaCount++
					if criteriaCount > maxParityCriteriaCount {
						return errors.New("criteria count exceeds limit")
					}
				}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		}
		_, err = decoder.Token()
		if err != nil {
			return fmt.Errorf("close bounded JSON value: %w", err)
		}
		return nil
	}
	return walk(0)
}

func pristinePreStoreEvidence(a searchParityArtifact) bool {
	return a.Legacy == (parityChain{}) && a.Tiered == (parityChain{}) &&
		a.Comparison == (parityComparison{}) && a.Projection == (parityProjection{}) &&
		a.ElapsedLowerBoundUS == 0
}

func validCounters(a searchParityArtifact) bool {
	values := []int64{int64(a.Legacy.Pages), int64(a.Legacy.ContinuationCount), int64(a.Legacy.Members), int64(a.Legacy.DuplicateCount), a.Legacy.LatencyUS, a.Legacy.ElapsedLowerBoundUS, int64(a.Tiered.Pages), int64(a.Tiered.ContinuationCount), int64(a.Tiered.Members), int64(a.Tiered.DuplicateCount), a.Tiered.LatencyUS, a.Tiered.ElapsedLowerBoundUS, a.Tiered.Coverage.Processed, a.Tiered.Coverage.Examined, a.Tiered.Coverage.HighWater, int64(a.Comparison.LegacyOnly), int64(a.Comparison.TieredOnly), a.Projection.Revision, a.Projection.HighWater, a.Projection.LogicalBytes, a.Projection.PhysicalBytes, int64(a.Budget.SourceRows), a.Budget.StoredBytes, a.Budget.DecodedBytes, a.Budget.TimeoutMS, a.ElapsedLowerBoundUS}
	for _, value := range values {
		if value < 0 {
			return false
		}
	}
	return a.Legacy.ContinuationCount == 0 && a.Tiered.ContinuationCount <= max(a.Tiered.Pages-1, 0)
}

func validCensoredEvidence(a searchParityArtifact) bool {
	return a.ElapsedLowerBoundUS > 0 && a.Legacy.LatencyUS == 0 && a.Tiered.LatencyUS == 0
}

func validPassedProjection(a searchParityArtifact) bool {
	return a.Projection.Revision >= 0 && a.Projection.HighWater >= 0 && a.Projection.LogicalBytes >= 0 && a.Projection.PhysicalBytes > 0 && a.Projection.HighWater == a.Tiered.Coverage.HighWater
}

func validTieredDiagnostics(chain parityChain) bool {
	classes := map[string]bool{"fingerprint_eligible": true, "bounded_verification": true}
	tiers := map[string]bool{string(apptypes.LiteralSearchTierFingerprint): true, string(apptypes.LiteralSearchTierBoundedVerification): true}
	partials := map[string]bool{"": true, "source_rows": true, "stored_bytes": true, "decoded_bytes": true, "verified_hydration_bytes": true, "result_limit": true}
	classTier := (chain.QueryClass == "fingerprint_eligible" && (chain.ObservedTier == string(apptypes.LiteralSearchTierFingerprint) || chain.ObservedTier == string(apptypes.LiteralSearchTierBoundedVerification))) || (chain.QueryClass == "bounded_verification" && chain.ObservedTier == string(apptypes.LiteralSearchTierBoundedVerification))
	return classes[chain.QueryClass] && tiers[chain.ObservedTier] && classTier && partials[chain.PartialReason] && chain.Coverage.Processed >= 0 && chain.Coverage.Examined >= 0 && chain.Coverage.HighWater >= 0 && chain.Coverage.Processed <= chain.Coverage.HighWater && chain.Coverage.Complete && chain.Coverage.Processed == chain.Coverage.HighWater && chain.PartialReason == "" && chain.ContinuationCount == max(chain.Pages-1, 0)
}

func completedParityChains(a searchParityArtifact) bool {
	return a.Legacy.Pages >= 1 && a.Tiered.Pages >= 1 && a.Legacy.DuplicateCount == 0 && a.Tiered.DuplicateCount == 0 && a.Legacy.LatencyUS > 0 && a.Tiered.LatencyUS > 0 && a.Legacy.ElapsedLowerBoundUS == 0 && a.Tiered.ElapsedLowerBoundUS == 0 && a.Legacy.Members-a.Comparison.LegacyOnly == a.Tiered.Members-a.Comparison.TieredOnly
}

func comparisonClaimsProof(a searchParityArtifact) bool {
	return a.Comparison.Equal || a.Comparison.LegacyOnly != 0 || a.Comparison.TieredOnly != 0
}
