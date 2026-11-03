package runtime

import (
	"encoding/hex"
	"strings"
)

// Launch admission limits are fixed by accepted R0; bounds never evict owned runs.
const (
	MaxActiveRuns     = 256
	MaxLaunchRequests = 1024
	MaxHostProfiles   = 32
)

// HostProfileState is safe operator metadata, never caller-supplied launch argv or environment.
type HostProfileState struct {
	ID                string
	Host              string
	Executable        string
	ExecutableWitness string
	Version           string
}

// HostProfile pins an operator's executable/version policy. It grants no native observation authority.
type HostProfile struct{ state HostProfileState }

// HostProfileFrom validates a safe immutable operator profile snapshot.
func HostProfileFrom(s HostProfileState) (HostProfile, error) {
	if _, err := ProfileIDFrom(s.ID); err != nil {
		return HostProfile{}, err
	}
	switch s.Host {
	case "codex", "claude", "antigravity", "grok":
	default:
		return HostProfile{}, ErrInvalid
	}
	if !validPath(s.Executable) || !validWitness(s.ExecutableWitness) || !printableLaunchValue(s.Version, 256) {
		return HostProfile{}, ErrInvalid
	}
	return HostProfile{state: s}, nil
}

// Snapshot returns defensive, non-secret profile metadata.
func (p HostProfile) Snapshot() HostProfileState { return p.state }

// ID identifies the operator profile, not an authorization capability.
func (p HostProfile) ID() ProfileID { return ProfileID(p.state.ID) }
func printableLaunchValue(s string, limit int) bool {
	if len(s) == 0 || len(s) > limit || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 32 || r > 126 {
			return false
		}
	}
	return true
}

// LaunchIdentity is a public correlation value, never permission to adopt a PID or resume a conversation.
type LaunchIdentity struct{ Run, Generation, Epoch string }

func validLaunchID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// Valid checks canonical public correlation identifiers.
func (i LaunchIdentity) Valid() bool {
	return validLaunchID(i.Run) && validLaunchID(i.Generation) && validLaunchID(i.Epoch)
}

// RunState concerns only a runtime-owned process, not the core Session or native interaction state.
type RunState string

// Process states are separate from native interaction or core Session lifecycle.
const (
	RunReserved RunState = "reserved"
	RunLive     RunState = "live"
	RunExited   RunState = "exited"
)

// RunSnapshot is an independent metadata-only view; PID is diagnostic, never a control handle.
type RunSnapshot struct {
	Identity             LaunchIdentity
	Target               Target
	Profile              HostProfile
	State                RunState
	PID                  int
	ExitCode             int
	PublicationUncertain bool
}

// LaunchDescriptor owns immutable launch binding and one-way child transitions. Runtime serializes mutations.
type LaunchDescriptor struct{ snapshot RunSnapshot }

// LaunchDescriptorOf creates a reserved immutable target/profile binding.
func LaunchDescriptorOf(id LaunchIdentity, target Target, profile HostProfile) (LaunchDescriptor, error) {
	if !id.Valid() || target.Worktree().ID() == "" || target.Profile() != profile.ID() {
		return LaunchDescriptor{}, ErrInvalid
	}
	if _, err := HostProfileFrom(profile.Snapshot()); err != nil {
		return LaunchDescriptor{}, err
	}
	return LaunchDescriptor{snapshot: RunSnapshot{Identity: id, Target: target, Profile: profile, State: RunReserved}}, nil
}

// Snapshot returns an independent metadata view.
func (d LaunchDescriptor) Snapshot() RunSnapshot { return d.snapshot }

// Identity returns the immutable epoch/run/generation tuple.
func (d LaunchDescriptor) Identity() LaunchIdentity { return d.snapshot.Identity }

// Target returns the original selection, never a later UI selection.
func (d LaunchDescriptor) Target() Target { return d.snapshot.Target }

// State returns only the owned process state.
func (d LaunchDescriptor) State() RunState { return d.snapshot.State }

// Started records the one transferred owned child; PID is diagnostic only.
func (d *LaunchDescriptor) Started(pid int) error {
	if d.snapshot.State != RunReserved {
		return ErrChanged
	}
	if pid <= 0 {
		return ErrInvalid
	}
	d.snapshot.PID = pid
	d.snapshot.State = RunLive
	return nil
}

// MarkUncertain latches publication uncertainty without inventing process exit.
func (d *LaunchDescriptor) MarkUncertain() error {
	d.snapshot.PublicationUncertain = true
	return nil
}

// Exit reconciles exactly one result from the owned Wait handle.
func (d *LaunchDescriptor) Exit(code int) error {
	if d.snapshot.State != RunLive {
		return ErrChanged
	}
	d.snapshot.State = RunExited
	d.snapshot.ExitCode = code
	return nil
}
