package runtime

import (
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"time"
)

type shutdownBindingRecord struct {
	Epoch                  string `json:"epoch"`
	PreviewID              string `json:"preview_id"`
	Revision               string `json:"revision"`
	RosterDigest           string `json:"roster_digest"`
	TotalRuns              int    `json:"total_runs"`
	PendingLaunches        int    `json:"pending_launches"`
	PendingControls        int    `json:"pending_controls"`
	PendingReconciliations int    `json:"pending_reconciliations"`
}

func recordShutdownBinding(s domain.ShutdownBindingState) shutdownBindingRecord {
	return shutdownBindingRecord{s.Epoch, s.PreviewID, domain.ByteOffset(s.Revision).String(), s.RosterDigest, s.TotalRuns, s.PendingLaunches, s.PendingControls, s.PendingReconciliations}
}
func (r shutdownBindingRecord) binding() (domain.ShutdownBinding, error) {
	revision, err := domain.ParseByteOffset(r.Revision)
	if err != nil {
		return domain.ShutdownBinding{}, domain.ErrInvalid
	}
	binding, err := domain.ShutdownBindingFrom(domain.ShutdownBindingState{Epoch: r.Epoch, PreviewID: r.PreviewID, Revision: uint64(revision), RosterDigest: r.RosterDigest, TotalRuns: r.TotalRuns, PendingLaunches: r.PendingLaunches, PendingControls: r.PendingControls, PendingReconciliations: r.PendingReconciliations})
	if err != nil {
		return domain.ShutdownBinding{}, domain.ErrInvalid
	}
	return binding, nil
}

type shutdownRecord struct {
	Binding         shutdownBindingRecord `json:"binding"`
	Outcome         string                `json:"outcome"`
	ClosedTerminals int                   `json:"closed_terminals"`
}

func recordShutdown(r app.ShutdownResult) shutdownRecord {
	return shutdownRecord{recordShutdownBinding(r.Binding), string(r.Outcome), r.ClosedTerminals}
}
func (r shutdownRecord) result() (app.ShutdownResult, error) {
	binding, err := r.Binding.binding()
	if err != nil {
		return app.ShutdownResult{}, err
	}
	s := binding.Snapshot()
	if binding.Confirm(s, true) != nil || r.ClosedTerminals < 0 || r.ClosedTerminals > s.TotalRuns {
		return app.ShutdownResult{}, domain.ErrInvalid
	}
	switch domain.ShutdownOutcome(r.Outcome) {
	case domain.ShutdownPending:
		if r.ClosedTerminals != 0 {
			return app.ShutdownResult{}, domain.ErrInvalid
		}
	case domain.ShutdownDraining:
		if r.ClosedTerminals != s.TotalRuns {
			return app.ShutdownResult{}, domain.ErrInvalid
		}
	case domain.ShutdownUnknown:
	default:
		return app.ShutdownResult{}, domain.ErrInvalid
	}
	return app.ShutdownResult{Binding: s, Outcome: domain.ShutdownOutcome(r.Outcome), ClosedTerminals: r.ClosedTerminals}, nil
}

type interruptionRecord struct {
	Selection       outputSelectionRecord `json:"selection"`
	ProcessUnknown  bool                  `json:"process_unknown"`
	PendingControls int                   `json:"pending_controls"`
}

func recordInterruption(i domain.Interruption) interruptionRecord {
	s := i.Snapshot()
	return interruptionRecord{recordSelection(app.OutputSelection{Identity: s.Binding.Identity, Worktree: s.Binding.Worktree, Profile: s.Binding.Profile}), s.ProcessUnknown, s.PendingControls}
}
func (r interruptionRecord) interruption() (domain.Interruption, error) {
	selection, err := r.Selection.selection()
	if err != nil {
		return domain.Interruption{}, domain.ErrInvalid
	}
	interruption, err := domain.InterruptionFrom(domain.InterruptionState{Binding: domain.FreshnessBinding{Identity: selection.Identity, Worktree: selection.Worktree, Profile: selection.Profile}, ProcessUnknown: r.ProcessUnknown, PendingControls: r.PendingControls})
	if err != nil {
		return domain.Interruption{}, domain.ErrInvalid
	}
	return interruption, nil
}

type updateDeferralRecord struct {
	Version     string `json:"version"`
	Protocol    int    `json:"protocol"`
	Started     string `json:"started"`
	Notified24h bool   `json:"notified_24h"`
}

func recordDeferral(s domain.UpdateDeferralState) updateDeferralRecord {
	return updateDeferralRecord{s.Target.Version, s.Target.Protocol, s.Started.UTC().Format(time.RFC3339Nano), s.Notified24h}
}
func (r updateDeferralRecord) deferral() (domain.UpdateDeferralState, error) {
	started, err := time.Parse(time.RFC3339Nano, r.Started)
	if err != nil || len(r.Started) > 40 || started.UTC().Format(time.RFC3339Nano) != r.Started {
		return domain.UpdateDeferralState{}, domain.ErrInvalid
	}
	d, err := domain.UpdateDeferralFrom(domain.UpdateDeferralState{Target: domain.UpdateTarget{Version: r.Version, Protocol: r.Protocol}, Started: started, Notified24h: r.Notified24h})
	if err != nil {
		return domain.UpdateDeferralState{}, domain.ErrInvalid
	}
	return d.Snapshot(), nil
}
