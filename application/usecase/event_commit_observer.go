package usecase

import (
	"context"
	"log/slog"
	"time"
)

const eventCommitHintBudget = 25 * time.Millisecond

// EventCommitObserver invalidates optional history views without receiving any
// event/body/session/native identifiers. Delivery never certifies a commit.
type EventCommitObserver interface{ NotifyCommittedHistory(context.Context) }

// EventUsecaseOption configures trusted optional dependencies before first use.
type EventUsecaseOption func(*eventUsecase)

// WithEventCommitObserver opts in to bounded post-commit hints, never a new drainer.
// At most one observer call per service may remain in flight; extra hints drop.
func WithEventCommitObserver(observer EventCommitObserver) EventUsecaseOption {
	return func(u *eventUsecase) { u.commitObserver = observer; u.commitHintSlot = make(chan struct{}, 1) }
}

func (u *eventUsecase) notifyCommittedHistory(ctx context.Context) {
	if u.commitObserver == nil || ctx.Err() != nil {
		return
	}
	select {
	case u.commitHintSlot <- struct{}{}:
	default:
		return
	}
	notifyCtx, cancel := context.WithTimeout(ctx, eventCommitHintBudget)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer func() {
			// Optional plugin/adapter failures must not crash a successfully
			// committed capture. Never log the panic value or event contents.
			if recover() != nil {
				slog.Debug("optional committed-history observer failed")
			}
			<-u.commitHintSlot
			close(done)
		}()
		u.commitObserver.NotifyCommittedHistory(notifyCtx)
	}()
	select {
	case <-done:
	case <-notifyCtx.Done():
	}
	// A timed-out callback retains its single slot until actual completion;
	// a hung observer cannot cause unbounded background goroutine fan-out.
}
