//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

func operationCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, app.ErrLaunchUnknown):
		return "launch_unknown"
	case errors.Is(err, app.ErrRuntimeBusy):
		return "busy"
	case errors.Is(err, domain.ErrOutputCursor):
		return "output_cursor"
	case errors.Is(err, domain.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, domain.ErrChanged):
		return "changed"
	case errors.Is(err, domain.ErrLimit):
		return "limit"
	case errors.Is(err, domain.ErrInvalid):
		return "invalid"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrUnsafe):
		return "unsafe"
	default:
		return "rejected"
	}
}
func (s *daemonServer) launchReply(reply *runtimeResponse, request *launchRequestRecord) {
	if s.runtime == nil {
		reply.Code = "unsupported"
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, ManagementRuntimeBudget)
	defer cancel()
	snapshot, err := s.runtime.Launch(ctx, app.LaunchRequest{Epoch: request.Epoch, Worktree: domain.WorktreeID(request.Worktree), Profile: domain.ProfileID(request.Profile), RequestID: request.RequestID})
	reply.Code = operationCode(err)
	if snapshot.Identity.Valid() {
		record := recordLaunch(snapshot)
		reply.Run = &record
	}
}
func (s *daemonServer) pageReply(reply *runtimeResponse, request *runPageRequestRecord) {
	if s.runtime == nil {
		reply.Code = "unsupported"
		return
	}
	page, err := s.runtime.Page(app.RunPageRequest{Epoch: request.Epoch, Cursor: request.Cursor, Revision: request.Revision})
	reply.Code = operationCode(err)
	if err != nil {
		return
	}
	record := &runPageRecord{Epoch: page.Epoch, Revision: page.Revision, Runs: make([]launchRecord, 0, len(page.Runs)), Next: page.Next}
	for _, run := range page.Runs {
		record.Runs = append(record.Runs, recordLaunch(run))
	}
	reply.Page = record
	// Escaped path bytes count toward the actual frame, not just entry counts.
	for {
		data, e := json.Marshal(reply)
		if e == nil && len(data) <= MaxRuntimeFrameBytes {
			return
		}
		if len(record.Runs) <= 1 {
			reply.Page = nil
			reply.Code = "limit"
			return
		}
		record.Runs = record.Runs[:len(record.Runs)-1]
		record.Next = record.Runs[len(record.Runs)-1].Identity.Run
	}
}

// Launch makes one explicit management call; possible post-send transport loss is unknown, never replayed.
func (d *LocalDaemon) Launch(ctx context.Context, root string, request app.LaunchRequest) (domain.RunSnapshot, error) {
	reply, sent, err := d.exchange(ctx, root, runtimeRequest{Operation: "launch", Launch: &launchRequestRecord{request.Epoch, string(request.Worktree), string(request.Profile), request.RequestID}}, ManagementRuntimeBudget)
	if sent && (errors.Is(err, app.ErrDaemonUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || reply.ID == "" && (errors.Is(err, app.ErrRuntimeProtocol) || errors.Is(err, app.ErrRuntimeUnsupported))) {
		return domain.RunSnapshot{}, app.ErrLaunchUnknown
	}
	if reply.Output != nil || reply.Status != nil || reply.Page != nil || reply.Role != "" {
		return domain.RunSnapshot{}, errors.Join(app.ErrLaunchUnknown, app.ErrRuntimeProtocol)
	}
	if reply.Run == nil {
		if err != nil {
			return domain.RunSnapshot{}, err
		}
		return domain.RunSnapshot{}, errors.Join(app.ErrLaunchUnknown, app.ErrRuntimeProtocol)
	}
	snapshot, decodeErr := reply.Run.domainSnapshot()
	if decodeErr != nil {
		return domain.RunSnapshot{}, errors.Join(app.ErrLaunchUnknown, app.ErrRuntimeProtocol)
	}
	if snapshot.Identity.Epoch != request.Epoch || snapshot.Target.Worktree().ID() != request.Worktree || snapshot.Profile.ID() != request.Profile {
		return domain.RunSnapshot{}, errors.Join(app.ErrLaunchUnknown, app.ErrRuntimeProtocol)
	}
	return snapshot, err
}

// ListRuns reads one bounded epoch/revision page without starting a daemon or agent.
func (d *LocalDaemon) ListRuns(ctx context.Context, root string, request app.RunPageRequest) (app.RunPage, error) {
	reply, _, err := d.exchange(ctx, root, runtimeRequest{Operation: "list_runs", Page: &runPageRequestRecord{request.Epoch, request.Cursor, request.Revision}}, ManagementRuntimeBudget)
	if err != nil {
		return app.RunPage{}, err
	}
	if reply.Output != nil || reply.Page == nil || reply.Status != nil || reply.Run != nil || reply.Role != "" || reply.Page.Epoch != request.Epoch || len(reply.Page.Runs) > 2 || reply.Page.Runs == nil {
		return app.RunPage{}, app.ErrRuntimeProtocol
	}
	result := app.RunPage{Epoch: reply.Page.Epoch, Revision: reply.Page.Revision, Next: reply.Page.Next, Runs: []domain.RunSnapshot{}}
	if request.Revision != 0 && reply.Page.Revision != request.Revision {
		return app.RunPage{}, domain.ErrChanged
	}
	if len(reply.Page.Runs) > 0 && reply.Page.Revision == 0 {
		return app.RunPage{}, app.ErrRuntimeProtocol
	}
	previous := request.Cursor

	for _, record := range reply.Page.Runs {
		run, e := record.domainSnapshot()
		if e != nil || run.Identity.Epoch != request.Epoch {
			return app.RunPage{}, app.ErrRuntimeProtocol
		}
		if run.Identity.Run <= previous {
			return app.RunPage{}, app.ErrRuntimeProtocol
		}
		previous = run.Identity.Run
		result.Runs = append(result.Runs, run)
	}
	if result.Next != "" && (len(result.Runs) == 0 || result.Next != result.Runs[len(result.Runs)-1].Identity.Run) {
		return app.RunPage{}, app.ErrRuntimeProtocol
	}
	return result, nil
}
