//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/domain/types"
	"golang.org/x/sys/unix"
	"math"
	"sort"
)

const maxLifecycleBytes int64 = 16 * 1024 * 1024

type lifecycleEnvelope struct {
	Version        int                   `json:"version"`
	Revision       *uint64               `json:"revision"`
	Epoch          string                `json:"epoch"`
	Clean          bool                  `json:"clean"`
	PreviousEpoch  string                `json:"previous_epoch"`
	PreviousClean  bool                  `json:"previous_clean"`
	Interrupted    []interruptionRecord  `json:"interrupted"`
	Shutdowns      []shutdownRecord      `json:"shutdowns"`
	Update         *updateDeferralRecord `json:"update,omitempty"`
	AutoStartOptIn bool                  `json:"autostart_opt_in"`
}

func emptyLifecycle() lifecycleEnvelope {
	revision := uint64(0)
	return lifecycleEnvelope{Version: 1, Revision: &revision, Interrupted: []interruptionRecord{}, Shutdowns: []shutdownRecord{}}
}
func validLifecycleEpoch(epoch string) bool {
	return (domain.LaunchIdentity{Epoch: epoch, Run: epoch, Generation: epoch}).Valid()
}
func (s *ExecutionStore) readLifecycle(fd int) (lifecycleEnvelope, *unix.Stat_t, error) {
	data, stamp, err := s.metadata.readMetadata(fd, "lifecycle.json", min(maxLifecycleBytes, s.metadata.budget))
	if err != nil {
		return lifecycleEnvelope{}, nil, err
	}
	if stamp == nil {
		return emptyLifecycle(), nil, nil
	}
	doc := lifecycleEnvelope{}
	if err = decodeControlMetadata(data, &doc); err != nil {
		return lifecycleEnvelope{}, nil, err
	}
	if doc.Version != 1 {
		return lifecycleEnvelope{}, nil, domain.ErrUnsupported
	}
	if doc.Revision == nil || doc.Interrupted == nil || doc.Shutdowns == nil || len(doc.Interrupted) > domain.MaxLaunchRequests || len(doc.Shutdowns) > domain.MaxLaunchRequests {
		return lifecycleEnvelope{}, nil, domain.ErrInvalid
	}
	if doc.Epoch != "" && !validLifecycleEpoch(doc.Epoch) || doc.PreviousEpoch != "" && (!validLifecycleEpoch(doc.PreviousEpoch) || doc.PreviousEpoch == doc.Epoch) || doc.PreviousClean && doc.PreviousEpoch == "" || doc.Clean && doc.Epoch == "" {
		return lifecycleEnvelope{}, nil, domain.ErrInvalid
	}
	if doc.Epoch == "" && (doc.PreviousEpoch != "" || len(doc.Interrupted) != 0 || len(doc.Shutdowns) != 0 || doc.Update != nil) {
		return lifecycleEnvelope{}, nil, domain.ErrInvalid
	}
	if doc.Update != nil {
		if _, err = doc.Update.deferral(); err != nil {
			return lifecycleEnvelope{}, nil, err
		}
	}
	executions, _, err := s.read(fd)
	if err != nil {
		return lifecycleEnvelope{}, nil, err
	}
	bindings := make(map[string]app.OutputSelection, len(executions.Launches))
	for _, record := range executions.Launches {
		snapshot, e := record.domainSnapshot()
		if e != nil {
			return lifecycleEnvelope{}, nil, e
		}
		bindings[snapshot.Identity.Run] = app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	}
	seen := make(map[string]bool, len(doc.Interrupted))
	for _, record := range doc.Interrupted {
		_, e := record.interruption()
		if e != nil {
			return lifecycleEnvelope{}, nil, e
		}
		selection := record.Selection
		key := selection.Run
		actual, e := selection.selection()
		if e != nil || bindings[key] != actual || actual.Identity.Epoch == doc.Epoch || seen[key] {
			return lifecycleEnvelope{}, nil, domain.ErrInvalid
		}
		seen[key] = true
	}
	plans := make(map[string]bool, len(doc.Shutdowns))
	for _, record := range doc.Shutdowns {
		result, e := record.result()
		if e != nil {
			return lifecycleEnvelope{}, nil, e
		}
		key := result.Binding.Epoch + result.Binding.PreviewID
		if plans[key] {
			return lifecycleEnvelope{}, nil, domain.ErrInvalid
		}
		plans[key] = true
	}
	if err = s.metadata.metadataBudget(fd, "lifecycle.json", int64(len(data))); err != nil {
		return lifecycleEnvelope{}, nil, err
	}
	return doc, stamp, nil
}
func lifecycleSnapshot(doc lifecycleEnvelope) (app.LifecycleSnapshot, error) {
	snapshot := app.LifecycleSnapshot{Epoch: doc.Epoch, PreviousEpoch: doc.PreviousEpoch, Clean: doc.Clean, PreviousClean: doc.PreviousClean, AutoStartOptIn: doc.AutoStartOptIn, Interrupted: []domain.Interruption{}, Shutdowns: []app.ShutdownResult{}}
	for _, record := range doc.Interrupted {
		i, err := record.interruption()
		if err != nil {
			return app.LifecycleSnapshot{}, err
		}
		snapshot.Interrupted = append(snapshot.Interrupted, i)
	}
	for _, record := range doc.Shutdowns {
		result, err := record.result()
		if err != nil {
			return app.LifecycleSnapshot{}, err
		}
		snapshot.Shutdowns = append(snapshot.Shutdowns, result)
	}
	if doc.Update != nil {
		value, err := doc.Update.deferral()
		if err != nil {
			return app.LifecycleSnapshot{}, err
		}
		snapshot.UpdateDeferral = types.Some(value)
	}
	return snapshot, nil
}
func (s *ExecutionStore) writeLifecycle(ctx context.Context, fd int, doc lifecycleEnvelope, stamp *unix.Stat_t) error {
	if *doc.Revision == math.MaxUint64 {
		return domain.ErrLimit
	}
	*doc.Revision++
	data, err := json.Marshal(doc)
	if err != nil {
		return domain.ErrInvalid
	}
	if int64(len(data)) > maxLifecycleBytes {
		return domain.ErrLimit
	}
	return s.metadata.writeMetadata(ctx, fd, "lifecycle.json", data, stamp)
}
func mapLifecycleCommit(err error) error {
	if errors.Is(err, ErrCommitUncertain) {
		return errors.Join(err, app.ErrLifecycleCommitUncertain)
	}
	return err
}
func (s *ExecutionStore) updateLifecycle(ctx context.Context, mutate func(*lifecycleEnvelope) error) error {
	err := s.metadata.locked(ctx, func(fd int) error {
		doc, stamp, err := s.readLifecycle(fd)
		if err != nil {
			return err
		}
		if err = mutate(&doc); err != nil {
			return err
		}
		return s.writeLifecycle(ctx, fd, doc, stamp)
	})
	return mapLifecycleCommit(err)
}

