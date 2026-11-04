//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

const nativeStartRootPrefix = "traceary-native-session-"
const maxNativeMarkerBytes int64 = 32 * 1024
const nativeStartSlotBudget int64 = 128 * 1024

type nativeStartPlanRecord struct {
	Identity  launchIdentityRecord `json:"identity"`
	SessionID string               `json:"session_id"`
	CWD       string               `json:"cwd"`
	Store     string               `json:"store"`
	Namespace string               `json:"namespace"`
	Source    string               `json:"source"`
}

func recordNativeStartPlan(p nativeStartPlan) nativeStartPlanRecord {
	return nativeStartPlanRecord{Identity: launchIdentityRecord{Run: p.Identity.Run, Generation: p.Identity.Generation, Epoch: p.Identity.Epoch}, SessionID: p.SessionID, CWD: p.CWD, Store: p.Store, Namespace: p.Namespace, Source: p.Source}
}
func (p nativeStartPlanRecord) plan() nativeStartPlan {
	return nativeStartPlan{Identity: domain.LaunchIdentity{Run: p.Identity.Run, Generation: p.Identity.Generation, Epoch: p.Identity.Epoch}, SessionID: p.SessionID, CWD: p.CWD, Store: p.Store, Namespace: p.Namespace, Source: p.Source}
}

type nativeStartIntent struct {
	Version int                   `json:"version"`
	Witness string                `json:"root_witness"`
	Plan    nativeStartPlanRecord `json:"plan"`
}
type nativeStartMarker struct {
	Version    int                   `json:"version"`
	Witness    string                `json:"root_witness"`
	Plan       nativeStartPlanRecord `json:"plan"`
	Status     string                `json:"status"`
	Transcript string                `json:"transcript_path"`
}
type nativeStartSlot struct {
	metadata *RegistryStore
	intent   nativeStartIntent
}

