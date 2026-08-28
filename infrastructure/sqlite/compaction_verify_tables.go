//nolint:wrapcheck,errcheck,revive // Scrub diagnostics retain the exact SQLite operation that failed.
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
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

func verifySearchProjectionGenerations(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	sourceLife, err := tableExists(ctx, sourceDB, "search_projection_generation_lifecycle")
	if err != nil {
		return err
	}
	candidateLife, err := tableExists(ctx, candidateDB, "search_projection_generation_lifecycle")
	if err != nil {
		return err
	}
	if !sourceLife && !candidateLife {
		return nil
	}
	if sourceLife != candidateLife {
		return fmt.Errorf("search_projection_generation_lifecycle present on one side only")
	}
	for _, table := range terminalProjectionTables {
		sourceHas, err := tableExists(ctx, sourceDB, table.name)
		if err != nil {
			return err
		}
		candidateHas, err := tableExists(ctx, candidateDB, table.name)
		if err != nil {
			return err
		}
		if sourceHas != candidateHas {
			return fmt.Errorf("%s present on one side only", table.name)
		}
	}

	eligible, protected, err := partitionSourceGenerations(ctx, sourceDB)
	if err != nil {
		return err
	}
	for _, table := range terminalProjectionTables {
		sourceHas, err := tableExists(ctx, sourceDB, table.name)
		if err != nil {
			return err
		}
		if !sourceHas {
			continue
		}
		columns, err := tableColumns(ctx, sourceDB, table.name)
		if err != nil {
			return err
		}
		query := `SELECT ` + joinQuotedIdentifiers(columns) + ` FROM ` + quoteIdentifier(table.name) + ` WHERE generation_id = ?`
		for _, g := range protected {
			srcCount, srcDigest, err := digestRows(ctx, sourceDB, columns, query, g)
			if err != nil {
				return fmt.Errorf("digest source %s generation %s: %w", table.name, g, err)
			}
			candCount, candDigest, err := digestRows(ctx, candidateDB, columns, query, g)
			if err != nil {
				return fmt.Errorf("digest candidate %s generation %s: %w", table.name, g, err)
			}
			if srcCount != candCount || srcDigest != candDigest {
				return fmt.Errorf("candidate changed protected search-projection generation %s on %s", g, table.name)
			}
		}
		for _, g := range eligible {
			var exists int
			if err := candidateDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+quoteIdentifier(table.name)+` WHERE generation_id = ?)`, g).Scan(&exists); err != nil {
				return fmt.Errorf("probe candidate %s generation %s: %w", table.name, g, err)
			}
			if exists != 0 {
				return fmt.Errorf("candidate retained terminal search-projection generation %s on %s", g, table.name)
			}
		}
		var sourceTotal, candidateTotal, eligibleTotal int64
		if err := sourceDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdentifier(table.name)).Scan(&sourceTotal); err != nil {
			return err
		}
		if err := candidateDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdentifier(table.name)).Scan(&candidateTotal); err != nil {
			return err
		}
		for _, g := range eligible {
			var n int64
			if err := sourceDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdentifier(table.name)+` WHERE generation_id = ?`, g).Scan(&n); err != nil {
				return err
			}
			eligibleTotal += n
		}
		if candidateTotal != sourceTotal-eligibleTotal {
			return fmt.Errorf("candidate %s row count %d does not equal source %d minus terminal %d", table.name, candidateTotal, sourceTotal, eligibleTotal)
		}
	}
	return verifySearchProjectionLifecycle(ctx, sourceDB, candidateDB)
}

func partitionSourceGenerations(ctx context.Context, sourceDB *sql.DB) (eligible, protected []string, err error) {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT l.generation_id, l.state, l.reclaimed_at,
       COALESCE(s.active_generation_id, ''), COALESCE(s.generation_id, ''), s.state
  FROM search_projection_generation_lifecycle l
  JOIN search_projection_state s ON s.singleton = 1`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	protectedSet := map[string]struct{}{}
	var active string
	for rows.Next() {
		var id, state, reclaimed, activeID, constructing, constructingState string
		if err := rows.Scan(&id, &state, &reclaimed, &activeID, &constructing, &constructingState); err != nil {
			return nil, nil, err
		}
		active = activeID
		underConstruction := id == constructing && (constructingState == "rebuilding" || constructingState == "drifted")
		isEligible := (state == "failed" || state == "abandoned") && id != activeID && !underConstruction && reclaimed == ""
		if isEligible {
			eligible = append(eligible, id)
			continue
		}
		protectedSet[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if active != "" {
		protectedSet[active] = struct{}{}
	}
	for id := range protectedSet {
		protected = append(protected, id)
	}
	return eligible, protected, nil
}

func verifySearchProjectionLifecycle(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	rows, err := sourceDB.QueryContext(ctx, `
SELECT generation_id, state, config_hash, source_revision, high_water, abandoned_at, failure_class, terminal_at
  FROM search_projection_generation_lifecycle`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	sourceIDs := map[string]struct{}{}
	for rows.Next() {
		var id, state, hash, abandoned, failure, terminal string
		var revision, highWater int64
		if err := rows.Scan(&id, &state, &hash, &revision, &highWater, &abandoned, &failure, &terminal); err != nil {
			return err
		}
		sourceIDs[id] = struct{}{}
		var gotState, gotHash, gotAbandoned, gotFailure, gotTerminal string
		var gotRevision, gotHighWater int64
		err := candidateDB.QueryRowContext(ctx, `
SELECT state, config_hash, source_revision, high_water, abandoned_at, failure_class, terminal_at
  FROM search_projection_generation_lifecycle WHERE generation_id = ?`, id).Scan(
			&gotState, &gotHash, &gotRevision, &gotHighWater, &gotAbandoned, &gotFailure, &gotTerminal)
		if err == sql.ErrNoRows {
			return fmt.Errorf("candidate dropped search-projection generation %s", id)
		}
		if err != nil {
			return err
		}
		if gotState != state || gotHash != hash || gotRevision != revision || gotHighWater != highWater ||
			gotAbandoned != abandoned || gotFailure != failure || gotTerminal != terminal {
			return fmt.Errorf("candidate changed search-projection lifecycle for generation %s", id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	candRows, err := candidateDB.QueryContext(ctx, `SELECT generation_id FROM search_projection_generation_lifecycle`)
	if err != nil {
		return err
	}
	defer func() { _ = candRows.Close() }()
	for candRows.Next() {
		var id string
		if err := candRows.Scan(&id); err != nil {
			return err
		}
		if _, ok := sourceIDs[id]; !ok {
			return fmt.Errorf("candidate invented search-projection generation %s", id)
		}
	}
	return candRows.Err()
}
