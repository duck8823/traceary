//nolint:wrapcheck,errcheck,revive // Scrub diagnostics retain the exact SQLite operation that failed.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain"
)

// SQLiteCompactionBuilder deliberately bypasses Database initialization.
type SQLiteCompactionBuilder struct {
	Filter application.CompactFilter
}

// SetCompactFilter installs the copy-filter for the next Build.
func (b *SQLiteCompactionBuilder) SetCompactFilter(filter application.CompactFilter) {
	if b == nil {
		return
	}
	b.Filter = filter
}

func (b SQLiteCompactionBuilder) Build(ctx context.Context, source, candidate string) error {
	if filepathDir(source) != filepathDir(candidate) {
		return errors.New("VACUUM INTO candidate must be beside source")
	}
	candidateID, err := inspectRegularFile(candidate)
	if err != nil {
		return fmt.Errorf("inspect prepared compaction candidate: %w", err)
	}
	if candidateID.Size != 0 {
		return errors.New("prepared compaction candidate must be empty before VACUUM INTO")
	}
	sourceDB, err := openDirectReadOnly(ctx, source)
	if err != nil {
		return err
	}
	if err := VerifyStoreCompatibility(ctx, sourceDB); err != nil {
		_ = sourceDB.Close()
		return fmt.Errorf("source compatibility: %w", err)
	}
	if err := sourceDB.Close(); err != nil {
		return err
	}
	work := candidate + ".work"
	_ = os.Remove(work)
	removeSQLiteSidecars(work)
	if err := copyRegularFile(source, work); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(work)
		removeSQLiteSidecars(work)
	}()
	if b.Filter.AfterClone != nil {
		if err := b.Filter.AfterClone(ctx, work); err != nil {
			return fmt.Errorf("compact force cover: %w", err)
		}
		removeSQLiteSidecars(work)
		if !b.Filter.Cutoff.IsZero() {
			gate, inspectErr := (SQLiteCompactionBuilder{}).InspectBodyGate(ctx, work, b.Filter.Cutoff)
			if inspectErr != nil {
				return fmt.Errorf("re-inspect work copy after force cover: %w", inspectErr)
			}
			if gate.UnrefinedSessions > 0 {
				return fmt.Errorf("compact --force cover left %d unrefined session(s)", gate.UnrefinedSessions)
			}
		}
	}
	if err := applyCopyFilters(ctx, work, b.Filter); err != nil {
		return err
	}
	removeSQLiteSidecars(work)
	// Run VACUUM from a transient writable database with the filtered work
	// copy attached as immutable. The original source is never opened as a
	// writer.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE `+quoteSQLiteStringLiteral(sqliteImmutableDSN(work))+` AS compact_source`); err != nil {
		return fmt.Errorf("attach immutable compaction work copy: %w", err)
	}
	if _, err := db.ExecContext(ctx, `VACUUM compact_source INTO `+quoteSQLiteStringLiteral(candidate)); err != nil {
		return fmt.Errorf("VACUUM INTO candidate: %w", err)
	}
	return nil
}

func (SQLiteCompactionBuilder) ClassifyCandidate(ctx context.Context, source, candidate string) (domain.CandidateCondition, error) {
	if err := (SQLiteCompactionBuilder{}).VerifyPair(ctx, source, candidate); err == nil {
		return domain.CandidateConditionComplete, nil
	}
	// The use case calls this only after matching the candidate's inode to the
	// identity durably recorded at CandidatePrepared.  Once ownership is proven
	// that way, SQLite validity is not an ownership signal: a valid database can
	// still be an interrupted or otherwise incomplete VACUUM output.
	return domain.CandidateConditionOwnedIncomplete, nil
}

func (SQLiteCompactionBuilder) Sync(_ context.Context, candidate string) error {
	if err := syncFile(candidate); err != nil {
		return fmt.Errorf("sync compaction candidate: %w", err)
	}
	return syncDirectory(filepathDir(candidate))
}

