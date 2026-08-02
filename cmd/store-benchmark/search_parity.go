package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
)

const (
	searchParitySchema    = "traceary.search-parity/v1"
	membershipSetContract = "membership_set/v1"
)

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
	ExpectedDirty    bool   `json:"expected_dirty"`
}

type parityRevision struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

type parityChain struct {
	Pages               int   `json:"pages"`
	ContinuationCount   int   `json:"continuation_count"`
	Members             int   `json:"members"`
	DuplicateCount      int   `json:"duplicate_count"`
	LatencyUS           int64 `json:"latency_us,omitempty"`
	ElapsedLowerBoundUS int64 `json:"elapsed_lower_bound_us,omitempty"`
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

type parityComparison struct {
	LegacyOnly int  `json:"legacy_only"`
	TieredOnly int  `json:"tiered_only"`
	Equal      bool `json:"equal"`
}

type searchParityArtifact struct {
	SchemaVersion      string           `json:"schema_version"`
	ComparisonContract string           `json:"comparison_contract"`
	Status             string           `json:"status"`
	Revision           parityRevision   `json:"revision"`
	Legacy             parityChain      `json:"legacy"`
	Tiered             parityChain      `json:"tiered"`
	Comparison         parityComparison `json:"comparison"`
	Projection         parityProjection `json:"projection"`
	Budget             parityBudget     `json:"budget"`
	ErrorClass         string           `json:"error_class,omitempty"`
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
		data, err = io.ReadAll(io.LimitReader(stdin, 1<<20))
	} else {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return searchParityManifest{}, errors.New("manifest_access")
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return searchParityManifest{}, errors.New("manifest_permissions")
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return searchParityManifest{}, errors.New("manifest_access")
	}
	var manifest searchParityManifest
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return searchParityManifest{}, errors.New("manifest_invalid")
	}
	if manifest.DBPath == "" || strings.TrimSpace(manifest.Query) == "" || manifest.ExpectedRevision == "" ||
		manifest.LegacyPageSize < 1 || manifest.TieredPageSize < 1 || manifest.TieredPageSize > apptypes.MaxLiteralSearchLimit ||
		manifest.SourceRows < 1 || manifest.SourceRows > apptypes.MaxLiteralSearchSourceRows || manifest.StoredBytes < 1 ||
		manifest.DecodedBytes < 1 || manifest.TimeoutMS < 1 {
		return searchParityManifest{}, errors.New("manifest_invalid")
	}
	return manifest, nil
}

