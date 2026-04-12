package sqlite

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
)

// SaveSessionBoundaryForTest exposes saveSessionBoundary so tests can seed
// the sessions table (insert + optional end update) without going through
// SaveBoundary. SaveBoundary would also append a session_started or
// session_ended event, which tests that verify independent event counts
// need to control themselves.
func (d *SessionDatasource) SaveSessionBoundaryForTest(ctx context.Context, session *model.Session) error {
	db, err := d.db.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return saveSessionBoundary(ctx, db, session)
}
