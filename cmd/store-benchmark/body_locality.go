package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
	infra "github.com/duck8823/traceary/infrastructure/sqlite"
	sqliteschema "github.com/duck8823/traceary/schema/sqlite"
)

const (
	bodyLocalitySeed             int64 = 1743
	defaultLocalityRows                = 256
	defaultLocalityBodyBytes           = 8192
	bodyLocalitySchemaVersion          = "traceary.body-locality/v1"
	materialInlineOverProjection       = 2.0
	materialInlineOverSide             = 2.0
	materialSideOverProjection         = 1.5
)

type bodyLocalityOpts struct {
	Dir       string
	Rows      int
	BodyBytes int
	Seed      int64
	Iters     int
}

type bodyLocalityReport struct {
	SchemaVersion    string               `json:"schema_version"`
	Seed             int64                `json:"seed"`
	Rows             int                  `json:"rows"`
	BodyBytes        int                  `json:"body_bytes"`
	Decision         string               `json:"decision"`
	DecisionReason   string               `json:"decision_reason"`
	LiveStoreQueried bool                 `json:"live_store_queried"`
	Corpora          []bodyLocalityCorpus `json:"corpora"`
}

type bodyLocalityCorpus struct {
	Kind    string               `json:"kind"`
	Layouts []bodyLocalityLayout `json:"layouts"`
	Gate    bodyLocalityGate     `json:"gate"`
}

type bodyLocalityLayout struct {
	Name             string             `json:"name"`
	DatabaseBytes    int64              `json:"database_bytes"`
	EventsTableBytes int64              `json:"events_table_bytes,omitempty"`
	EvidenceMethod   string             `json:"evidence_method"`
	Cases            []bodyLocalityCase `json:"cases"`
}

type bodyLocalityCase struct {
	Name      string   `json:"name"`
	WarmP50US int64    `json:"warm_p50_us"`
	ColdP50US int64    `json:"cold_p50_us"`
	QueryPlan []string `json:"query_plan"`
	TableScan bool     `json:"table_scan"`
}

type bodyLocalityGate struct {
	InlineOverProjectionWarm float64 `json:"inline_over_projection_warm"`
	InlineOverSideWarm       float64 `json:"inline_over_side_warm"`
	SideOverProjectionWarm   float64 `json:"side_over_projection_warm"`
	MethodValid              bool    `json:"method_valid"`
	Material                 bool    `json:"material"`
	ScanRegression           bool    `json:"scan_regression"`
}

func runBodyLocality(ctx context.Context, opts bodyLocalityOpts) (bodyLocalityReport, error) {
	if opts.Dir == "" {
		return bodyLocalityReport{}, fmt.Errorf("measure-body-locality directory is required")
	}
	if opts.Rows < 1 || opts.BodyBytes < 1 {
		return bodyLocalityReport{}, fmt.Errorf("locality row count and body bytes must be positive")
	}
	if opts.Seed == 0 {
		opts.Seed = bodyLocalitySeed
	}
	if opts.Iters < 1 {
		opts.Iters = 7
	}
	if err := rejectLiveStoreDir(opts.Dir); err != nil {
		return bodyLocalityReport{}, err
	}
	if _, err := os.Stat(opts.Dir); err == nil {
		return bodyLocalityReport{}, fmt.Errorf("destination already exists")
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return bodyLocalityReport{}, fmt.Errorf("create locality directory: %w", err)
	}
	report := bodyLocalityReport{
		SchemaVersion:    bodyLocalitySchemaVersion,
		Seed:             opts.Seed,
		Rows:             opts.Rows,
		BodyBytes:        opts.BodyBytes,
		LiveStoreQueried: false,
	}
	for _, kind := range []corpusKind{corpusEntropy, corpusRepetitive} {
		corpus, err := measureLocalityCorpus(ctx, opts, kind)
		if err != nil {
			return bodyLocalityReport{}, fmt.Errorf("%s: %w", kind, err)
		}
		report.Corpora = append(report.Corpora, corpus)
	}
	report.Decision, report.DecisionReason = decideBodyLocality(report.Corpora)
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, fmt.Errorf("encode locality report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(opts.Dir, "body-locality.json"), append(out, '\n'), 0o600); err != nil {
		return report, fmt.Errorf("write body-locality.json: %w", err)
	}
	return report, nil
}

func rejectLiveStoreDir(dir string) error {
	live, err := defaultLiveStorePath()
	if err != nil {
		return err
	}
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve locality directory: %w", err)
	}
	same, err := pathsReferToSameStore(resolved, live)
	if err != nil {
		return err
	}
	if same {
		return fmt.Errorf("refusing the default live store %s", live)
	}
	if filepath.Clean(resolved) == filepath.Clean(filepath.Dir(live)) {
		return fmt.Errorf("refusing to write locality fixtures next to the live store %s", live)
	}
	return nil
}

