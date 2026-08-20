package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
)

// EndedSessionInspector opens a store mode=ro with no busy wait — the same
// bounded probe as the page-metadata inspector — and answers which of the
// given session IDs have ended. The query is session-id keyed against the
// sessions primary key in bounded batches, so large-store doctor can resolve
// hook cancellation markers without walking event bodies or dbstat (#2235).
type EndedSessionInspector struct{}

// NewEndedSessionInspector creates a path-based bounded ended-session reader.
func NewEndedSessionInspector() *EndedSessionInspector {
	return &EndedSessionInspector{}
}

var _ application.EndedSessionInspector = (*EndedSessionInspector)(nil)

// FindEndedSessionIDs returns the subset of sessionIDs whose sessions row has
// ended_at set. An unreadable or locked store returns an error so the caller
// can fail-soft instead of guessing session state.
func (EndedSessionInspector) FindEndedSessionIDs(ctx context.Context, dbPath string, sessionIDs []types.SessionID) (_ map[types.SessionID]struct{}, err error) {
	result := make(map[types.SessionID]struct{})
	if len(sessionIDs) == 0 {
		return result, nil
	}
	db, err := sql.Open("sqlite", sqliteO1ReadOnlyDSN(dbPath))
	if err != nil {
		return nil, xerrors.Errorf("open store for ended session lookup: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("close ended session lookup: %w", closeErr)
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return nil, xerrors.Errorf("ping store for ended session lookup: %w", err)
	}

	const batchSize = 900
	for start := 0; start < len(sessionIDs); start += batchSize {
		end := min(start+batchSize, len(sessionIDs))
		batch := sessionIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, sessionID := range batch {
			args = append(args, sessionID.String())
		}
		if err := func() (resultErr error) {
			rows, err := db.QueryContext(ctx, "SELECT session_id FROM sessions WHERE ended_at IS NOT NULL AND session_id IN ("+placeholders+")", args...)
			if err != nil {
				return xerrors.Errorf("failed to query ended sessions: %w", err)
			}
			defer func() {
				if err := rows.Close(); err != nil && resultErr == nil {
					resultErr = xerrors.Errorf("failed to close ended session rows: %w", err)
				}
			}()
			for rows.Next() {
				var sessionID string
				if err := rows.Scan(&sessionID); err != nil {
					return xerrors.Errorf("failed to scan ended session ID: %w", err)
				}
				result[types.SessionID(sessionID)] = struct{}{}
			}
			if err := rows.Err(); err != nil {
				return xerrors.Errorf("failed to iterate ended session IDs: %w", err)
			}
			return nil
		}(); err != nil {
			return nil, err
		}
	}
	return result, nil
}