func nativeStartTempParent() (string, error) {
	parent, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", domain.ErrUnsafe
	}
	return parent, nil
}
func nativeStartRoot(root string) bool {
	parent, err := nativeStartTempParent()
	return err == nil && canonicalNativePath(root) && filepath.Dir(root) == parent && strings.HasPrefix(filepath.Base(root), nativeStartRootPrefix) && len(filepath.Base(root)) > len(nativeStartRootPrefix)
}
func decodeNativePrivate(data []byte, out any) error {
	if !utf8.Valid(data) {
		return domain.ErrInvalid
	}
	if err := uniqueJSONValue(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return domain.ErrInvalid
	}
	canonical, err := json.Marshal(out)
	if err != nil || !sameRecoveryJSON(data, canonical) {
		return domain.ErrInvalid
	}
	return nil
}
func readNativeIntent(m *RegistryStore, fd int) (nativeStartIntent, error) {
	data, stamp, err := m.readMetadata(fd, "intent.json", maxNativeMarkerBytes)
	if err != nil {
		return nativeStartIntent{}, err
	}
	if stamp == nil {
		return nativeStartIntent{}, domain.ErrNotFound
	}
	var intent nativeStartIntent
	if err := decodeNativePrivate(data, &intent); err != nil {
		return nativeStartIntent{}, err
	}
	if intent.Version != 1 || intent.Witness != m.witness || !intent.Plan.plan().valid() {
		return nativeStartIntent{}, domain.ErrInvalid
	}
	return intent, nil
}
func newNativeStartSlot(ctx context.Context, plan nativeStartPlan) (*nativeStartSlot, error) {
	if !plan.valid() {
		return nil, domain.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, xerrors.Errorf("create native observation intent: %w", err)
	}
	parent, err := nativeStartTempParent()
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(parent, nativeStartRootPrefix)
	if err != nil {
		return nil, domain.ErrUnsafe
	}
	m, err := OpenRegistryStore(root, nativeStartSlotBudget)
	if err != nil {
		return nil, err
	}
	slot := &nativeStartSlot{metadata: m, intent: nativeStartIntent{Version: 1, Witness: m.witness, Plan: recordNativeStartPlan(plan)}}
	err = m.locked(ctx, func(fd int) error {
		data, err := json.Marshal(slot.intent)
		if err != nil {
			return domain.ErrInvalid
		}
		if int64(len(data)) > maxNativeMarkerBytes {
			return domain.ErrLimit
		}
		return m.writeMetadata(ctx, fd, "intent.json", data, nil)
	})
	if err != nil {
		_ = slot.close(context.Background())
		return nil, err
	}
	return slot, nil
}
func readNativeMarker(m *RegistryStore, fd int, intent nativeStartIntent) (nativeStartMarker, *unix.Stat_t, error) {
	data, stamp, err := m.readMetadata(fd, "source.json", maxNativeMarkerBytes)
	if err != nil {
		return nativeStartMarker{}, nil, err
	}
	if stamp == nil {
		return nativeStartMarker{}, nil, nil
	}
	var marker nativeStartMarker
	if err := decodeNativePrivate(data, &marker); err != nil {
		return nativeStartMarker{}, nil, err
	}
	if marker.Version != 1 || marker.Witness != intent.Witness || marker.Plan != intent.Plan {
		return nativeStartMarker{}, nil, domain.ErrChanged
	}
	switch marker.Status {
	case "bound":
		// Reapply the same path/namespace representation rules without exposing raw
		// stdin or accepting a public verified flag as binding authority.
		p := intent.Plan.plan()
		encoded, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": p.SessionID, "cwd": p.CWD, "source": p.Source, "transcript_path": marker.Transcript})
		if _, err := decodeNativeStart(encoded, p, nativePlanHints(p)); err != nil {
			return nativeStartMarker{}, nil, err
		}
	case "unavailable":
		if marker.Transcript != "" {
			return nativeStartMarker{}, nil, domain.ErrInvalid
		}
	default:
		return nativeStartMarker{}, nil, domain.ErrInvalid
	}
	return marker, stamp, nil
}
func nativePlanHints(p nativeStartPlan) map[string]string {
	return map[string]string{"TRACEARY_RUNTIME_RUN_ID": p.Identity.Run, "TRACEARY_RUNTIME_GENERATION": p.Identity.Generation, "TRACEARY_RUNTIME_EPOCH": p.Identity.Epoch, "TRACEARY_DB_PATH": p.Store}
}
func (s *nativeStartSlot) read(ctx context.Context) (nativeStartEvent, error) {
	var event nativeStartEvent
	err := s.metadata.locked(ctx, func(fd int) error {
		intent, err := readNativeIntent(s.metadata, fd)
		if err != nil {
			return err
		}
		if intent != s.intent {
			return domain.ErrChanged
		}
		marker, stamp, err := readNativeMarker(s.metadata, fd, intent)
		if err != nil {
			return err
		}
		if stamp == nil {
			return domain.ErrNotFound
		}
		if marker.Status != "bound" {
			return domain.ErrChanged
		}
		event = nativeStartEvent{Plan: intent.Plan.plan(), TranscriptPath: marker.Transcript}
		return s.metadata.verifyRoot(fd)
	})
	return event, err
}

// NativeSessionMarkerSink consumes only a private per-owned-run intent. It
// writes reduced native metadata, never core SQL, terminal bodies, approvals or
// RecoveryBinding. The caller's plumbing entry must keep optional failure silent.
// Private files share the existing same-UID trust assumption, not a new auth API.
type NativeSessionMarkerSink struct{ hints map[string]string }

// Observe never creates arbitrary roots or accepts an arbitrary output filename.
// Actual source/program/child/history verification is still required by owner.
func (s NativeSessionMarkerSink) Observe(ctx context.Context, root string, input io.Reader) error {
	if !nativeStartRoot(root) || input == nil {
		return domain.ErrInvalid
	}
	m, err := OpenRegistryStore(root, nativeStartSlotBudget)
	if err != nil {
		return err
	}
	data, inputErr := io.ReadAll(io.LimitReader(input, maxNativeStartBytes+1))
	defer clear(data)
	hints := s.hints
	if hints == nil {
		hints = make(map[string]string, 4)
		for _, key := range []string{"TRACEARY_RUNTIME_RUN_ID", "TRACEARY_RUNTIME_GENERATION", "TRACEARY_RUNTIME_EPOCH", "TRACEARY_DB_PATH"} {
			hints[key] = os.Getenv(key)
		}
	}
	return m.locked(ctx, func(fd int) error {
		intent, err := readNativeIntent(m, fd)
		if err != nil {
			return err
		}
		for key, value := range nativePlanHints(intent.Plan.plan()) {
			if hints[key] != value {
				return domain.ErrChanged
			}
		}
		old, stamp, err := readNativeMarker(m, fd, intent)
		if err != nil {
			return err
		}
		if stamp != nil && old.Status == "unavailable" {
			return domain.ErrChanged
		}
		event, decodeErr := decodeNativeStartEvent(data, intent.Plan.plan(), hints, stamp != nil && old.Status == "bound")
		if inputErr != nil {
			// An incomplete latest callback must invalidate earlier evidence, even
			// when its partial bytes happen to form a complete JSON object.
			decodeErr = domain.ErrInvalid
		}
		marker := nativeStartMarker{Version: 1, Witness: intent.Witness, Plan: intent.Plan, Status: "bound", Transcript: event.TranscriptPath}
		if decodeErr != nil {
			marker.Status = "unavailable"
			marker.Transcript = ""
		}
		if stamp != nil && marker == old {
			return decodeErr
		}
		encoded, err := json.Marshal(marker)
		if err != nil {
			return domain.ErrInvalid
		}
		if int64(len(encoded)) > maxNativeMarkerBytes {
			return domain.ErrLimit
		}
		if err = m.writeMetadata(ctx, fd, "source.json", encoded, stamp); err != nil {
			return err
		}
		return decodeErr
	})
}