func measureLocalityCorpus(ctx context.Context, opts bodyLocalityOpts, kind corpusKind) (bodyLocalityCorpus, error) {
	inlinePath := filepath.Join(opts.Dir, string(kind)+"-inline.db")
	sidePath := filepath.Join(opts.Dir, string(kind)+"-side.db")
	if err := createLocalityInlineStore(ctx, inlinePath, kind, opts.Seed, opts.Rows, opts.BodyBytes); err != nil {
		return bodyLocalityCorpus{}, err
	}
	if err := extractBodiesToSideTable(ctx, inlinePath, sidePath); err != nil {
		return bodyLocalityCorpus{}, err
	}
	inlineLayout, err := measureLocalityLayout(ctx, inlinePath, "inline", opts.Iters)
	if err != nil {
		return bodyLocalityCorpus{}, fmt.Errorf("measure inline: %w", err)
	}
	sideLayout, err := measureLocalityLayout(ctx, sidePath, "side_table", opts.Iters)
	if err != nil {
		return bodyLocalityCorpus{}, fmt.Errorf("measure side: %w", err)
	}
	corpus := bodyLocalityCorpus{Kind: string(kind), Layouts: []bodyLocalityLayout{inlineLayout, sideLayout}}
	corpus.Gate = computeLocalityGate(inlineLayout, sideLayout)
	return corpus, nil
}

func createLocalityInlineStore(ctx context.Context, path string, kind corpusKind, seed int64, rows, bodyBytes int) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("destination already exists")
	}
	migrations, err := sqliteschema.Migrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	database := infra.NewDatabase(path, migrations)
	if err := infra.NewStoreManagementDatasource(database).Initialize(ctx); err != nil {
		return fmt.Errorf("initialize locality store: %w", err)
	}
	events := infra.NewEventDatasource(database)
	for index := 0; index < rows; index++ {
		body := localityBody(kind, seed, index, bodyBytes)
		createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(index) * time.Millisecond)
		event := model.EventOf(
			types.EventID(fmt.Sprintf("%s-%08d", kind, index)),
			types.EventKindPrompt,
			types.Client("cli"),
			types.Agent("codex"),
			types.SessionID("locality"),
			types.Workspace("locality"),
			body,
			createdAt,
		)
		if err := events.Save(ctx, event); err != nil {
			return fmt.Errorf("save %s row %d: %w", kind, index, err)
		}
	}
	return checkpointAndVacuum(ctx, path)
}

func localityBody(kind corpusKind, seed int64, index, bodyBytes int) string {
	if bodyBytes < 1 {
		bodyBytes = defaultLocalityBodyBytes
	}
	switch kind {
	case corpusEntropy:
		var b strings.Builder
		b.Grow(bodyBytes)
		for b.Len() < bodyBytes {
			b.WriteString(corpusBody(corpusEntropy, seed, index*1024+b.Len(), bodyBytes))
		}
		return b.String()[:bodyBytes]
	case corpusRepetitive:
		return strings.Repeat("redacted synthetic payload ", (bodyBytes/len("redacted synthetic payload "))+1)[:bodyBytes]
	default:
		return corpusBody(kind, seed, index, bodyBytes)
	}
}

func extractBodiesToSideTable(ctx context.Context, inlinePath, sidePath string) error {
	if err := copyFile(inlinePath, sidePath); err != nil {
		return fmt.Errorf("copy inline store: %w", err)
	}
	db, err := sql.Open("sqlite", sidePath)
	if err != nil {
		return fmt.Errorf("open side store: %w", err)
	}
	defer func() { _ = db.Close() }()
	stmts := []string{
		`CREATE TABLE event_bodies (event_id TEXT PRIMARY KEY, body TEXT NOT NULL)`,
		`INSERT INTO event_bodies (event_id, body) SELECT id, body FROM events`,
		`UPDATE events SET body = ''`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("extract bodies: %w", err)
		}
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close side store before vacuum: %w", err)
	}
	return checkpointAndVacuum(ctx, sidePath)
}

func checkpointAndVacuum(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open store for vacuum: %w", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	}
	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy bytes: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}
	return nil
}

func measureLocalityLayout(ctx context.Context, path, name string, iterations int) (bodyLocalityLayout, error) {
	layout := bodyLocalityLayout{Name: name, EvidenceMethod: "pragma"}
	if info, err := os.Stat(path); err == nil {
		layout.DatabaseBytes = info.Size()
	}
	if bytes, ok, err := eventsTableBytes(ctx, path); err != nil {
		return layout, err
	} else if ok {
		layout.EventsTableBytes = bytes
		layout.EvidenceMethod = "dbstat"
	}
	queries := localityQueries()
	if name == "inline" {
		queries = append([]localityQuery{projectionMetaQuery(5000), projectionMetaQuery(200)}, queries...)
	}
	for _, query := range queries {
		result, err := benchmark(ctx, path, iterations, query.Name, query.SQL, nil)
		if err != nil {
			return layout, fmt.Errorf("%s: %w", query.Name, err)
		}
		layout.Cases = append(layout.Cases, bodyLocalityCase{
			Name:      query.Name,
			WarmP50US: result.WarmP50US,
			ColdP50US: result.ColdP50US,
			QueryPlan: result.QueryPlan,
			TableScan: planIsTableScan(result.QueryPlan),
		})
	}
	return layout, nil
}