func decodeStrictJSON(data []byte, target any) error {
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

func runSearchParity(ctx context.Context, manifest searchParityManifest) searchParityArtifact {
	artifact := searchParityArtifact{SchemaVersion: searchParitySchema, ComparisonContract: membershipSetContract,
		Budget: parityBudget{SourceRows: manifest.SourceRows, StoredBytes: manifest.StoredBytes, DecodedBytes: manifest.DecodedBytes, TimeoutMS: manifest.TimeoutMS}}
	criteria, err := parseParityCriteria(manifest)
	if err != nil {
		artifact.Status = "failed"
		artifact.ErrorClass = fixedErrorClass(err)
		return artifact
	}
	revision, err := repositoryRevision(ctx)
	artifact.Revision = revision
	if err != nil || revision.Commit != manifest.ExpectedRevision || revision.Dirty != manifest.ExpectedDirty {
		artifact.Status = "failed"
		artifact.ErrorClass = "revision_mismatch"
		return artifact
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, time.Duration(manifest.TimeoutMS)*time.Millisecond)
	defer cancel()
	probe, err := infra.NewImmutableReadDatabase(deadlineCtx, manifest.DBPath)
	if err != nil {
		artifact.Status = "failed"
		artifact.ErrorClass = "store_unavailable"
		return artifact
	}
	_ = probe.CloseSharedReadOnly()
	legacySet, legacyErr := collectLegacyParity(deadlineCtx, manifest.DBPath, criteria, manifest.LegacyPageSize, &artifact.Legacy)
	tieredSet, tieredErr := collectTieredParity(deadlineCtx, manifest.DBPath, criteria, manifest, &artifact.Tiered)
	artifact.Projection = readParityProjection(deadlineCtx, manifest.DBPath)
	timeout := errors.Is(legacyErr, context.DeadlineExceeded) || errors.Is(tieredErr, context.DeadlineExceeded)
	failed := (legacyErr != nil && !errors.Is(legacyErr, context.DeadlineExceeded)) || (tieredErr != nil && !errors.Is(tieredErr, context.DeadlineExceeded))
	if failed {
		if legacyErr != nil {
			artifact.ErrorClass = fixedErrorClass(legacyErr)
		} else {
			artifact.ErrorClass = fixedErrorClass(tieredErr)
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
	return artifact
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
		page, err := datasource.Search(ctx, c.query, types.Workspace(c.workspace), types.SessionID(c.sessionID), types.Client(c.client), types.Agent(c.agent), types.EventKind(c.kind), c.from, c.to, pageSize, offset, c.failuresOnly)
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
	for {
		database, openErr := infra.NewImmutableReadDatabase(ctx, path)
		if openErr != nil {
			return members, fmt.Errorf("open immutable tiered page: %w", openErr)
		}
		datasource := infra.NewEventDatasource(database)
		builder := apptypes.NewEventSearchCriteriaBuilder(m.TieredPageSize).Query(c.query).
			Workspace(types.Workspace(c.workspace)).SessionID(types.SessionID(c.sessionID)).Client(types.Client(c.client)).
			Agent(types.Agent(c.agent)).Kind(types.EventKind(c.kind)).From(c.from).To(c.to).FailuresOnly(c.failuresOnly)
		page, err := datasource.SearchLiteralPage(ctx, apptypes.LiteralSearchRequest{
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
		for _, event := range page.Events {
			id := event.Metadata().EventID().String()
			if _, exists := members[id]; exists {
				metrics.DuplicateCount++
				return members, errors.New("duplicate")
			}
			members[id] = struct{}{}
		}
		if page.Continuation == "" {
			if !page.Coverage.Complete {
				return members, errors.New("progress")
			}
			break
		}
		if page.Continuation == continuation {
			return members, errors.New("progress")
		}
		if page.Coverage.ProcessedSources <= 0 {
			return members, errors.New("progress")
		}
		continuation = page.Continuation
		metrics.ContinuationCount++
	}
	metrics.Members = len(members)
	metrics.LatencyUS = max(time.Since(started).Microseconds(), 1)
	return members, nil
}

func setCensoredLatency(metrics *parityChain, started time.Time, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		metrics.ElapsedLowerBoundUS = max(time.Since(started).Microseconds(), 1)
	}
}

func readParityProjection(ctx context.Context, path string) parityProjection {
	db, err := openCompatibleReadOnly(ctx, path)
	if err != nil {
		return parityProjection{}
	}
	defer func() { _ = db.Close() }()
	var p parityProjection
	_ = db.QueryRowContext(ctx, `SELECT query_revision, high_water FROM literal_search_projection_state WHERE singleton=1`).Scan(&p.Revision, &p.HighWater)
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&p.HighWater)
	_ = db.QueryRowContext(ctx, `SELECT COALESCE((SELECT SUM(COALESCE(body_plaintext_bytes,length(body),0)) FROM events),0)+COALESCE((SELECT SUM(COALESCE(command_plaintext_bytes,length(command_text),0)+COALESCE(input_plaintext_bytes,length(input_text),0)+COALESCE(output_plaintext_bytes,length(output_text),0)) FROM command_audits),0)`).Scan(&p.LogicalBytes)
	var pageCount, pageSize int64
	if db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount) == nil && db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize) == nil {
		p.PhysicalBytes = pageCount * pageSize
	}
	return p
}

func validateSearchParityFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("read search parity artifact")
	}
	return validateSearchParityJSON(data)
}

func validateSearchParityJSON(data []byte) error {
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
	var artifact searchParityArtifact
	if err := decodeStrictJSON(data, &artifact); err != nil {
		return errors.New("invalid search parity schema")
	}
	if artifact.SchemaVersion != searchParitySchema || artifact.ComparisonContract != membershipSetContract {
		return errors.New("unsupported search parity contract")
	}
	if artifact.Revision.Commit == "" || artifact.Budget.SourceRows < 1 || artifact.Budget.StoredBytes < 1 || artifact.Budget.DecodedBytes < 1 || artifact.Budget.TimeoutMS < 1 {
		return errors.New("incomplete search parity evidence")
	}
	if artifact.Legacy.Pages < 0 || artifact.Tiered.Pages < 0 || artifact.Legacy.Members < 0 || artifact.Tiered.Members < 0 || artifact.Tiered.ContinuationCount < 0 || artifact.Projection.LogicalBytes < 0 || artifact.Projection.PhysicalBytes < 0 {
		return errors.New("negative search parity metric")
	}
	switch artifact.Status {
	case "passed":
		if artifact.ErrorClass != "" || !artifact.Comparison.Equal || artifact.Comparison.LegacyOnly != 0 || artifact.Comparison.TieredOnly != 0 || artifact.Legacy.LatencyUS < 1 || artifact.Tiered.LatencyUS < 1 {
			return errors.New("inconsistent passed parity evidence")
		}
	case "mismatch":
		if artifact.ErrorClass != "" || artifact.Comparison.Equal || artifact.Comparison.LegacyOnly+artifact.Comparison.TieredOnly < 1 {
			return errors.New("inconsistent mismatch evidence")
		}
	case "timeout":
		if artifact.ErrorClass != "" || artifact.Legacy.ElapsedLowerBoundUS+artifact.Tiered.ElapsedLowerBoundUS < 1 {
			return errors.New("inconsistent timeout evidence")
		}
	case "failed":
		allowed := map[string]bool{"manifest_access": true, "manifest_permissions": true, "manifest_invalid": true, "revision_unavailable": true, "revision_mismatch": true, "progress": true, "duplicate": true, "store_unavailable": true, "search_failed": true}
		if !allowed[artifact.ErrorClass] {
			return errors.New("invalid fixed error class")
		}
	default:
		return errors.New("invalid search parity status")
	}
	return nil
}
