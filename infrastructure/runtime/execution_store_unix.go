//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"unicode/utf8"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

const maxExecutionBytes int64 = 16 * 1024 * 1024

// ExecutionStore shares the private root lock and total byte budget with RegistryStore.
// Old epochs remain metadata only: opening this store never adopts a child/PID.
type ExecutionStore struct{ metadata *RegistryStore }

// NewExecutionStore pins owner-private metadata independently of the core store.
func NewExecutionStore(root string, budget int64) (*ExecutionStore, error) {
	metadata, err := NewRegistryStore(root, budget)
	if err != nil {
		return nil, xerrors.Errorf("open runtime execution metadata: %w", err)
	}
	return &ExecutionStore{metadata: metadata}, nil
}

// OpenExecutionStore never creates a missing private metadata root.
func OpenExecutionStore(root string, budget int64) (*ExecutionStore, error) {
	metadata, err := OpenRegistryStore(root, budget)
	if err != nil {
		return nil, xerrors.Errorf("open existing runtime execution metadata: %w", err)
	}
	return &ExecutionStore{metadata: metadata}, nil
}

type executionEnvelope struct {
	Version  int             `json:"version"`
	Revision *uint64         `json:"revision"`
	Profiles []profileRecord `json:"profiles"`
	Launches []launchRecord  `json:"launches"`
}

func (s *ExecutionStore) read(fd int) (executionEnvelope, *unix.Stat_t, error) {
	limit := min(maxExecutionBytes, s.metadata.budget)
	data, stamp, err := s.metadata.readMetadata(fd, "execution.json", limit)
	if err != nil {
		return executionEnvelope{}, nil, err
	}
	revision := uint64(0)
	empty := executionEnvelope{Version: 1, Revision: &revision, Profiles: []profileRecord{}, Launches: []launchRecord{}}
	if stamp == nil {
		return empty, nil, nil
	}
	if !utf8.Valid(data) {
		return executionEnvelope{}, nil, domain.ErrInvalid
	}
	if err = uniqueJSONValue(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return executionEnvelope{}, nil, err
	}
	var header struct {
		Version *int `json:"version"`
	}
	if json.Unmarshal(data, &header) != nil || header.Version == nil {
		return executionEnvelope{}, nil, domain.ErrInvalid
	}
	if *header.Version != 1 {
		return executionEnvelope{}, nil, domain.ErrUnsupported
	}
	var doc executionEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&doc) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || doc.Revision == nil || doc.Profiles == nil || doc.Launches == nil {
		return executionEnvelope{}, nil, domain.ErrInvalid
	}
	if len(doc.Profiles) > domain.MaxHostProfiles || len(doc.Launches) > domain.MaxLaunchRequests {
		return executionEnvelope{}, nil, domain.ErrLimit
	}
	if *doc.Revision < uint64(len(doc.Profiles)+len(doc.Launches)) {
		return executionEnvelope{}, nil, domain.ErrInvalid
	}
	profiles := make(map[domain.ProfileID]domain.HostProfile, len(doc.Profiles))
	for _, r := range doc.Profiles {
		p, e := r.domainProfile()
		if e != nil {
			return executionEnvelope{}, nil, e
		}
		if _, exists := profiles[p.ID()]; exists {
			return executionEnvelope{}, nil, domain.ErrInvalid
		}
		profiles[p.ID()] = p
	}
	runs := make(map[string]bool, len(doc.Launches))
	generations := make(map[string]bool, len(doc.Launches))
	for _, r := range doc.Launches {
		snapshot, e := r.domainSnapshot()
		if e != nil {
			return executionEnvelope{}, nil, e
		}
		if runs[snapshot.Identity.Run] || generations[snapshot.Identity.Generation] || profiles[snapshot.Profile.ID()] != snapshot.Profile {
			return executionEnvelope{}, nil, domain.ErrInvalid
		}
		runs[snapshot.Identity.Run] = true
		generations[snapshot.Identity.Generation] = true
	}
	if err = s.metadata.metadataBudget(fd, "execution.json", int64(len(data))); err != nil {
		return executionEnvelope{}, nil, err
	}
	return doc, stamp, nil
}
func (s *ExecutionStore) update(ctx context.Context, mutate func(*executionEnvelope) error) error {
	return s.metadata.locked(ctx, func(fd int) error {
		doc, stamp, err := s.read(fd)
		if err != nil {
			return err
		}
		if *doc.Revision == math.MaxUint64 {
			return domain.ErrLimit
		}
		if err = mutate(&doc); err != nil {
			return err
		}
		*doc.Revision++
		data, err := json.Marshal(doc)
		if err != nil {
			return domain.ErrInvalid
		}
		if int64(len(data)) > maxExecutionBytes {
			return domain.ErrLimit
		}
		return s.metadata.writeMetadata(ctx, fd, "execution.json", data, stamp)
	})
}

