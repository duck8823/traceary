package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/insert_memory_edge.sql
var insertMemoryEdgeQuery string

//go:embed sql/list_memory_edges.sql
var listMemoryEdgesQuery string

// MemoryEdgeDatasource is the SQLite-backed implementation of the
// memory-graph overlay introduced for #573.
type MemoryEdgeDatasource struct {
	db *Database
}

// NewMemoryEdgeDatasource constructs a MemoryEdgeDatasource bound to
// the given database.
func NewMemoryEdgeDatasource(db *Database) *MemoryEdgeDatasource {
	return &MemoryEdgeDatasource{db: db}
}

// Compile-time interface assertions.
var (
	_ model.MemoryEdgeRepository   = (*MemoryEdgeDatasource)(nil)
	_ model.MemoryEdgeQueryService = (*MemoryEdgeDatasource)(nil)
)

// Save persists a new edge row.
func (d *MemoryEdgeDatasource) Save(ctx context.Context, edge *model.MemoryEdge) error {
	if edge == nil {
		return xerrors.Errorf("edge must not be nil")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for memory edge save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	validToValue := nullableString("")
	if to, ok := edge.ValidTo().Value(); ok {
		validToValue = nullableString(formatMemoryValidityTimestamp(to))
	}

	if _, err := db.ExecContext(
		ctx,
		insertMemoryEdgeQuery,
		edge.EdgeID().String(),
		edge.FromMemoryID().String(),
		edge.ToMemoryID().String(),
		edge.RelationType().String(),
		formatMemoryValidityTimestamp(edge.ValidFrom()),
		validToValue,
		edge.CreatedAt().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return xerrors.Errorf("failed to insert memory edge: %w", err)
	}
	return nil
}

// List returns edges matching the given filter.
func (d *MemoryEdgeDatasource) List(ctx context.Context, filter model.MemoryEdgeListFilter) ([]*model.MemoryEdge, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for memory edge list: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	memoryID := filter.MemoryID.String()
	relation := filter.Relation.String()
	asOfValue := ""
	if t, ok := filter.AsOf.Value(); ok {
		asOfValue = formatMemoryValidityTimestamp(t)
	}

	rows, err := db.QueryContext(
		ctx,
		listMemoryEdgesQuery,
		memoryID, memoryID, memoryID,
		relation, relation,
		asOfValue, asOfValue,
		asOfValue, asOfValue,
		filter.Limit, filter.Limit,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query memory edges: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	edges := make([]*model.MemoryEdge, 0)
	for rows.Next() {
		edge, err := scanMemoryEdge(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan memory edge row: %w", err)
		}
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate memory edge rows: %w", err)
	}
	return edges, nil
}

func scanMemoryEdge(scanner interface {
	Scan(dest ...any) error
}) (*model.MemoryEdge, error) {
	var (
		idValue         string
		fromIDValue     string
		toIDValue       string
		relationValue   string
		validFromValue  string
		validToValue    sql.NullString
		createdAtValue  string
	)
	if err := scanner.Scan(
		&idValue,
		&fromIDValue,
		&toIDValue,
		&relationValue,
		&validFromValue,
		&validToValue,
		&createdAtValue,
	); err != nil {
		return nil, xerrors.Errorf("failed to scan edge: %w", err)
	}
	edgeID, err := types.MemoryEdgeIDOf(idValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore edge ID: %w", err)
	}
	fromID, err := types.MemoryIDOf(fromIDValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore from memory ID: %w", err)
	}
	toID, err := types.MemoryIDOf(toIDValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore to memory ID: %w", err)
	}
	validFrom, err := time.Parse(time.RFC3339Nano, validFromValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore valid_from: %w", err)
	}
	validTo := types.None[time.Time]()
	if validToValue.Valid && validToValue.String != "" {
		t, err := time.Parse(time.RFC3339Nano, validToValue.String)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore valid_to: %w", err)
		}
		validTo = types.Some(t)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtValue)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore created_at: %w", err)
	}
	return model.MemoryEdgeOf(
		edgeID,
		fromID,
		toID,
		types.MemoryEdgeRelationOf(relationValue),
		validFrom,
		validTo,
		createdAt,
	), nil
}