type localityQuery struct {
	Name string
	SQL  string
}

func projectionMetaQuery(limit int) localityQuery {
	return localityQuery{
		Name: fmt.Sprintf("projection_meta_%d", limit),
		SQL: fmt.Sprintf(`SELECT id, kind, client, agent, session_id, workspace, source_hook, created_at,
       body_original_bytes, body_stored_bytes, body_ingest_truncated,
       body_storage_truncated, body_metadata_version
  FROM event_metadata_projection ORDER BY created_at_norm DESC, id DESC LIMIT %d`, limit),
	}
}

func localityQueries() []localityQuery {
	return []localityQuery{
		{
			Name: "events_meta_5000",
			SQL: `SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes, e.body_ingest_truncated,
       e.body_storage_truncated, e.body_metadata_version
  FROM events e ORDER BY e.created_at_norm DESC, e.id DESC LIMIT 5000`,
		},
		{
			Name: "events_meta_200",
			SQL: `SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes, e.body_ingest_truncated,
       e.body_storage_truncated, e.body_metadata_version
  FROM events e ORDER BY e.created_at_norm DESC, e.id DESC LIMIT 200`,
		},
		{
			Name: "events_id_only_5000",
			SQL:  `SELECT e.id FROM events e ORDER BY e.created_at_norm DESC, e.id DESC LIMIT 5000`,
		},
	}
}

func eventsTableBytes(ctx context.Context, path string) (int64, bool, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return 0, false, fmt.Errorf("open dbstat store: %w", err)
	}
	defer func() { _ = db.Close() }()
	var bytes int64
	err = db.QueryRowContext(ctx, `SELECT SUM(pgsize) FROM dbstat WHERE name = 'events'`).Scan(&bytes)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: dbstat") || strings.Contains(err.Error(), "no such module") {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("dbstat events: %w", err)
	}
	return bytes, true, nil
}

func planIsTableScan(plan []string) bool {
	for _, line := range plan {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "scan") {
			continue
		}
		if strings.Contains(lower, "using index") || strings.Contains(lower, "using covering index") {
			continue
		}
		return true
	}
	return false
}

func computeLocalityGate(inline, side bodyLocalityLayout) bodyLocalityGate {
	projection := caseWarm(inline, "projection_meta_5000")
	inlineEvents := caseWarm(inline, "events_meta_5000")
	sideEvents := caseWarm(side, "events_meta_5000")
	gate := bodyLocalityGate{}
	if projection > 0 {
		gate.InlineOverProjectionWarm = float64(inlineEvents) / float64(projection)
		gate.SideOverProjectionWarm = float64(sideEvents) / float64(projection)
	}
	if sideEvents > 0 {
		gate.InlineOverSideWarm = float64(inlineEvents) / float64(sideEvents)
	}
	gate.ScanRegression = caseTableScan(inline, "events_meta_5000") || caseTableScan(side, "events_meta_5000")
	gate.MethodValid = projection > 0 && gate.InlineOverProjectionWarm >= materialInlineOverProjection
	gate.Material = gate.MethodValid && !gate.ScanRegression &&
		gate.InlineOverSideWarm >= materialInlineOverSide &&
		gate.SideOverProjectionWarm <= materialSideOverProjection
	return gate
}

func caseWarm(layout bodyLocalityLayout, name string) int64 {
	for _, item := range layout.Cases {
		if item.Name == name {
			return item.WarmP50US
		}
	}
	return 0
}

func caseTableScan(layout bodyLocalityLayout, name string) bool {
	for _, item := range layout.Cases {
		if item.Name == name {
			return item.TableScan
		}
	}
	return false
}

func decideBodyLocality(corpora []bodyLocalityCorpus) (string, string) {
	var entropy, repetitive *bodyLocalityGate
	for i := range corpora {
		switch corpora[i].Kind {
		case string(corpusEntropy):
			entropy = &corpora[i].Gate
		case string(corpusRepetitive):
			repetitive = &corpora[i].Gate
		}
	}
	if entropy == nil || repetitive == nil {
		return "not_material", "measurement did not produce both entropy and repetitive corpora"
	}
	if entropy.ScanRegression || repetitive.ScanRegression {
		return "not_material", "a metadata query moved to a table scan"
	}
	if !entropy.MethodValid {
		return "not_material", "scratch entropy corpus did not reproduce a ≥2x inline-over-projection gap, so a side-table win would be unfalsifiable here"
	}
	if entropy.Material && repetitive.Material {
		return "material", "side table recovered the projection-class gap on both overflow (entropy) and compressed (repetitive) bodies"
	}
	if entropy.Material && !repetitive.Material {
		return "not_material", "side table only helped uncompressed overflow bodies; after the #1685 codec the compressed corpus no longer pays a material inline penalty"
	}
	return "not_material", "side table did not recover a projection-class metadata scan on the overflow corpus"
}
