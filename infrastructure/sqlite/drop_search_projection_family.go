package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SemanticVerifierDropSearchProjectionFamily is the Layer-2 verifier for
// offline migration 80 (owner #2319).
const SemanticVerifierDropSearchProjectionFamily SemanticVerifierID = "drop_search_projection_family"

const droppedSearchFamilyReaderVersion = 36

// droppedSearchFamilyTables is the v80 absence set: the thirteen search
// projection tables plus the unread recent FTS virtual table (parent rule 5).
var droppedSearchFamilyTables = []string{
	"literal_search_fingerprints",
	"literal_search_projection_state",
	"search_projection_command_aggregates",
	"search_projection_exclusions",
	"search_projection_generation_lifecycle",
	"search_projection_inventory_compat",
	"search_projection_inventory_state",
	"search_projection_recent_documents",
	"search_projection_session_keywords",
	"search_projection_session_summaries",
	"search_projection_source_revision",
	"search_projection_source_sequence",
	"search_projection_state",
	"search_projection_recent_fts",
}

func verifyDropSearchProjectionFamily(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	for _, name := range droppedSearchFamilyTables {
		exists, err := tableExists(ctx, candidateDB, name)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("candidate still has table %s", name)
		}
	}
	kept, err := tableExists(ctx, candidateDB, "event_metadata_projection")
	if err != nil {
		return err
	}
	if !kept {
		return fmt.Errorf("candidate missing table event_metadata_projection")
	}
	var minimumReader int
	if err := candidateDB.QueryRowContext(ctx, `SELECT minimum_reader_version FROM store_format_state WHERE singleton = 1`).Scan(&minimumReader); err != nil {
		return fmt.Errorf("read candidate minimum_reader_version: %w", err)
	}
	if minimumReader != droppedSearchFamilyReaderVersion {
		return fmt.Errorf("candidate minimum_reader_version = %d, want %d", minimumReader, droppedSearchFamilyReaderVersion)
	}
	if err := verifyNoForeignKeysToDroppedSearchFamily(ctx, candidateDB); err != nil {
		return err
	}
	if err := verifyFiveTableConservation(ctx, sourceDB, candidateDB); err != nil {
		return err
	}
	return verifyNoTriggerOrViewReferencesDroppedSearchFamily(ctx, candidateDB)
}

func verifyNoForeignKeysToDroppedSearchFamily(ctx context.Context, db *sql.DB) error {
	dropped := make(map[string]struct{}, len(droppedSearchFamilyTables))
	for _, name := range droppedSearchFamilyTables {
		dropped[name] = struct{}{}
	}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return fmt.Errorf("list candidate tables for foreign-key sweep: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan candidate table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate candidate tables: %w", err)
	}
	for _, table := range tables {
		fkRows, err := db.QueryContext(ctx, `PRAGMA foreign_key_list(`+quoteIdentifier(table)+`)`)
		if err != nil {
			return fmt.Errorf("inspect %s foreign keys: %w", table, err)
		}
		for fkRows.Next() {
			var id, seq int
			var referenced, from, to, onUpdate, onDelete, match string
			if err := fkRows.Scan(&id, &seq, &referenced, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				_ = fkRows.Close()
				return fmt.Errorf("scan %s foreign key: %w", table, err)
			}
			if _, ok := dropped[referenced]; ok {
				_ = fkRows.Close()
				return fmt.Errorf("candidate %s still references dropped table %s", table, referenced)
			}
		}
		err = fkRows.Err()
		_ = fkRows.Close()
		if err != nil {
			return fmt.Errorf("iterate %s foreign keys: %w", table, err)
		}
	}
	return nil
}

func verifyNoTriggerOrViewReferencesDroppedSearchFamily(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT type, name, sql FROM sqlite_master WHERE type IN ('trigger','view') AND sql IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("list candidate triggers and views: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var objectType, name, sqlText string
		if err := rows.Scan(&objectType, &name, &sqlText); err != nil {
			return fmt.Errorf("scan candidate trigger or view: %w", err)
		}
		for _, dropped := range droppedSearchFamilyTables {
			if strings.Contains(sqlText, dropped) {
				return fmt.Errorf("candidate %s %s still references dropped object %s", objectType, name, dropped)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate candidate triggers and views: %w", err)
	}
	return nil
}