// ReadLifecycle returns safe metadata only; it cannot adopt or inspect a process.
func (s *ExecutionStore) ReadLifecycle(ctx context.Context) (app.LifecycleSnapshot, error) {
	var result app.LifecycleSnapshot
	err := s.metadata.locked(ctx, func(fd int) error {
		doc, _, err := s.readLifecycle(fd)
		if err != nil {
			return err
		}
		result, err = lifecycleSnapshot(doc)
		if err != nil {
			return err
		}
		return s.metadata.verifyRoot(fd)
	})
	return result, err
}

// RecordEpochStart classifies old records without touching saved PIDs or changing
// original launch/control evidence. Caller owns the daemon lease and a NEW epoch.
func (s *ExecutionStore) RecordEpochStart(ctx context.Context, epoch string) (app.LifecycleSnapshot, error) {
	if !validLifecycleEpoch(epoch) {
		return app.LifecycleSnapshot{}, domain.ErrInvalid
	}
	var result app.LifecycleSnapshot
	err := s.metadata.locked(ctx, func(fd int) error {
		doc, stamp, err := s.readLifecycle(fd)
		if err != nil {
			return err
		}
		if doc.Epoch == epoch || doc.PreviousEpoch == epoch {
			return domain.ErrChanged
		}
		for _, record := range doc.Shutdowns {
			if record.Binding.Epoch == epoch {
				return domain.ErrChanged
			}
		}
		controls, _, _, err := s.readControls(fd)
		if err != nil {
			return err
		}
		pending := make(map[string]int, len(controls.Runs))
		for _, group := range controls.Runs {
			for _, record := range group.Receipts {
				if record.Outcome == string(domain.ControlPending) {
					pending[group.Selection.Run]++
				}
			}
		}
		executions, _, err := s.read(fd)
		if err != nil {
			return err
		}
		interrupted := make([]interruptionRecord, 0, len(executions.Launches))
		for _, record := range executions.Launches {
			snapshot, e := record.domainSnapshot()
			if e != nil {
				return e
			}
			if snapshot.Identity.Epoch == epoch {
				return domain.ErrChanged
			} // never revive a recorded epoch
			count := pending[snapshot.Identity.Run]
			if snapshot.State == domain.RunExited && !snapshot.PublicationUncertain && count == 0 {
				continue
			}
			i, e := domain.InterruptionOf(snapshot, count)
			if e != nil {
				return domain.ErrInvalid
			}
			interrupted = append(interrupted, recordInterruption(i))
		}
		sort.Slice(interrupted, func(i, j int) bool { return interrupted[i].Selection.Run < interrupted[j].Selection.Run })
		doc.PreviousEpoch = doc.Epoch
		doc.PreviousClean = doc.Clean
		doc.Epoch = epoch
		doc.Clean = false
		doc.Interrupted = interrupted
		if err = s.writeLifecycle(ctx, fd, doc, stamp); err != nil {
			return err
		}
		result, err = lifecycleSnapshot(doc)
		return err
	})
	return result, mapLifecycleCommit(err)
}

