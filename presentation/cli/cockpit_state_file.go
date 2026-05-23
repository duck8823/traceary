package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/xerrors"
)

const (
	cockpitStateLockRetryLimit = 100
	cockpitStateLockRetryDelay = 10 * time.Millisecond
	cockpitStateStaleLockAfter = 5 * time.Minute
)

type fileCockpitStateStore struct {
	path string
}

type cockpitStateFile struct {
	MemoryLastSeenAt string   `json:"memory_last_seen_at,omitempty"`
	EventLastSeenAt  string   `json:"event_last_seen_at,omitempty"`
	EventLastSeenIDs []string `json:"event_last_seen_ids,omitempty"`
}

// NewFileCockpitStateStore returns the local-first cockpit state store used by
// the default CLI wiring. Missing or unreadable state is treated as non-critical
// by cockpit callers; writes use an atomic replace under the user's state dir.
func NewFileCockpitStateStore() CockpitStateReader {
	return fileCockpitStateStore{path: defaultCockpitStatePath()}
}

func newFileCockpitStateStoreAt(path string) fileCockpitStateStore {
	return fileCockpitStateStore{path: path}
}

func defaultCockpitStatePath() string {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "traceary", "cockpit.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".traceary", "cockpit.json")
	}
	return filepath.Join(home, ".local", "state", "traceary", "cockpit.json")
}

func (s fileCockpitStateStore) MemoryLastSeenAt(context.Context) (time.Time, bool, error) {
	state, err := s.read()
	if err != nil {
		return time.Time{}, false, err
	}
	return parseCockpitStateTime(state.MemoryLastSeenAt)
}

func (s fileCockpitStateStore) EventLastSeenAt(context.Context) (time.Time, bool, error) {
	state, err := s.read()
	if err != nil {
		return time.Time{}, false, err
	}
	return parseCockpitStateTime(state.EventLastSeenAt)
}

func (s fileCockpitStateStore) EventLastSeenIDs(context.Context) ([]string, bool, error) {
	state, err := s.read()
	if err != nil {
		return nil, false, err
	}
	if len(state.EventLastSeenIDs) == 0 {
		return nil, false, nil
	}
	return normalizeCockpitStateIDs(state.EventLastSeenIDs), true, nil
}

func (s fileCockpitStateStore) MarkMemoryLastSeenAt(_ context.Context, at time.Time) error {
	return s.update(func(state *cockpitStateFile) (bool, error) {
		if shouldAdvanceCockpitStateTime(state.MemoryLastSeenAt, at) {
			state.MemoryLastSeenAt = at.UTC().Format(time.RFC3339Nano)
			return true, nil
		}
		return false, nil
	})
}

func (s fileCockpitStateStore) MarkEventLastSeenAt(_ context.Context, at time.Time, seenIDs []string) error {
	return s.update(func(state *cockpitStateFile) (bool, error) {
		parsed, ok, err := parseCockpitStateTime(state.EventLastSeenAt)
		if err != nil || !ok || at.After(parsed) {
			state.EventLastSeenAt = at.UTC().Format(time.RFC3339Nano)
			state.EventLastSeenIDs = normalizeCockpitStateIDs(seenIDs)
			return true, nil
		}
		if at.Equal(parsed) {
			mergedIDs := mergeCockpitStateIDs(state.EventLastSeenIDs, seenIDs)
			if !slices.Equal(mergedIDs, normalizeCockpitStateIDs(state.EventLastSeenIDs)) {
				state.EventLastSeenIDs = mergedIDs
				return true, nil
			}
		}
		return false, nil
	})
}

func (s fileCockpitStateStore) ResetCockpitState(context.Context) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return xerrors.Errorf("failed to create cockpit state directory: %w", err)
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to reset cockpit state: %w", err)
	}
	return nil
}

func (s fileCockpitStateStore) read() (cockpitStateFile, error) {
	if s.path == "" {
		return cockpitStateFile{}, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return cockpitStateFile{}, nil
		}
		return cockpitStateFile{}, xerrors.Errorf("failed to read cockpit state: %w", err)
	}
	var state cockpitStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return cockpitStateFile{}, xerrors.Errorf("failed to parse cockpit state: %w", err)
	}
	return state, nil
}

func (s fileCockpitStateStore) update(update func(*cockpitStateFile) (bool, error)) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return xerrors.Errorf("failed to create cockpit state directory: %w", err)
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	state, err := s.read()
	if err != nil {
		return err
	}
	changed, err := update(&state)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode cockpit state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".cockpit-*.json")
	if err != nil {
		return xerrors.Errorf("failed to create cockpit state temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to write cockpit state temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return xerrors.Errorf("failed to chmod cockpit state temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return xerrors.Errorf("failed to close cockpit state temp file: %w", err)
	}
	if err := replaceCockpitStateFile(tmpPath, s.path); err != nil {
		return err
	}
	return nil
}

func (s fileCockpitStateStore) lock() (func(), error) {
	lockPath := s.path + ".lock"
	for i := 0; ; i++ {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, xerrors.Errorf("failed to lock cockpit state: %w", err)
		}
		if err := removeStaleCockpitStateLock(lockPath); err != nil {
			return nil, err
		}
		if i >= cockpitStateLockRetryLimit {
			return nil, xerrors.Errorf("failed to lock cockpit state: timed out")
		}
		time.Sleep(cockpitStateLockRetryDelay)
	}
}

func removeStaleCockpitStateLock(lockPath string) error {
	info, err := os.Stat(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return xerrors.Errorf("failed to inspect cockpit state lock: %w", err)
	}
	if time.Since(info.ModTime()) <= cockpitStateStaleLockAfter {
		return nil
	}
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return xerrors.Errorf("failed to remove stale cockpit state lock: %w", err)
	}
	return nil
}

func parseCockpitStateTime(value string) (time.Time, bool, error) {
	if value == "" {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false, xerrors.Errorf("invalid cockpit state timestamp %q: %w", value, err)
	}
	return parsed, true, nil
}

func shouldAdvanceCockpitStateTime(existing string, candidate time.Time) bool {
	parsed, ok, err := parseCockpitStateTime(existing)
	if err != nil {
		return true
	}
	return !ok || candidate.After(parsed)
}

func normalizeCockpitStateIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	slices.Sort(normalized)
	return normalized
}

func mergeCockpitStateIDs(existing []string, incoming []string) []string {
	return normalizeCockpitStateIDs(append(slices.Clone(existing), incoming...))
}