func (SQLiteCompactionBuilder) VerifyPair(ctx context.Context, source, candidate string) error {
	sourceDB, err := openDirectReadOnly(ctx, source)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer sourceDB.Close()
	candidateDB, err := openDirectReadOnly(ctx, candidate)
	if err != nil {
		return fmt.Errorf("open candidate: %w", err)
	}
	defer candidateDB.Close()
	// Both files pass the exact same compatibility policy; neither result is
	// inferred from the other file.
	if err := VerifyStoreCompatibility(ctx, sourceDB); err != nil {
		return fmt.Errorf("source compatibility: %w", err)
	}
	if err := VerifyStoreCompatibility(ctx, candidateDB); err != nil {
		return fmt.Errorf("candidate compatibility: %w", err)
	}
	sourceEvents, err := tableExists(ctx, sourceDB, "events")
	if err != nil {
		return err
	}
	candidateEvents, err := tableExists(ctx, candidateDB, "events")
	if err != nil {
		return err
	}
	if sourceEvents || candidateEvents {
		return verifyFilteredCandidate(ctx, sourceDB, candidateDB)
	}
	left, err := scrubStore(ctx, sourceDB)
	if err != nil {
		return fmt.Errorf("scrub source: %w", err)
	}
	right, err := scrubStore(ctx, candidateDB)
	if err != nil {
		return fmt.Errorf("scrub candidate: %w", err)
	}
	if left != right {
		return fmt.Errorf("candidate logical digest mismatch: source=%+v candidate=%+v", left, right)
	}
	return nil
}

func verifyFilteredCandidate(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	if _, err := scrubStore(ctx, sourceDB); err != nil {
		return fmt.Errorf("scrub source: %w", err)
	}
	if _, err := scrubStore(ctx, candidateDB); err != nil {
		return fmt.Errorf("scrub candidate: %w", err)
	}
	present, err := legacySearchFamilyPresent(ctx, candidateDB)
	if err != nil {
		return err
	}
	if present {
		return errors.New("candidate still carries the retired search index family")
	}
	sourceEvents, err := eventVerifyMap(ctx, sourceDB)
	if err != nil {
		return fmt.Errorf("read source events: %w", err)
	}
	candidateEvents, err := eventVerifyMap(ctx, candidateDB)
	if err != nil {
		return fmt.Errorf("read candidate events: %w", err)
	}
	remainingFingerprints := map[string]struct{}{}
	for id, got := range candidateEvents {
		want, ok := sourceEvents[id]
		if !ok {
			return fmt.Errorf("candidate invented event %s", id)
		}
		if got.ident != want.ident {
			return fmt.Errorf("candidate changed identity of event %s", id)
		}
		if got.availability == "available" {
			if want.availability != "available" {
				return fmt.Errorf("candidate revived event %s", id)
			}
			if got.fingerprint != want.fingerprint {
				return fmt.Errorf("candidate rewrote body of event %s", id)
			}
		}
		remainingFingerprints[got.fingerprint] = struct{}{}
	}
	for id, want := range sourceEvents {
		if _, kept := candidateEvents[id]; kept {
			continue
		}
		if want.fingerprint == "" {
			return fmt.Errorf("candidate dropped unique event %s", id)
		}
		if _, ok := remainingFingerprints[want.fingerprint]; !ok {
			return fmt.Errorf("candidate dropped unique event %s", id)
		}
	}
	return nil
}

type eventIdentity struct {
	Kind      string
	SessionID string
	CreatedAt string
	Agent     string
	Client    string
	Workspace string
}

type eventVerifyRecord struct {
	ident        eventIdentity
	availability string
	fingerprint  string
}