// RecordCleanShutdown is an owner publication AFTER actual ownership joins, not
// evidence that historical process/descendant records are safe to resume.
func (s *ExecutionStore) RecordCleanShutdown(ctx context.Context, epoch string) error {
	if !validLifecycleEpoch(epoch) {
		return domain.ErrInvalid
	}
	return s.updateLifecycle(ctx, func(doc *lifecycleEnvelope) error {
		if doc.Epoch != epoch {
			return domain.ErrChanged
		}
		doc.Clean = true
		return nil
	})
}

// ReserveShutdown writes a quiescent immutable intent BEFORE master closure.
func (s *ExecutionStore) ReserveShutdown(ctx context.Context, binding domain.ShutdownBinding) error {
	state := binding.Snapshot()
	if err := binding.Confirm(state, true); err != nil {
		return domain.ErrInvalid
	}
	return s.updateLifecycle(ctx, func(doc *lifecycleEnvelope) error {
		if doc.Epoch != state.Epoch || doc.Clean {
			return domain.ErrChanged
		}
		for _, record := range doc.Shutdowns {
			if record.Binding.Epoch == state.Epoch && record.Binding.PreviewID == state.PreviewID {
				return domain.ErrDuplicate
			}
		}
		if len(doc.Shutdowns) >= domain.MaxLaunchRequests {
			return domain.ErrLimit
		}
		doc.Shutdowns = append(doc.Shutdowns, recordShutdown(app.ShutdownResult{Binding: state, Outcome: domain.ShutdownPending}))
		return nil
	})
}

// SaveShutdown completes only a prior exact intent; terminal outcomes are immutable.
func (s *ExecutionStore) SaveShutdown(ctx context.Context, result app.ShutdownResult) error {
	record := recordShutdown(result)
	if _, err := record.result(); err != nil || result.Outcome == domain.ShutdownPending {
		return domain.ErrInvalid
	}
	return s.updateLifecycle(ctx, func(doc *lifecycleEnvelope) error {
		if doc.Epoch != result.Binding.Epoch || doc.Clean {
			return domain.ErrChanged
		}
		for i, old := range doc.Shutdowns {
			if old.Binding.Epoch != record.Binding.Epoch || old.Binding.PreviewID != record.Binding.PreviewID {
				continue
			}
			if old.Binding != record.Binding {
				return domain.ErrChanged
			}
			if old.Outcome != string(domain.ShutdownPending) {
				if old == record {
					return nil
				}
				return domain.ErrChanged
			}
			doc.Shutdowns[i] = record
			return nil
		}
		return domain.ErrNotFound
	})
}

// SaveUpdateDeferral preserves one continuous start and already-published notice.
func (s *ExecutionStore) SaveUpdateDeferral(ctx context.Context, epoch string, state domain.UpdateDeferralState) error {
	if _, err := domain.UpdateDeferralFrom(state); err != nil {
		return domain.ErrInvalid
	}
	record := recordDeferral(state)
	return s.updateLifecycle(ctx, func(doc *lifecycleEnvelope) error {
		if doc.Epoch != epoch || !validLifecycleEpoch(epoch) || doc.Clean {
			return domain.ErrChanged
		}
		if doc.Update != nil && (doc.Update.Started != record.Started || doc.Update.Notified24h && !record.Notified24h) {
			return domain.ErrChanged
		}
		doc.Update = &record
		return nil
	})
}

// ClearUpdateDeferral is explicit handoff/cancel metadata, never an updater action.
func (s *ExecutionStore) ClearUpdateDeferral(ctx context.Context, epoch string) error {
	return s.updateLifecycle(ctx, func(doc *lifecycleEnvelope) error {
		if doc.Epoch != epoch || !validLifecycleEpoch(epoch) || doc.Clean {
			return domain.ErrChanged
		}
		doc.Update = nil
		return nil
	})
}

// SetAutoStartOptIn changes local preference only. It never installs/starts a host
// manager, changes login settings, inherits environment, or launches an agent.
func (s *ExecutionStore) SetAutoStartOptIn(ctx context.Context, optIn bool) error {
	return s.updateLifecycle(ctx, func(doc *lifecycleEnvelope) error { doc.AutoStartOptIn = optIn; return nil })
}