// close deletes only generated known metadata. Unknown entries and exchanged
// roots/files fail closed, never recursive cleanup or native-history deletion.
// The child owner must snapshot latest marker state after actual Wait first.
func (s *nativeStartSlot) close(ctx context.Context) error {
	return s.cleanup(ctx, nil)
}

// finish consumes the latest observation and removes the slot under the same
// lock. A delayed invalidation cannot slip between snapshot and deletion.
// Only the actual child owner may call this after its real Wait.
func (s *nativeStartSlot) finish(ctx context.Context) (nativeStartEvent, error) {
	var event nativeStartEvent
	var observedErr error
	err := s.cleanup(ctx, func(fd int) {
		marker, stamp, err := readNativeMarker(s.metadata, fd, s.intent)
		if err != nil {
			observedErr = err
			return
		}
		if stamp == nil {
			observedErr = domain.ErrNotFound
			return
		}
		if marker.Status != "bound" {
			observedErr = domain.ErrChanged
			return
		}
		event = nativeStartEvent{Plan: s.intent.Plan.plan(), TranscriptPath: marker.Transcript}
	})
	if err != nil {
		return nativeStartEvent{}, err
	}
	if observedErr != nil {
		return nativeStartEvent{}, observedErr
	}
	// A missing root is idempotent cleanup, but never binding evidence.
	if event == (nativeStartEvent{}) {
		return event, domain.ErrNotFound
	}
	return event, nil
}

func (s *nativeStartSlot) cleanup(ctx context.Context, observe func(int)) error {
	if _, err := os.Lstat(s.metadata.root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	err := s.metadata.locked(ctx, func(fd int) error {
		copyFD, err := unix.Dup(fd)
		if err != nil {
			return domain.ErrUnsafe
		}
		file := os.NewFile(uintptr(copyFD), "native observation inventory")
		entries, readErr := file.ReadDir(6)
		_ = file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return domain.ErrUnsafe
		}
		if len(entries) > 4 {
			return domain.ErrLimit
		}
		for _, entry := range entries {
			switch entry.Name() {
			case "intent.json", "source.json", "settings.json", "registry.lock":
			default:
				return domain.ErrUnsupported
			}
			var stamp unix.Stat_t
			if unix.Fstatat(fd, entry.Name(), &stamp, unix.AT_SYMLINK_NOFOLLOW) != nil || !privateRegular(stamp) {
				return domain.ErrUnsafe
			}
		}
		intent, err := readNativeIntent(s.metadata, fd)
		if err != nil {
			if !errors.Is(err, domain.ErrNotFound) || len(entries) != 1 || entries[0].Name() != "registry.lock" {
				return domain.ErrChanged
			}
		} else if intent != s.intent {
			return domain.ErrChanged
		}
		if err := s.metadata.verifyRoot(fd); err != nil {
			return err
		}
		if observe != nil {
			observe(fd)
		}
		if err := ctx.Err(); err != nil {
			return xerrors.Errorf("finish native metadata: %w", err)
		}
		for _, entry := range entries {
			if err := unix.Unlinkat(fd, entry.Name(), 0); err != nil {
				return domain.ErrUnsafe
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err = os.Remove(s.metadata.root); err != nil {
		return domain.ErrChanged
	}
	return nil
}