func eventVerifyMap(ctx context.Context, db *sql.DB) (map[string]eventVerifyRecord, error) {
	hasAvail, err := databaseColumnExists(ctx, db, "events", "body_availability")
	if err != nil {
		return nil, err
	}
	hasSHA, err := databaseColumnExists(ctx, db, "events", "body_sha256")
	if err != nil {
		return nil, err
	}
	hasCodec, err := databaseColumnExists(ctx, db, "events", "body_codec")
	if err != nil {
		return nil, err
	}
	query := `SELECT id, kind, COALESCE(session_id,''), created_at, COALESCE(agent,''), COALESCE(client,''), COALESCE(workspace,''), `
	if hasAvail {
		query += `COALESCE(body_availability,'available'), `
	} else {
		query += `'available', `
	}
	query += `body`
	if hasCodec {
		query += `, body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes`
	} else {
		query += `, NULL, NULL, NULL, NULL`
	}
	if hasSHA {
		query += `, body_sha256`
	} else {
		query += `, NULL`
	}
	query += ` FROM events`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]eventVerifyRecord{}
	for rows.Next() {
		var id string
		var rec eventVerifyRecord
		var row payloadRow
		if err := rows.Scan(
			&id,
			&rec.ident.Kind,
			&rec.ident.SessionID,
			&rec.ident.CreatedAt,
			&rec.ident.Agent,
			&rec.ident.Client,
			&rec.ident.Workspace,
			&rec.availability,
			&row.Stored,
			&row.Codec,
			&row.FormatVersion,
			&row.PlaintextBytes,
			&row.StoredBytes,
			&row.SHA256,
		); err != nil {
			return nil, err
		}
		rec.fingerprint = contentFingerprint(row)
		out[id] = rec
	}
	return out, rows.Err()
}

func contentFingerprint(row payloadRow) string {
	plain, err := row.decode(maxDecodedPayloadBytes)
	if err == nil {
		sum := sha256.Sum256(plain)
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(row.Stored)
	return hex.EncodeToString(sum[:])
}

type storeScrub struct {
	Schema string
	Tables string
}

func scrubStore(ctx context.Context, db *sql.DB) (storeScrub, error) {
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return storeScrub{}, fmt.Errorf("integrity_check=%q: %w", integrity, err)
	}
	var fkCount int
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return storeScrub{}, err
	}
	for rows.Next() {
		fkCount++
	}
	if err := rows.Close(); err != nil {
		return storeScrub{}, err
	}
	if fkCount != 0 {
		return storeScrub{}, fmt.Errorf("foreign_key_check found %d violations", fkCount)
	}
	schema, err := schemaDigest(ctx, db)
	if err != nil {
		return storeScrub{}, err
	}
	tables, err := logicalDigest(ctx, db, compactLogicalSkipTables())
	if err != nil {
		return storeScrub{}, err
	}
	if err := scrubPayloadCodecs(ctx, db); err != nil {
		return storeScrub{}, err
	}
	return storeScrub{Schema: schema, Tables: tables}, nil
}

