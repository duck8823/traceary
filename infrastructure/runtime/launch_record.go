package runtime

import (
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

type profileRecord struct {
	ID         string `json:"id"`
	Host       string `json:"host"`
	Executable string `json:"executable"`
	Witness    string `json:"witness"`
	Version    string `json:"version"`
}
type launchIdentityRecord struct {
	Run        string `json:"run"`
	Generation string `json:"generation"`
	Epoch      string `json:"epoch"`
}
type launchRecord struct {
	Identity  launchIdentityRecord `json:"identity"`
	Worktree  worktreeRecord       `json:"worktree"`
	Profile   profileRecord        `json:"profile"`
	State     string               `json:"state"`
	PID       *int                 `json:"pid"`
	ExitCode  *int                 `json:"exit_code"`
	Uncertain *bool                `json:"publication_uncertain"`
}

func recordProfile(p domain.HostProfile) profileRecord {
	s := p.Snapshot()
	return profileRecord{s.ID, s.Host, s.Executable, s.ExecutableWitness, s.Version}
}
func (r profileRecord) domainProfile() (domain.HostProfile, error) {
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: r.ID, Host: r.Host, Executable: r.Executable, ExecutableWitness: r.Witness, Version: r.Version})
	if err != nil {
		return domain.HostProfile{}, xerrors.Errorf("decode registered host profile: %w", err)
	}
	return profile, nil
}
func recordLaunch(s domain.RunSnapshot) launchRecord {
	w := s.Target.Worktree()
	id := s.Identity
	return launchRecord{Identity: launchIdentityRecord{id.Run, id.Generation, id.Epoch}, Worktree: worktreeRecord{string(w.ID()), string(w.Project()), w.Path(), w.CommonDir(), w.Store(), w.Witness()}, Profile: recordProfile(s.Profile), State: string(s.State), PID: &s.PID, ExitCode: &s.ExitCode, Uncertain: &s.PublicationUncertain}
}
func (r launchRecord) domainSnapshot() (domain.RunSnapshot, error) {
	if r.PID == nil || r.ExitCode == nil || r.Uncertain == nil {
		return domain.RunSnapshot{}, domain.ErrInvalid
	}
	w, err := domain.WorktreeOf(domain.ProjectID(r.Worktree.Project), r.Worktree.Path, r.Worktree.Common, r.Worktree.Store, r.Worktree.Witness)
	if err != nil || string(w.ID()) != r.Worktree.ID {
		return domain.RunSnapshot{}, domain.ErrInvalid
	}
	profile, err := r.Profile.domainProfile()
	if err != nil {
		return domain.RunSnapshot{}, xerrors.Errorf("decode launch descriptor: %w", err)
	}
	target, err := domain.TargetOf(w, string(profile.ID()))
	if err != nil {
		return domain.RunSnapshot{}, xerrors.Errorf("decode launch descriptor: %w", err)
	}
	descriptor, err := domain.LaunchDescriptorOf(domain.LaunchIdentity{Run: r.Identity.Run, Generation: r.Identity.Generation, Epoch: r.Identity.Epoch}, target, profile)
	if err != nil {
		return domain.RunSnapshot{}, xerrors.Errorf("decode launch descriptor: %w", err)
	}
	switch domain.RunState(r.State) {
	case domain.RunReserved:
		if *r.PID != 0 || *r.ExitCode != 0 {
			return domain.RunSnapshot{}, domain.ErrInvalid
		}
	case domain.RunLive, domain.RunExited:
		if descriptor.Started(*r.PID) != nil {
			return domain.RunSnapshot{}, domain.ErrInvalid
		}
		if domain.RunState(r.State) == domain.RunExited {
			if *r.ExitCode < -1 || *r.ExitCode > 255 {
				return domain.RunSnapshot{}, domain.ErrInvalid
			}
			_ = descriptor.Exit(*r.ExitCode)
		} else if *r.ExitCode != 0 {
			return domain.RunSnapshot{}, domain.ErrInvalid
		}
	default:
		return domain.RunSnapshot{}, domain.ErrInvalid
	}
	if *r.Uncertain {
		_ = descriptor.MarkUncertain()
	}
	return descriptor.Snapshot(), nil
}
