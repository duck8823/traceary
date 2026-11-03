package runtime

import (
	"context"
	"errors"

	"github.com/duck8823/traceary/application/queryservice"
	app "github.com/duck8823/traceary/application/runtime"
	apptypes "github.com/duck8823/traceary/application/types"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/infrastructure/sqlite"
	"golang.org/x/xerrors"
)

// SQLiteHistoryReader reuses body-free canonical queries with a fresh yielding scope.
// It has no retained store pool, migrations, spool/receipt writer or native handles.
type SQLiteHistoryReader struct{}

// ReadHistoryPage closes physical leases before returning to source/Git validation.
func (SQLiteHistoryReader) ReadHistoryPage(ctx context.Context, source domain.HistorySource, v domain.HistorySelection) ([]domain.HistoryEvent, error) {
	if !source.Valid() || !v.Valid() {
		return nil, domain.ErrInvalid
	}
	db := sqlite.NewDatabase(source.Worktree().Store(), nil)
	var result []domain.HistoryEvent
	err := db.WithYieldingReadScope(ctx, func(readCtx context.Context) error {
		criteria := apptypes.NewEventListCriteriaBuilder(v.Limit() + 1).Workspace(types.Workspace(source.Worktree().Path())).From(v.From()).To(v.To())
		if before := v.Before(); !before.IsZero() {
			anchor, err := apptypes.EventPageAnchorOf(before.At, types.EventID(before.ID))
			if err != nil {
				return xerrors.Errorf("construct history keyset: %w", err)
			}
			criteria.PageAnchor(anchor)
		}
		rows, err := sqlite.NewEventDatasource(db).ListRecentMetadata(readCtx, criteria.Build())
		if err != nil {
			return xerrors.Errorf("read existing history metadata: %w", err)
		}
		if len(rows) > v.Limit()+1 {
			return domain.ErrLimit
		}
		result = make([]domain.HistoryEvent, 0, len(rows))
		for _, row := range rows {
			event, err := domain.HistoryEventOf(domain.HistoryEventState{ID: row.EventID().String(), Kind: row.Kind().String(), Client: row.Client().String(), Agent: row.Agent().String(), SessionID: row.SessionID().String(), Workspace: row.Workspace().String(), SourceHook: row.SourceHook(), CreatedAt: row.CreatedAt()})
			if err != nil {
				return xerrors.Errorf("validate committed history metadata: %w", err)
			}
			result = append(result, event)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, queryservice.ErrHistoryReadBusy) {
			return nil, errors.Join(app.ErrRuntimeBusy, err)
		}
		if errors.Is(err, queryservice.ErrHistoryReadUnsupported) {
			return nil, errors.Join(domain.ErrUnsupported, err)
		}
		return nil, xerrors.Errorf("finish bounded history query: %w", err)
	}
	return result, nil
}
