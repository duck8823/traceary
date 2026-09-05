//nolint:wrapcheck,errcheck,revive // Scrub diagnostics retain the exact SQLite operation that failed.
package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"slices"
)

type hashedRows struct {
	count uint64
	xor   [32]byte
	sums  [4]uint64
}

func hashRows(ctx context.Context, db *sql.DB, columns []string, query string, args ...any) (hashedRows, error) {
	var out hashedRows
	r, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer func() { _ = r.Close() }()
	for r.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := r.Scan(ptrs...); err != nil {
			return out, err
		}
		h := sha256.New()
		for _, v := range values {
			writeSQLValue(h, v)
		}
		digest := h.Sum(nil)
		out.count++
		for i := range out.xor {
			out.xor[i] ^= digest[i]
		}
		for i := 0; i < 4; i++ {
			out.sums[i] += binary.BigEndian.Uint64(digest[i*8 : (i+1)*8])
		}
	}
	if err := r.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func digestRows(ctx context.Context, db *sql.DB, columns []string, query string, args ...any) (count uint64, digest [32]byte, err error) {
	hashed, err := hashRows(ctx, db, columns, query, args...)
	if err != nil {
		return 0, [32]byte{}, err
	}
	return hashed.count, hashed.xor, nil
}

func verifyCommandAudits(ctx context.Context, sourceDB, candidateDB *sql.DB, candidateEvents map[string]eventVerifyRecord) error {
	sourceHas, err := tableExists(ctx, sourceDB, "command_audits")
	if err != nil {
		return err
	}
	candidateHas, err := tableExists(ctx, candidateDB, "command_audits")
	if err != nil {
		return err
	}
	if !sourceHas && !candidateHas {
		return nil
	}
	if sourceHas != candidateHas {
		return fmt.Errorf("command_audits present on one side only")
	}
	sourceCols, err := tableColumns(ctx, sourceDB, "command_audits")
	if err != nil {
		return err
	}
	candidateCols, err := tableColumns(ctx, candidateDB, "command_audits")
	if err != nil {
		return err
	}
	if !slices.Equal(sourceCols, candidateCols) {
		return fmt.Errorf("command_audits column list differs")
	}
	ident := commandAuditIdentColumns(sourceCols)
	query := commandAuditVerifyQuery(ident)
	sourceCur, err := openCommandAuditVerifyCursor(ctx, sourceDB, query, ident)
	if err != nil {
		return fmt.Errorf("read source command audits: %w", err)
	}
	defer sourceCur.close()
	candidateCur, err := openCommandAuditVerifyCursor(ctx, candidateDB, query, ident)
	if err != nil {
		return fmt.Errorf("read candidate command audits: %w", err)
	}
	defer candidateCur.close()
	if err := sourceCur.next(); err != nil {
		return err
	}
	if err := candidateCur.next(); err != nil {
		return err
	}
	for !sourceCur.eof || !candidateCur.eof {
		switch {
		case candidateCur.eof || (!sourceCur.eof && sourceCur.eventID < candidateCur.eventID):
			if _, kept := candidateEvents[sourceCur.eventID]; kept {
				return fmt.Errorf("candidate dropped command audit %s", sourceCur.eventID)
			}
			if err := sourceCur.next(); err != nil {
				return err
			}
		case sourceCur.eof || (!candidateCur.eof && candidateCur.eventID < sourceCur.eventID):
			return fmt.Errorf("candidate invented command audit %s", candidateCur.eventID)
		default:
			if !bytes.Equal(sourceCur.identDigest, candidateCur.identDigest) {
				return fmt.Errorf("candidate changed identity of command audit %s", sourceCur.eventID)
			}
			for i, field := range commandAuditPayloadFields {
				if !bytes.Equal(sourceCur.payloads[i], candidateCur.payloads[i]) {
					return fmt.Errorf("candidate rewrote %s of command audit %s", field, sourceCur.eventID)
				}
			}
			if err := sourceCur.next(); err != nil {
				return err
			}
			if err := candidateCur.next(); err != nil {
				return err
			}
		}
	}
	return nil
}

var commandAuditPayloadFields = []string{"command", "input", "output"}

func commandAuditPayloadSkipSet() map[string]bool {
	return map[string]bool{
		"event_id":     true,
		"command_text": true,
		"input_text":   true,
		"output_text":  true,
	}
}

func commandAuditIdentColumns(cols []string) []string {
	skip := commandAuditPayloadSkipSet()
	ident := make([]string, 0, len(cols))
	for _, col := range cols {
		if skip[col] {
			continue
		}
		ident = append(ident, col)
	}
	return ident
}

func commandAuditVerifyQuery(ident []string) string {
	cols := make([]string, 0, 1+len(ident)+3)
	cols = append(cols, "event_id")
	cols = append(cols, ident...)
	cols = append(cols, "command_text", "input_text", "output_text")
	return `SELECT ` + joinQuotedIdentifiers(cols) + ` FROM command_audits ORDER BY event_id`
}

type commandAuditVerifyCursor struct {
	rows        *sql.Rows
	identCount  int
	eventID     string
	identDigest []byte
	payloads    [][]byte
	eof         bool
}

func openCommandAuditVerifyCursor(ctx context.Context, db *sql.DB, query string, ident []string) (*commandAuditVerifyCursor, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &commandAuditVerifyCursor{rows: rows, identCount: len(ident), payloads: make([][]byte, len(commandAuditPayloadFields))}, nil
}

func (c *commandAuditVerifyCursor) close() {
	if c != nil && c.rows != nil {
		_ = c.rows.Close()
	}
}

func (c *commandAuditVerifyCursor) next() error {
	if c.eof {
		return nil
	}
	if !c.rows.Next() {
		c.eof = true
		c.eventID = ""
		c.identDigest = nil
		return c.rows.Err()
	}
	identValues := make([]any, c.identCount)
	identPtrs := make([]any, c.identCount)
	for i := range identValues {
		identPtrs[i] = &identValues[i]
	}
	dest := make([]any, 0, 1+c.identCount+len(c.payloads))
	dest = append(dest, &c.eventID)
	dest = append(dest, identPtrs...)
	for i := range c.payloads {
		c.payloads[i] = nil
		dest = append(dest, &c.payloads[i])
	}
	if err := c.rows.Scan(dest...); err != nil {
		return err
	}
	h := sha256.New()
	for _, v := range identValues {
		writeSQLValue(h, v)
	}
	c.identDigest = h.Sum(nil)
	return nil
}