// RegisterProfile adds an immutable explicitly inspected operator profile, never a launch override.
func (s *ExecutionStore) RegisterProfile(ctx context.Context, profile domain.HostProfile) error {
	if _, err := domain.HostProfileFrom(profile.Snapshot()); err != nil {
		return xerrors.Errorf("register host profile: %w", err)
	}
	return s.update(ctx, func(doc *executionEnvelope) error {
		for _, p := range doc.Profiles {
			if p.ID == string(profile.ID()) {
				return domain.ErrDuplicate
			}
		}
		if len(doc.Profiles) >= domain.MaxHostProfiles {
			return domain.ErrLimit
		}
		doc.Profiles = append(doc.Profiles, recordProfile(profile))
		return nil
	})
}

// LookupProfile loads a snapshot without running the executable or reading host credentials.
func (s *ExecutionStore) LookupProfile(ctx context.Context, id domain.ProfileID) (domain.HostProfile, error) {
	var result domain.HostProfile
	err := s.metadata.locked(ctx, func(fd int) error {
		doc, _, err := s.read(fd)
		if err != nil {
			return err
		}
		for _, p := range doc.Profiles {
			if p.ID == string(id) {
				result, err = p.domainProfile()
				if err != nil {
					return err
				}
				return s.metadata.verifyRoot(fd)
			}
		}
		return domain.ErrNotFound
	})
	return result, err
}

// ListProfiles returns defensive safe metadata in registration order.
func (s *ExecutionStore) ListProfiles(ctx context.Context) ([]domain.HostProfile, error) {
	result := []domain.HostProfile{}
	err := s.metadata.locked(ctx, func(fd int) error {
		doc, _, err := s.read(fd)
		if err != nil {
			return err
		}
		for _, p := range doc.Profiles {
			profile, e := p.domainProfile()
			if e != nil {
				return e
			}
			result = append(result, profile)
		}
		return s.metadata.verifyRoot(fd)
	})
	return result, err
}

// ReserveLaunch durably reserves capacity and immutable intent BEFORE any child is spawned.
func (s *ExecutionStore) ReserveLaunch(ctx context.Context, snapshot domain.RunSnapshot) error {
	if snapshot.State != domain.RunReserved {
		return domain.ErrInvalid
	}
	// A committed launch must be representable in one bounded management frame.
	encoded, encodeErr := json.Marshal(recordLaunch(snapshot))
	if encodeErr != nil || len(encoded) > MaxRuntimeFrameBytes-1024 {
		return domain.ErrLimit
	}

	if checked, err := recordLaunch(snapshot).domainSnapshot(); err != nil || checked != snapshot {
		return domain.ErrInvalid
	}
	return s.update(ctx, func(doc *executionEnvelope) error {
		for _, r := range doc.Launches {
			if r.Identity.Run == snapshot.Identity.Run || r.Identity.Generation == snapshot.Identity.Generation {
				return domain.ErrDuplicate
			}
		}
		if len(doc.Launches) >= domain.MaxLaunchRequests {
			return domain.ErrLimit
		}
		found := false
		for _, p := range doc.Profiles {
			if p == recordProfile(snapshot.Profile) {
				found = true
				break
			}
		}
		if !found {
			return domain.ErrChanged
		}
		doc.Launches = append(doc.Launches, recordLaunch(snapshot))
		return nil
	})
}

// SaveLaunch advances only a previously reserved immutable binding; it cannot adopt a PID.
func (s *ExecutionStore) SaveLaunch(ctx context.Context, snapshot domain.RunSnapshot) error {
	if checked, err := recordLaunch(snapshot).domainSnapshot(); err != nil || checked != snapshot {
		return domain.ErrInvalid
	}
	return s.update(ctx, func(doc *executionEnvelope) error {
		for i, r := range doc.Launches {
			if r.Identity.Run != snapshot.Identity.Run {
				continue
			}
			old, err := r.domainSnapshot()
			if err != nil {
				return err
			}
			if old.Identity != snapshot.Identity || old.Target != snapshot.Target || old.Profile != snapshot.Profile || old.PublicationUncertain && !snapshot.PublicationUncertain {
				return domain.ErrChanged
			}
			if old.State == domain.RunExited && (snapshot.State != old.State || snapshot.ExitCode != old.ExitCode) {
				return domain.ErrChanged
			}
			if old.State != domain.RunReserved && (old.PID != snapshot.PID || snapshot.State == domain.RunReserved) {
				return domain.ErrChanged
			}
			doc.Launches[i] = recordLaunch(snapshot)
			return nil
		}
		return domain.ErrNotFound
	})
}

// ListLaunches reads prior epochs as history only, never as evidence of process liveness.
func (s *ExecutionStore) ListLaunches(ctx context.Context) ([]domain.RunSnapshot, error) {
	result := []domain.RunSnapshot{}
	err := s.metadata.locked(ctx, func(fd int) error {
		doc, _, err := s.read(fd)
		if err != nil {
			return err
		}
		for _, r := range doc.Launches {
			snapshot, e := r.domainSnapshot()
			if e != nil {
				return e
			}
			result = append(result, snapshot)
		}
		return s.metadata.verifyRoot(fd)
	})
	return result, err
}
