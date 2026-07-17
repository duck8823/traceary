// Package usecase — bundle usecase implements the v0.9 portability
// primitive introduced for #572: a local-first, encrypted,
// content-verifiable archive that operators can move between their
// machines through any file-transport they already have (AirDrop,
// scp, Syncthing, etc.). Traceary never ships its own transport.
//
// Portability covers all five tables — events, sessions, command_audits,
// memories, and memory_edges — see docs/operations/cross-machine-handoff
// for the operator guide.
package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"

	"golang.org/x/xerrors"
)

func (u *bundleUsecase) bundleTableRegistry() map[string]bundleTableImporter {
	sessions := bundleSessionsTable{}
	events := bundleEventsTable{}
	commandAudits := bundleCommandAuditsTable{}
	memories := bundleMemoriesTable{}
	memoryEdges := bundleMemoryEdgesTable{}
	return map[string]bundleTableImporter{
		sessions.Name():      sessions,
		events.Name():        events,
		commandAudits.Name(): commandAudits,
		memories.Name():      memories,
		memoryEdges.Name():   memoryEdges,
	}
}

func bundleTableImportOrder() []string {
	return []string{"sessions", "events", "command_audits", "memories", "memory_edges"}
}

type bundleSessionsTable struct{}

func (bundleSessionsTable) Name() string { return "sessions" }

func (bundleSessionsTable) FileName() string { return "sessions.ndjson" }

func (bundleSessionsTable) Export(_ context.Context, input bundleExportInputRows) (*bytes.Buffer, error) {
	return encodeSessionsNDJSON(input.Sessions)
}

