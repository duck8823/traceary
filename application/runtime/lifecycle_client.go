package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// ShutdownPreviewRun is compact public risk metadata; no PID, paths or terminal content.
type ShutdownPreviewRun struct {
	Selection            OutputSelection
	State                domain.RunState
	PublicationUncertain bool
}

// ShutdownPreviewRunFrom strips private launch details from a trusted snapshot.
func ShutdownPreviewRunFrom(s domain.RunSnapshot) ShutdownPreviewRun {
	return ShutdownPreviewRun{Selection: OutputSelection{Identity: s.Identity, Worktree: s.Target.Worktree().ID(), Profile: s.Profile.ID()}, State: s.State, PublicationUncertain: s.PublicationUncertain}
}

// Valid checks metadata shape; it is never authority to affect a process.
func (r ShutdownPreviewRun) Valid() bool {
	if !(domain.FreshnessBinding{Identity: r.Selection.Identity, Worktree: r.Selection.Worktree, Profile: r.Selection.Profile}).Valid() {
		return false
	}
	switch r.State {
	case domain.RunReserved, domain.RunLive:
		return true
	case domain.RunExited:
		return r.PublicationUncertain
	default:
		return false
	}
}

// ShutdownRosterDigest preserves the original sorted whitelist encoding. It is a
// public plan correlation digest, not a human/native approval attestation.
func ShutdownRosterDigest(rows []ShutdownPreviewRun) (string, error) {
	if len(rows) > domain.MaxShutdownPreviewRuns {
		return "", domain.ErrLimit
	}
	type entry struct {
		Identity  domain.LaunchIdentity
		Worktree  domain.WorktreeID
		Profile   domain.ProfileID
		State     domain.RunState
		Uncertain bool
	}
	roster := make([]entry, 0, len(rows))
	previous := ""
	for _, r := range rows {
		if !r.Valid() || previous != "" && r.Selection.Identity.Run <= previous {
			return "", domain.ErrInvalid
		}
		previous = r.Selection.Identity.Run
		roster = append(roster, entry{r.Selection.Identity, r.Selection.Worktree, r.Selection.Profile, r.State, r.PublicationUncertain})
	}
	data, err := json.Marshal(roster)
	if err != nil {
		return "", domain.ErrInvalid
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// ShutdownPageRequest continues one existing immutable preview, never replaces it.
type ShutdownPageRequest struct {
	Binding domain.ShutdownBindingState
	Cursor  string
	Offset  int
}

// ShutdownPreviewPage is count- and revision-bound. Wire pages also have byte limits.
type ShutdownPreviewPage struct {
	Binding domain.ShutdownBindingState
	Offset  int
	Runs    []ShutdownPreviewRun
	Next    string
}

// ShutdownPlan is the full validated display plan, not one transport page.
type ShutdownPlan struct {
	Binding domain.ShutdownBindingState
	Runs    []ShutdownPreviewRun
}

// LifecycleClient exposes named management-only calls; none starts a daemon.
type LifecycleClient interface {
	PreviewShutdown(context.Context, string, string) (ShutdownPreviewPage, error)
	ShutdownPage(context.Context, string, ShutdownPageRequest) (ShutdownPreviewPage, error)
	CancelShutdown(context.Context, string, domain.ShutdownBindingState) error
	ConfirmShutdown(context.Context, string, ShutdownConfirmation) (ShutdownResult, error)
	ShutdownReceipt(context.Context, string, domain.ShutdownBindingState) (ShutdownResult, error)
}

// Valid checks one bounded contiguous page, including explicit completion shape.
func (p ShutdownPreviewPage) Valid() bool {
	if _, err := domain.ShutdownBindingFrom(p.Binding); err != nil {
		return false
	}
	if p.Runs == nil || len(p.Runs) > 2 || p.Offset < 0 || p.Offset > p.Binding.TotalRuns || p.Offset+len(p.Runs) > p.Binding.TotalRuns {
		return false
	}
	previous := ""
	for _, r := range p.Runs {
		if !r.Valid() || r.Selection.Identity.Epoch != p.Binding.Epoch || previous != "" && r.Selection.Identity.Run <= previous {
			return false
		}
		previous = r.Selection.Identity.Run
	}
	if p.Next == "" {
		return p.Offset+len(p.Runs) == p.Binding.TotalRuns
	}
	return len(p.Runs) > 0 && p.Offset+len(p.Runs) < p.Binding.TotalRuns && p.Next == previous
}

// ReadShutdownPlan assembles every page before permitting a display to claim a
// complete roster. It makes bounded read calls only, never confirmation/retry.
func ReadShutdownPlan(ctx context.Context, client LifecycleClient, root, epoch string) (ShutdownPlan, error) {
	if !(domain.LaunchIdentity{Epoch: epoch, Run: epoch, Generation: epoch}).Valid() {
		return ShutdownPlan{}, ErrRuntimeProtocol
	}
	if client == nil {
		return ShutdownPlan{}, ErrRuntimeUnsupported
	}
	page, err := client.PreviewShutdown(ctx, root, epoch)
	if err != nil {
		return ShutdownPlan{}, xerrors.Errorf("read shutdown preview: %w", err)
	}
	plan := ShutdownPlan{Binding: page.Binding, Runs: []ShutdownPreviewRun{}}
	for calls := 0; calls <= domain.MaxShutdownPreviewRuns; calls++ {
		if !page.Valid() || page.Binding.Epoch != epoch || page.Binding != plan.Binding || page.Offset != len(plan.Runs) {
			return ShutdownPlan{}, ErrRuntimeProtocol
		}
		plan.Runs = append(plan.Runs, page.Runs...)
		if len(plan.Runs) > domain.MaxShutdownPreviewRuns {
			return ShutdownPlan{}, ErrRuntimeProtocol
		}
		if page.Next == "" {
			digest, e := ShutdownRosterDigest(plan.Runs)
			if e != nil || len(plan.Runs) != plan.Binding.TotalRuns || digest != plan.Binding.RosterDigest {
				return ShutdownPlan{}, ErrRuntimeProtocol
			}
			return plan, nil
		}
		page, err = client.ShutdownPage(ctx, root, ShutdownPageRequest{Binding: plan.Binding, Cursor: page.Next, Offset: len(plan.Runs)})
		if err != nil {
			return ShutdownPlan{}, xerrors.Errorf("continue shutdown preview: %w", err)
		}
	}
	return ShutdownPlan{}, ErrRuntimeProtocol
}
