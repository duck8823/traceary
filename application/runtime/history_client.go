package runtime

import (
	"context"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// HistoryFactory is trusted composition; creation must not open a core store or agent.
type HistoryFactory func(context.Context, string) (*History, error)

// RemoteHistoryRequest binds the original page selection to one daemon epoch.
type RemoteHistoryRequest struct {
	Epoch   string
	Request HistoryRequest
}

// Valid rejects malformed epochs and unbound page continuations before transport.
func (r RemoteHistoryRequest) Valid() bool {
	return (domain.LaunchIdentity{Epoch: r.Epoch, Run: r.Epoch, Generation: r.Epoch}).Valid() && r.Request.Valid()
}

// HistoryHintState is advisory, lossy invalidation, NEVER a commit sequence.
type HistoryHintState struct {
	Revision uint64
	Overflow bool
}

// Valid enforces saturating generation semantics, not freshness certainty.
func (s HistoryHintState) Valid() bool { return s.Overflow == (s.Revision == ^uint64(0)) }

// HistoryResult carries one committed page and advisory invalidation metadata.
type HistoryResult struct {
	Epoch string
	Page  domain.HistoryPage
	Hint  HistoryHintState
}

// HistoryClient performs only named nonstarting history and optional hint calls.
type HistoryClient interface {
	HistoryPage(context.Context, string, RemoteHistoryRequest) (HistoryResult, error)
	HintHistory(context.Context, string) error
}