func schemaDigest(ctx context.Context, db *sql.DB) (string, error) {
	skip := compactLogicalSkipTables()
	rows, err := db.QueryContext(ctx, `SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_schema ORDER BY type,name,tbl_name,sql`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var typ, name, table, ddl string
		if err := rows.Scan(&typ, &name, &table, &ddl); err != nil {
			return "", err
		}
		if skip[name] || skip[table] {
			continue
		}
		writeFramed(h, []byte(typ), []byte(name), []byte(table), []byte(ddl))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func compactLogicalSkipTables() map[string]bool {
	skip := map[string]bool{"event_content_dedupe_archive": true}
	for _, name := range legacySearchFamilyTables {
		skip[name] = true
	}
	return skip
}

func logicalDigest(ctx context.Context, db *sql.DB, skip map[string]bool) (string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND (name NOT LIKE 'sqlite_%' OR name='sqlite_sequence') ORDER BY name`)
	if err != nil {
		return "", err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", err
		}
		if skip[name] {
			continue
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		return "", err
	}
	out := sha256.New()
	for _, table := range tables {
		columns, err := tableColumns(ctx, db, table)
		if err != nil {
			return "", err
		}
		query := `SELECT ` + joinQuotedIdentifiers(columns) + ` FROM ` + quoteIdentifier(table)
		r, err := db.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		var count uint64
		var xor [32]byte
		var sums [4]uint64
		for r.Next() {
			values := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				_ = r.Close()
				return "", err
			}
			h := sha256.New()
			for _, v := range values {
				writeSQLValue(h, v)
			}
			digest := h.Sum(nil)
			count++
			for i := range xor {
				xor[i] ^= digest[i]
			}
			for i := 0; i < 4; i++ {
				sums[i] += binary.BigEndian.Uint64(digest[i*8 : (i+1)*8])
			}
		}
		if err := r.Close(); err != nil {
			return "", err
		}
		binary.Write(out, binary.BigEndian, count)
		writeFramed(out, []byte(table), []byte(strings.Join(columns, "\x00")), xor[:])
		for _, sum := range sums {
			binary.Write(out, binary.BigEndian, sum)
		}
	}
	return hex.EncodeToString(out.Sum(nil)), nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteIdentifier(table)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type col struct {
		n    int
		name string
	}
	var cols []col
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, col{cid, name})
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].n < cols[j].n })
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.name
	}
	return out, rows.Err()
}

func writeSQLValue(h hash.Hash, v any) {
	switch x := v.(type) {
	case nil:
		writeFramed(h, []byte("n"))
	case int64:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(x))
		writeFramed(h, []byte("i"), b[:])
	case float64:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(x))
		writeFramed(h, []byte("f"), b[:])
	case string:
		writeFramed(h, []byte("s"), []byte(x))
	case []byte:
		writeFramed(h, []byte("b"), x)
	default:
		writeFramed(h, []byte(fmt.Sprintf("%T:%v", v, v)))
	}
}
func writeFramed(h hash.Hash, parts ...[]byte) {
	var b [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(b[:], uint64(len(p)))
		h.Write(b[:])
		h.Write(p)
	}
}
func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func joinQuotedIdentifiers(s []string) string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = quoteIdentifier(v)
	}
	return strings.Join(out, ",")
}

func scrubPayloadCodecs(ctx context.Context, db *sql.DB) error {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='payload_rehearsal_rows')`).Scan(&exists); err != nil || exists == 0 {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT payload,codec,format_version,plaintext_bytes,stored_bytes,payload_sha256 FROM payload_rehearsal_rows`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r payloadRow
		if err := rows.Scan(&r.Stored, &r.Codec, &r.FormatVersion, &r.PlaintextBytes, &r.StoredBytes, &r.SHA256); err != nil {
			return err
		}
		if _, err := r.decode(maxDecodedPayloadBytes); err != nil {
			return fmt.Errorf("payload codec scrub: %w", err)
		}
	}
	return rows.Err()
}

// requireStaticSearchState re-checks at apply time, after the exclusive lease,
// what Plan checked before the digest. The two are separated by an operator
// decision, so the family can be removed — or reappear from a restored
// backup — in between.
//
// This replaces the former search_maintenance_control phase check. Migration
// 052 drops that table, so no store reaching this code can be mid-transition
// any more, and a partially retired store is caught by the family check with
// an error the operator can act on.
func requireStaticSearchState(ctx context.Context, db *sql.DB) error {
	present, err := legacySearchFamilyPresent(ctx, db)
	if err != nil {
		return err
	}
	if present {
		return errors.New("legacy search index family is still present; store compact drops it during the copy")
	}
	return nil
}
func openDirectReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteImmutableDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
func directSQLiteRWDSN(path string) string {
	v := url.Values{}
	v.Add("mode", "rw")
	v.Add("_pragma", "foreign_keys(1)")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: v.Encode()}).String()
}
func filepathDir(path string) string {
	idx := strings.LastIndexAny(path, "/\\")
	if idx < 0 {
		return "."
	}
	return path[:idx]
}
