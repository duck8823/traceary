//go:build darwin || linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

type claudeLaunchPlan struct {
	slot     *nativeStartSlot
	settings string
	model    string
}

// claudeNativeLauncher exposes the recovery port only in a daemon composition
// with a statically qualified registered source. It uses the same PTY owner
// and executable directly; it is not a native command or process wrapper.
type claudeNativeLauncher struct{ *PTYLauncher }

func (p *claudeLaunchPlan) args() []string {
	plan := p.slot.intent.Plan.plan()
	if plan.Source == "resume" {
		return []string{"--resume", plan.SessionID, "--model", p.model, "--settings", p.settings}
	}
	return []string{"--session-id", plan.SessionID, "--settings", p.settings}
}

func (p *claudeLaunchPlan) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.slot.close(ctx)
}

func (l *PTYLauncher) prepareClaudeLaunch(ctx context.Context, snapshot domain.RunSnapshot, old *domain.RecoveryBinding) (*claudeLaunchPlan, error) {
	source, err := inspectClaudeRecoverySource(ctx, snapshot.Profile, l.environment)
	if err != nil {
		return nil, err
	}
	plan := nativeStartPlan{Identity: snapshot.Identity, CWD: snapshot.Target.Worktree().Path(), Store: snapshot.Target.Worktree().Store(), Namespace: source.Namespace, Source: "startup"}
	model := ""
	if old == nil {
		id, err := uuid.NewRandom()
		if err != nil {
			return nil, domain.ErrUnsafe
		}
		plan.SessionID = id.String()
	} else {
		b := old.Snapshot()
		if b.Target != snapshot.Target || b.Profile != snapshot.Profile || b.Identity == snapshot.Identity {
			return nil, domain.ErrChanged
		}
		plan.SessionID = b.NativeSessionID
		plan.Source = "resume"
		model = b.Model
	}
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		return nil, err
	}
	result := &claudeLaunchPlan{slot: slot, model: model}
	success := false
	defer func() {
		if !success {
			result.close()
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return nil, domain.ErrUnsafe
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, domain.ErrUnsafe
	}
	settings, err := slot.writeSettings(ctx, executable)
	if err != nil {
		return nil, err
	}
	result.settings = settings
	success = true
	return result, nil
}

func interruptedContinuationDisabled(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key != "CLAUDE_CODE_RESUME_INTERRUPTED_TURN" {
			result = append(result, entry)
		}
	}
	return append(result, "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0")
}

// RevalidateRecovery is read-only and does not accept missing owned-exit proof
// as an alternate route. Runtime owns that separate prerequisite and journal.
func (l *claudeNativeLauncher) RevalidateRecovery(ctx context.Context, binding domain.RecoveryBinding) error {
	b := binding.Snapshot()
	if _, err := domain.RecoveryBindingOf(b); err != nil {
		return domain.ErrInvalid
	}
	if b.Provider != "claude.ai:firstParty" {
		return domain.ErrUnsupported
	}
	profile := b.Profile.Snapshot()
	if profile.Host != "claude" || profile.Version != claudeRecoveryVersion+" (Claude Code)" {
		return domain.ErrUnsupported
	}
	if err := l.registry.Revalidate(ctx, b.Target, string(b.Profile.ID())); err != nil {
		return xerrors.Errorf("revalidate native recovery target: %w", err)
	}
	if err := l.verifyVersion(ctx, b.Profile); err != nil {
		return err
	}
	source, err := inspectClaudeRecoverySource(ctx, b.Profile, l.environment)
	if err != nil {
		return err
	}
	plan := nativeStartPlan{Identity: b.Identity, SessionID: b.NativeSessionID, CWD: b.Target.Worktree().Path(), Store: b.Target.Worktree().Store(), Namespace: source.Namespace, Source: "resume"}
	history, err := readClaudeRecoveryHistory(ctx, nativeStartEvent{Plan: plan, TranscriptPath: b.NativeHistoryPath})
	if err != nil {
		return err
	}
	if history.NamespaceWitness != b.NativeNamespaceWitness || history.Model != b.Model {
		return domain.ErrChanged
	}
	return verifyClaudeRecoveryProvider(ctx, source, l.environment, plan.CWD)
}

// Resume uses only the original registered normal entry and recorded native
// conversation/model. No pending input, approval or tool is ever written here.
func (l *claudeNativeLauncher) Resume(ctx context.Context, descriptor domain.LaunchDescriptor, binding domain.RecoveryBinding) (app.OwnedChild, error) {
	snapshot := descriptor.Snapshot()
	b := binding.Snapshot()
	if snapshot.Target != b.Target || snapshot.Profile != b.Profile || snapshot.Identity == b.Identity || snapshot.State != domain.RunReserved {
		return nil, domain.ErrChanged
	}
	if err := l.RevalidateRecovery(ctx, binding); err != nil {
		return nil, err
	}
	plan, err := l.prepareClaudeLaunch(ctx, snapshot, &binding)
	if err != nil {
		return nil, err
	}
	return l.start(ctx, descriptor, plan)
}
