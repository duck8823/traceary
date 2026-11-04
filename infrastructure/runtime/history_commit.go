package runtime

import (
	"context"

	"github.com/duck8823/traceary/application/usecase"
)

// HistoryHintClient is the nonstarting, metadata-free side of the daemon SDK.
type HistoryHintClient interface {
	HintHistory(context.Context, string) error
}

type historyCommitObserver struct {
	client HistoryHintClient
	root   string
}

// NewHistoryCommitObserver enables advisory hints only for explicit existing
// private routing. Invalid/absent routing never changes capture availability.
// A routing root certifies neither a native session nor delivery of a commit.
func NewHistoryCommitObserver(client HistoryHintClient, root string) usecase.EventCommitObserver {
	if client == nil || !validHistoryHintRoot(root) {
		return nil
	}
	return historyCommitObserver{client: client, root: root}
}

func (o historyCommitObserver) NotifyCommittedHistory(ctx context.Context) {
	// No retry, autostart, event payload or persistent queue. The caller owns
	// the bounded wait; missing hints are recovered by full history rescans.
	_ = o.client.HintHistory(ctx, o.root)
}