func (bundleSessionsTable) Decode(r io.Reader) ([]bundleRow, error) {
	decoder := json.NewDecoder(r)
	rows := []bundleRow{}
	for decoder.More() {
		var row bundleSessionRow
		if err := decoder.Decode(&row); err != nil {
			return nil, xerrors.Errorf("session row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (bundleSessionsTable) Apply(
	ctx context.Context,
	tx BundleImportTransaction,
	rows []bundleRow,
	policy bundleImportPolicy,
) (int, int, error) {
	sortedRows, err := sortBundleSessionRows(rows)
	if err != nil {
		return 0, 0, err
	}
	imported := 0
	skipped := 0
	for _, row := range sortedRows {
		session, err := row.toSession()
		if err != nil {
			return imported, skipped, xerrors.Errorf("restore session: %w", err)
		}
		didImport, err := tx.ImportSession(ctx, session, policy.OnConflict, policy.MissingParent)
		if err != nil {
			return imported, skipped, xerrors.Errorf("session %s: %w", session.SessionID(), err)
		}
		if didImport {
			imported++
		} else {
			skipped++
		}
	}
	return imported, skipped, nil
}

type bundleEventsTable struct{}

func (bundleEventsTable) Name() string { return "events" }

func (bundleEventsTable) FileName() string { return "events.ndjson" }

func (bundleEventsTable) Export(_ context.Context, input bundleExportInputRows) (*bytes.Buffer, error) {
	return encodeEventsNDJSON(input.Events)

}

func (bundleEventsTable) Decode(r io.Reader) ([]bundleRow, error) {
	decoder := json.NewDecoder(r)
	rows := []bundleRow{}
	for decoder.More() {
		var row bundleEventRow
		if err := decoder.Decode(&row); err != nil {
			return nil, xerrors.Errorf("event row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (bundleEventsTable) Apply(
	ctx context.Context,
	tx BundleImportTransaction,
	rows []bundleRow,
	policy bundleImportPolicy,

) (int, int, error) {
	imported := 0
	skipped := 0
	for _, generic := range rows {
		row, ok := generic.(bundleEventRow)
		if !ok {
			return imported, skipped, xerrors.Errorf("unexpected events row type %T", generic)
		}
		event, err := row.toEvent()
		if err != nil {
			return imported, skipped, xerrors.Errorf("restore event: %w", err)
		}
		didImport, err := tx.ImportEvent(ctx, event, policy.OnConflict)

		if err != nil {
			return imported, skipped, xerrors.Errorf("event %s: %w", event.EventID(), err)
		}
		if didImport {
			imported++
		} else {
			skipped++
		}
	}
	return imported, skipped, nil
}

type bundleCommandAuditsTable struct{}

func (bundleCommandAuditsTable) Name() string { return "command_audits" }

func (bundleCommandAuditsTable) FileName() string { return "command_audits.ndjson" }

func (bundleCommandAuditsTable) Export(_ context.Context, input bundleExportInputRows) (*bytes.Buffer, error) {
	return encodeCommandAuditsNDJSON(input.CommandAudits)
}

func (bundleCommandAuditsTable) Decode(r io.Reader) ([]bundleRow, error) {
	decoder := json.NewDecoder(r)
	rows := []bundleRow{}
	for decoder.More() {
		var row bundleCommandAuditRow
		if err := decoder.Decode(&row); err != nil {
			return nil, xerrors.Errorf("command audit row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (bundleCommandAuditsTable) Apply(
	ctx context.Context,
	tx BundleImportTransaction,
	rows []bundleRow,
	policy bundleImportPolicy,
) (int, int, error) {
	imported := 0
	skipped := 0
	for _, generic := range rows {
		row, ok := generic.(bundleCommandAuditRow)
		if !ok {
			return imported, skipped, xerrors.Errorf("unexpected command_audits row type %T", generic)
		}
		audit, err := row.toCommandAudit()
		if err != nil {
			return imported, skipped, xerrors.Errorf("restore command audit: %w", err)
		}
		didImport, err := tx.ImportCommandAudit(ctx, audit, policy.OnConflict)
		if err != nil {
			return imported, skipped, xerrors.Errorf("command audit %s: %w", audit.EventID(), err)
		}
		if didImport {
			imported++
		} else {
			skipped++
		}
	}
	return imported, skipped, nil
}

type bundleMemoriesTable struct{}

func (bundleMemoriesTable) Name() string { return "memories" }

func (bundleMemoriesTable) FileName() string { return "memories.ndjson" }

func (bundleMemoriesTable) Export(_ context.Context, input bundleExportInputRows) (*bytes.Buffer, error) {
	return encodeMemoriesNDJSON(input.Memories)

}

func (bundleMemoriesTable) Decode(r io.Reader) ([]bundleRow, error) {
	decoder := json.NewDecoder(r)
	rows := []bundleRow{}
	for decoder.More() {
		var row bundleMemoryRow
		if err := decoder.Decode(&row); err != nil {
			return nil, xerrors.Errorf("memory row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (bundleMemoriesTable) Apply(
	ctx context.Context,
	tx BundleImportTransaction,
	rows []bundleRow,
	policy bundleImportPolicy,

) (int, int, error) {
	sortedRows, err := topologicallySortBundleMemoryRows(rows)
	if err != nil {
		return 0, 0, err
	}
	imported := 0
	skipped := 0
	for _, row := range sortedRows {
		memory, err := row.toMemory()
		if err != nil {
			return imported, skipped, xerrors.Errorf("restore memory: %w", err)
		}
		didImport, err := tx.ImportMemory(ctx, memory, policy.OnConflict)

		if err != nil {
			return imported, skipped, xerrors.Errorf("memory %s: %w", memory.MemoryID(), err)
		}
		if didImport {
			imported++
		} else {
			skipped++
		}
	}
	return imported, skipped, nil
}

type bundleMemoryEdgesTable struct{}

func (bundleMemoryEdgesTable) Name() string { return "memory_edges" }

func (bundleMemoryEdgesTable) FileName() string { return "memory_edges.ndjson" }

func (bundleMemoryEdgesTable) Export(_ context.Context, input bundleExportInputRows) (*bytes.Buffer, error) {
	return encodeMemoryEdgesNDJSON(input.MemoryEdges)
}

func (bundleMemoryEdgesTable) Decode(r io.Reader) ([]bundleRow, error) {
	decoder := json.NewDecoder(r)
	rows := []bundleRow{}
	for decoder.More() {
		var row bundleMemoryEdgeRow
		if err := decoder.Decode(&row); err != nil {
			return nil, xerrors.Errorf("memory edge row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (bundleMemoryEdgesTable) Apply(
	ctx context.Context,
	tx BundleImportTransaction,
	rows []bundleRow,
	policy bundleImportPolicy,
) (int, int, error) {
	imported := 0
	skipped := 0
	for _, generic := range rows {
		row, ok := generic.(bundleMemoryEdgeRow)
		if !ok {
			return imported, skipped, xerrors.Errorf("unexpected memory_edges row type %T", generic)
		}
		edge, err := row.toMemoryEdge()
		if err != nil {
			return imported, skipped, xerrors.Errorf("restore memory edge: %w", err)
		}
		edgeExists, err := tx.MemoryEdgeExists(ctx, edge.EdgeID())
		if err != nil {
			return imported, skipped, xerrors.Errorf("edge %s conflict check: %w", edge.EdgeID(), err)
		}
		if edgeExists {
			switch policy.OnConflict {
			case BundleConflictError:
				return imported, skipped, xerrors.Errorf("memory edge %s: memory edge conflict", edge.EdgeID())
			case BundleConflictSkip:
				skipped++
				continue
			}
		}
		fromExists, err := tx.MemoryExists(ctx, edge.FromMemoryID())
		if err != nil {
			return imported, skipped, xerrors.Errorf("edge %s from endpoint check: %w", edge.EdgeID(), err)
		}
		toExists, err := tx.MemoryExists(ctx, edge.ToMemoryID())
		if err != nil {
			return imported, skipped, xerrors.Errorf("edge %s to endpoint check: %w", edge.EdgeID(), err)
		}
		if !fromExists || !toExists {
			if policy.OrphanEdges == BundleOrphanEdgesReject {
				return imported, skipped, xerrors.Errorf("memory edge %s references missing endpoint(s): from_memory_id=%s exists=%t, to_memory_id=%s exists=%t", edge.EdgeID(), edge.FromMemoryID(), fromExists, edge.ToMemoryID(), toExists)
			}
			slog.WarnContext(
				ctx,
				"bundle import skipped orphan memory edge",
				"table", "memory_edges",
				"edge_id", edge.EdgeID().String(),
				"from_memory_id", edge.FromMemoryID().String(),
				"from_exists", fromExists,
				"to_memory_id", edge.ToMemoryID().String(),
				"to_exists", toExists,
				"policy", string(BundleOrphanEdgesSkip),
			)
			skipped++
			continue
		}
		didImport, err := tx.ImportMemoryEdge(ctx, edge, policy.OnConflict)
		if err != nil {
			return imported, skipped, xerrors.Errorf("memory edge %s: %w", edge.EdgeID(), err)
		}
		if didImport {
			imported++
		} else {
			skipped++
		}
	}
	return imported, skipped, nil
}
