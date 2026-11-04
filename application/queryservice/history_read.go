package queryservice

import (
	"context"
	"errors"
	"time"
)

// HistoryReadBudget bounds one maintenance-yielding history read, not Git/native validation.
const HistoryReadBudget = 250 * time.Millisecond

var (
	// ErrHistoryReadBusy means the bounded reader must drain and a later read may retry.
	ErrHistoryReadBusy = errors.New("history read busy; retry after store maintenance")
	// ErrHistoryReadUnsupported refuses unsupported/uninitialized stores or non-yielding scopes.
	ErrHistoryReadUnsupported = errors.New("history read requires a supported initialized store and yielding scope")
)

// HistoryReadScope holds no store connection beyond its bounded callback.
// The callback must honor cancellation and finish/close its query resources.
// It cannot retain the context or connection for pagination, UI waits or native I/O.
type HistoryReadScope interface {
	WithYieldingReadScope(context.Context, func(context.Context) error) error
}
