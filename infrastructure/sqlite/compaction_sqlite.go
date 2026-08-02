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
)

// SQLiteCompactionBuilder deliberately bypasses Database initialization.
type SQLiteCompactionBuilder struct{}

func (SQLiteCompactionBuilder) Build(ctx context.Context, source, candidate string) error {
	if filepathDir(source) != filepathDir(candidate) {
		return errors.New("VACUUM INTO candidate must be beside source")
	}
	if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
		return errors.New("compaction candidate already exists")
	}
	db, err := sql.Open("sqlite", directSQLiteRWDSN(source))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	if err := VerifyStoreCompatibility(ctx, db); err != nil {
		return fmt.Errorf("source compatibility: %w", err)
	}
	if err := requireStaticSearchState(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO `+quoteSQLiteStringLiteral(candidate)); err != nil {
		return fmt.Errorf("VACUUM INTO candidate: %w", err)
	}
	return nil
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
	tables, err := logicalDigest(ctx, db)
	if err != nil {
		return storeScrub{}, err
	}
	if err := scrubPayloadCodecs(ctx, db); err != nil {
		return storeScrub{}, err
	}
	return storeScrub{Schema: schema, Tables: tables}, nil
}

func schemaDigest(ctx context.Context, db *sql.DB) (string, error) {
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
		writeFramed(h, []byte(typ), []byte(name), []byte(table), []byte(ddl))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func logicalDigest(ctx context.Context, db *sql.DB) (string, error) {
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

func requireStaticSearchState(ctx context.Context, db *sql.DB) error {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='search_maintenance_control')`).Scan(&exists); err != nil || exists == 0 {
		return err
	}
	var phase string
	if err := db.QueryRowContext(ctx, `SELECT phase FROM search_maintenance_control WHERE singleton=1`).Scan(&phase); err != nil {
		return err
	}
	if phase == "retiring" || phase == "restoring" {
		return fmt.Errorf("search maintenance is not static: %s", phase)
	}
	return nil
}
func openDirectReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(path))
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
