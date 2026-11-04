//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"math"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// NewHistoryService opens only existing private Registry metadata, not any core store.
func NewHistoryService(ctx context.Context, root string) (*app.History, error) {
	if err := ctx.Err(); err != nil {
		return nil, xerrors.Errorf("compose history budget: %w", err)
	}
	metadata, err := OpenRegistryStore(root, MaxRegistryBytes)
	if err != nil {
		return nil, err
	}
	sources, err := app.NewRegisteredHistorySources(metadata, RegistryResolver{})
	if err != nil {
		return nil, xerrors.Errorf("compose registered history: %w", err)
	}
	history, err := app.NewHistory(sources, SQLiteHistoryReader{})
	if err != nil {
		return nil, xerrors.Errorf("compose history reader: %w", err)
	}
	return history, nil
}
func (s *daemonServer) historyHintState() app.HistoryHintState {
	n := s.historyHints.Load()
	return app.HistoryHintState{Revision: n, Overflow: n == math.MaxUint64}
}
func (s *daemonServer) historyHintReply(reply *runtimeResponse) {
	if s.history == nil {
		reply.Code = "unsupported"
		return
	}
	for {
		n := s.historyHints.Load()
		if n == math.MaxUint64 || s.historyHints.CompareAndSwap(n, n+1) {
			break
		}
	}
	h := recordHistoryHint(s.historyHintState())
	reply.HistoryHint = &h
	reply.Code = "ok"
}
func (s *daemonServer) historyReply(reply *runtimeResponse, record *historyRequestRecord) {
	if s.history == nil {
		reply.Code = "unsupported"
		return
	}
	request, err := record.request()
	if err != nil {
		reply.Code = "invalid"
		return
	}
	if request.Epoch != s.epoch {
		reply.Code = "changed"
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, ManagementRuntimeBudget)
	defer cancel()
	page, err := s.history.Page(ctx, request.Request)
	reply.Code = operationCode(err)
	if err != nil {
		return
	}
	result := app.HistoryResult{Epoch: s.epoch, Page: page, Hint: s.historyHintState()}
	for {
		reply.History = recordHistoryResult(request, result)
		data, e := json.Marshal(reply)
		if e == nil && len(data) <= MaxRuntimeFrameBytes {
			return
		}
		if len(result.Page.Events()) <= 1 {
			reply.History = nil
			reply.Code = "limit"
			return
		}
		result.Page, err = result.Page.Prefix(len(result.Page.Events()) - 1)
		if err != nil {
			reply.History = nil
			reply.Code = "invalid"
			return
		}
	}
}
func historyOnlyResponse(r runtimeResponse) bool {
	return r.Resume == nil && r.Control == nil && r.Output == nil && r.Run == nil && r.Page == nil && r.Status == nil && r.Role == ""
}

// HistoryPage reads exactly once from an existing daemon; no store/path/agent side effects.
func (d *LocalDaemon) HistoryPage(ctx context.Context, root string, request app.RemoteHistoryRequest) (app.HistoryResult, error) {
	if !request.Valid() {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	r := recordHistoryRequest(request)
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "history_page", History: &r}, ManagementRuntimeBudget)
	if !historyOnlyResponse(reply) || reply.HistoryHint != nil {
		return app.HistoryResult{}, app.ErrRuntimeProtocol
	}
	if err != nil {
		if reply.History != nil {
			return app.HistoryResult{}, app.ErrRuntimeProtocol
		}
		return app.HistoryResult{}, err
	}
	if reply.Code != "ok" || reply.History == nil {
		return app.HistoryResult{}, app.ErrRuntimeProtocol
	}
	echo, e := reply.History.Request.request()
	if e != nil || echo != request {
		return app.HistoryResult{}, app.ErrRuntimeProtocol
	}
	result, e := reply.History.result()
	if e != nil || result.Epoch != request.Epoch {
		return app.HistoryResult{}, app.ErrRuntimeProtocol
	}
	return result, nil
}

// HintHistory is only coarse lossy invalidation; its acknowledgment proves no data commit.
func (d *LocalDaemon) HintHistory(ctx context.Context, root string) error {
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "history_hint", HistoryHint: &struct{}{}}, OptionalRuntimeBudget)
	if !historyOnlyResponse(reply) || reply.History != nil {
		return app.ErrRuntimeProtocol
	}
	if err != nil {
		if reply.HistoryHint != nil {
			return app.ErrRuntimeProtocol
		}
		return err
	}
	if reply.Code != "ok" || reply.HistoryHint == nil {
		return app.ErrRuntimeProtocol
	}
	if _, err = reply.HistoryHint.hint(); err != nil {
		return app.ErrRuntimeProtocol
	}
	return nil
}
