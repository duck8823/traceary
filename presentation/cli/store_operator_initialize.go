package cli

import (
	"context"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

// operatorStoreBusyRetry is longer than hook sqliteBusyTimeout (1000 ms) so
// search/report/list wait out a writer instead of failing on an already-
// current schema (#2186). Hook Initialize must not use this helper.
const operatorStoreBusyRetry = 15 * time.Second

func initializeOperatorStore(ctx context.Context, store interface {
	Initialize(context.Context) error
}) error {
	deadline := time.Now().Add(operatorStoreBusyRetry)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	var err error
	for {
		err = store.Initialize(ctx)
		if err == nil {
			return nil
		}
		if !operatorSQLiteBusy(err) {
			return xerrors.Errorf("initialize operator store: %w", err)
		}
		if !time.Now().Before(deadline) {
			return xerrors.Errorf("initialize operator store: %w", err)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return xerrors.Errorf("initialize operator store: %w", err)
		case <-timer.C:
		}
	}
}

func operatorSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}
