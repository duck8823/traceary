//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
	"time"
)

const agyMetadataRootPrefix = "traceary-agy-metadata-"

// This is last local delivery, not native ordering/currentness or a binding.
type agyMetadataObservation struct {
	Sequence                                        uint64
	Status                                          string
	Latest                                          antigravityStatuslineSnapshot
	FirstSession, FirstModel                        string
	SessionChanged, ModelChanged, Uncertain, Closed bool
}
type agyMetadataPlan struct {
	Identity launchIdentityRecord `json:"identity"`
	Worktree worktreeRecord       `json:"worktree"`
	Profile  profileRecord        `json:"profile"`
}

func (p agyMetadataPlan) snapshot() (domain.RunSnapshot, error) {
	zero := 0
	uncertain := false
	s, err := (launchRecord{Identity: p.Identity, Worktree: p.Worktree, Profile: p.Profile, State: string(domain.RunReserved), PID: &zero, ExitCode: &zero, Uncertain: &uncertain}).domainSnapshot()
	if err != nil {
		return domain.RunSnapshot{}, err
	}
	if p.Profile.Host != "antigravity" || p.Profile.Version != "1.5.0" {
		return domain.RunSnapshot{}, domain.ErrUnsupported
	}
	return s, nil
}

type agyMetadataIntent struct {
	Version int             `json:"version"`
	Witness string          `json:"root_witness"`
	Plan    agyMetadataPlan `json:"plan"`
}
type agyMetadataSnapshotRecord struct {
	Session string `json:"session_id"`
	CWD     string `json:"cwd"`
	Project string `json:"project_dir"`
	Model   string `json:"model"`
	Account bool   `json:"account_present"`
}

func recordAgySnapshot(s antigravityStatuslineSnapshot) agyMetadataSnapshotRecord {
	return agyMetadataSnapshotRecord{s.SessionID, s.CWD, s.ProjectDir, s.Model, s.AccountPresent}
}
func (s agyMetadataSnapshotRecord) snapshot() antigravityStatuslineSnapshot {
	return antigravityStatuslineSnapshot{SessionID: s.Session, CWD: s.CWD, ProjectDir: s.Project, Model: s.Model, AccountPresent: s.Account}
}
func agyMetadataSession(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value && id.Version() == 4 && id.Variant() == uuid.RFC4122
}
func agyMetadataModel(value string) bool {
	switch value {
	case "gemini-4-argon-medium", "gemini-4-argon-high", "gemini-4-argon-low":
		return true
	default:
		return false
	}
}

type agyMetadataObservationRecord struct {
	Version        int                       `json:"version"`
	Witness        string                    `json:"root_witness"`
	Sequence       uint64                    `json:"sequence"`
	Status         string                    `json:"status"`
	Latest         agyMetadataSnapshotRecord `json:"latest"`
	FirstSession   string                    `json:"first_session"`
	FirstModel     string                    `json:"first_model"`
	SessionChanged bool                      `json:"session_changed"`
	ModelChanged   bool                      `json:"model_changed"`
	Uncertain      bool                      `json:"uncertain"`
	FirstProject   string                    `json:"first_project"`
	FirstAccount   bool                      `json:"first_account"`
}

func (r agyMetadataObservationRecord) observation() agyMetadataObservation {
	return agyMetadataObservation{Sequence: r.Sequence, Status: r.Status, Latest: r.Latest.snapshot(), FirstSession: r.FirstSession, FirstModel: r.FirstModel, SessionChanged: r.SessionChanged, ModelChanged: r.ModelChanged, Uncertain: r.Uncertain}
}
func (r agyMetadataObservationRecord) valid(intent agyMetadataIntent) bool {
	if r.Version != 1 || r.Witness != intent.Witness || r.Sequence == 0 {
		return false
	}
	if r.Uncertain {
		if r.Status != "unavailable" {
			return false
		}
	} else if r.Status != "initializing" && r.Status != "observed" {
		return false
	}
	if (r.SessionChanged || r.ModelChanged) && !r.Uncertain {
		return false
	}
	if r.FirstSession == "" {
		if r.FirstModel != "" || r.FirstProject != "" || r.FirstAccount {
			return false
		}
	} else if !agyMetadataSession(r.FirstSession) || !agyMetadataModel(r.FirstModel) || !canonicalNativePath(r.FirstProject) {
		return false
	}
	if r.Latest == (agyMetadataSnapshotRecord{}) {
		return r.Status != "observed"
	}
	if !agyMetadataSession(r.Latest.Session) || r.Latest.CWD != intent.Plan.Worktree.Path || !canonicalNativePath(r.Latest.Project) || !agyMetadataModel(r.Latest.Model) || r.FirstSession == "" {
		return false
	}
	return (r.Latest.Session == r.FirstSession || r.SessionChanged) && (r.Latest.Model == r.FirstModel || r.ModelChanged) && (r.Latest.Project == r.FirstProject && r.Latest.Account == r.FirstAccount || r.Uncertain)
}

// next advances local delivery state only. Native ordering/currentness is not
// inferred; once any relevant reduced state contradicts prior evidence it stays
// unavailable, even if later bytes equal an earlier snapshot.
func (r agyMetadataObservationRecord) next(intent agyMetadataIntent, snapshot antigravityStatuslineSnapshot, decodeErr error) (agyMetadataObservationRecord, error) {
	if r.Sequence != 0 && !r.valid(intent) {
		return agyMetadataObservationRecord{}, domain.ErrChanged
	}
	if decodeErr == nil && snapshot.CWD != intent.Plan.Worktree.Path {
		snapshot = antigravityStatuslineSnapshot{}
		decodeErr = domain.ErrChanged
	}
	overflow := r.Sequence == math.MaxUint64
	r.Version = 1
	r.Witness = intent.Witness
	if r.Sequence == math.MaxUint64 {
		r.Status = "unavailable"
		r.Uncertain = true
		r.Latest = agyMetadataSnapshotRecord{}
	} else {
		r.Sequence++
		if decodeErr == nil {
			r.Latest = recordAgySnapshot(snapshot)
			if r.FirstSession == "" {
				r.FirstSession = snapshot.SessionID
				r.FirstModel = snapshot.Model
				r.FirstProject = snapshot.ProjectDir
				r.FirstAccount = snapshot.AccountPresent
			}
			r.SessionChanged = r.SessionChanged || r.FirstSession != snapshot.SessionID
			r.ModelChanged = r.ModelChanged || r.FirstModel != snapshot.Model
			r.Uncertain = r.Uncertain || r.SessionChanged || r.ModelChanged || r.FirstProject != snapshot.ProjectDir || r.FirstAccount != snapshot.AccountPresent
			r.Status = "observed"
		} else {
			r.Latest = agyMetadataSnapshotRecord{}
			r.Status = "initializing"
			r.Uncertain = r.Uncertain || !errors.Is(decodeErr, domain.ErrNotFound) || r.FirstSession != ""
		}
	}
	if r.Uncertain {
		r.Status = "unavailable"
	}
	if !r.valid(intent) {
		return agyMetadataObservationRecord{}, domain.ErrInvalid
	}

	if overflow {
		return r, domain.ErrLimit
	}
	return r, decodeErr
}

type agyMetadataSlot struct {
	metadata *RegistryStore
	intent   agyMetadataIntent
	settings []byte
}

func agyMetadataRoot(root string) bool {
	parent, err := nativeStartTempParent()
	return err == nil && canonicalNativePath(root) && filepath.Dir(root) == parent && strings.HasPrefix(filepath.Base(root), agyMetadataRootPrefix) && len(filepath.Base(root)) > len(agyMetadataRootPrefix)
}
func newAgyMetadataSlot(ctx context.Context, s domain.RunSnapshot) (*agyMetadataSlot, error) {
	if s.State != domain.RunReserved || s.PID != 0 || s.ExitCode != 0 || s.PublicationUncertain {
		return nil, domain.ErrInvalid
	}
	r := recordLaunch(s)
	plan := agyMetadataPlan{r.Identity, r.Worktree, r.Profile}
	if _, err := plan.snapshot(); err != nil {
		return nil, err
	}
	parent, err := nativeStartTempParent()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, xerrors.Errorf("create AGY metadata intent: %w", err)
	}
	root, err := os.MkdirTemp(parent, agyMetadataRootPrefix)
	if err != nil {
		return nil, domain.ErrUnsafe
	}
	m, err := OpenRegistryStore(root, nativeStartSlotBudget)
	if err != nil {
		_ = os.Remove(root)
		return nil, err
	}
	slot := &agyMetadataSlot{metadata: m, intent: agyMetadataIntent{Version: 1, Witness: m.witness, Plan: plan}}
	err = m.locked(ctx, func(fd int) error {
		data, e := json.Marshal(slot.intent)
		if e != nil {
			return domain.ErrInvalid
		}
		if len(data) > int(maxNativeMarkerBytes) {
			return domain.ErrLimit
		}
		return m.writeMetadata(ctx, fd, "intent.json", data, nil)
	})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = slot.close(cleanup)
		cancel()
		return nil, err
	}
	return slot, nil
}
func readAgyMetadataIntent(m *RegistryStore, fd int) (agyMetadataIntent, error) {
	data, stamp, err := m.readMetadata(fd, "intent.json", maxNativeMarkerBytes)
	if err != nil {
		return agyMetadataIntent{}, err
	}
	if stamp == nil {
		return agyMetadataIntent{}, domain.ErrNotFound
	}
	var intent agyMetadataIntent
	if err = decodeNativePrivate(data, &intent); err != nil {
		return agyMetadataIntent{}, err
	}
	if intent.Version != 1 || intent.Witness != m.witness {
		return agyMetadataIntent{}, domain.ErrChanged
	}
	if _, err = intent.Plan.snapshot(); err != nil {
		return agyMetadataIntent{}, err
	}
	return intent, nil
}
func readAgyMetadataObservation(m *RegistryStore, fd int, intent agyMetadataIntent) (agyMetadataObservationRecord, *unix.Stat_t, error) {
	data, stamp, err := m.readMetadata(fd, "observation.json", maxNativeMarkerBytes)
	if err != nil || stamp == nil {
		return agyMetadataObservationRecord{}, stamp, err
	}
	var record agyMetadataObservationRecord
	if err = decodeNativePrivate(data, &record); err != nil {
		return agyMetadataObservationRecord{}, nil, err
	}
	if !record.valid(intent) {
		return agyMetadataObservationRecord{}, nil, domain.ErrChanged
	}
	return record, stamp, nil
}
func (s *agyMetadataSlot) read(ctx context.Context) (agyMetadataObservation, error) {
	var result agyMetadataObservation
	err := s.metadata.locked(ctx, func(fd int) error {
		intent, e := readAgyMetadataIntent(s.metadata, fd)
		if e != nil {
			return e
		}
		if intent != s.intent {
			return domain.ErrChanged
		}
		record, stamp, e := readAgyMetadataObservation(s.metadata, fd, intent)
		if e != nil {
			return e
		}
		if stamp == nil {
			return domain.ErrNotFound
		}
		result = record.observation()
		return s.metadata.verifyRoot(fd)
	})
	if err != nil {
		return agyMetadataObservation{}, err
	}
	return result, nil
}
func agyMetadataHints(p agyMetadataPlan) map[string]string {
	return map[string]string{"TRACEARY_RUNTIME_RUN_ID": p.Identity.Run, "TRACEARY_RUNTIME_GENERATION": p.Identity.Generation, "TRACEARY_RUNTIME_EPOCH": p.Identity.Epoch, "TRACEARY_DB_PATH": p.Worktree.Store}
}

// AntigravityStatuslineSink writes reduced local observations only. Its root is
// existing/open-only, independent of core SQL, native control or RecoveryBinding.
type AntigravityStatuslineSink struct{ hints map[string]string }

// Observe opens only an existing matching intent and never grants authority.
func (s AntigravityStatuslineSink) Observe(ctx context.Context, root string, input io.Reader) error {
	if !agyMetadataRoot(root) || input == nil {
		return domain.ErrInvalid
	}
	m, err := OpenRegistryStore(root, nativeStartSlotBudget)
	if err != nil {
		return err
	}
	hints := s.hints
	if hints == nil {
		hints = make(map[string]string, 4)
		for _, key := range []string{"TRACEARY_RUNTIME_RUN_ID", "TRACEARY_RUNTIME_GENERATION", "TRACEARY_RUNTIME_EPOCH", "TRACEARY_DB_PATH"} {
			hints[key] = os.Getenv(key)
		}
	}
	var original agyMetadataIntent
	check := func(fd int) error {
		intent, e := readAgyMetadataIntent(m, fd)
		if e != nil {
			return e
		}
		for k, v := range agyMetadataHints(intent.Plan) {
			if hints[k] != v {
				return domain.ErrChanged
			}
		}
		original = intent
		return nil
	}
	// Reject foreign calls before consuming any native input.
	if err = m.locked(ctx, check); err != nil {
		return err
	}
	data, inputErr := io.ReadAll(io.LimitReader(input, maxNativeStartBytes+1))
	defer clear(data)
	snapshot, decodeErr := decodeAntigravityStatusline(data)
	if inputErr != nil {
		snapshot = antigravityStatuslineSnapshot{}
		decodeErr = domain.ErrInvalid
	}
	return m.locked(ctx, func(fd int) error {
		intent, e := readAgyMetadataIntent(m, fd)
		if e != nil {
			return e
		}
		if intent != original {
			return domain.ErrChanged
		}
		for k, v := range agyMetadataHints(intent.Plan) {
			if hints[k] != v {
				return domain.ErrChanged
			}
		}
		old, stamp, e := readAgyMetadataObservation(m, fd, intent)
		if e != nil {
			return e
		}
		record, outcome := old.next(intent, snapshot, decodeErr)
		if !record.valid(intent) {
			return domain.ErrInvalid
		}
		encoded, e := json.Marshal(record)
		if e != nil {
			return domain.ErrInvalid
		}
		if int64(len(encoded)) > maxNativeMarkerBytes {
			return domain.ErrLimit
		}
		if e = m.writeMetadata(ctx, fd, "observation.json", encoded, stamp); e != nil {
			return e
		}
		return outcome
	})
}

func (s *agyMetadataSlot) close(ctx context.Context) error {
	_, err := s.cleanup(ctx, false)
	return err
}
func (s *agyMetadataSlot) finish(ctx context.Context) (agyMetadataObservation, error) {
	return s.cleanup(ctx, true)
}

// Only the actual child owner calls finish after Wait. Closed means metadata
// lifetime ended, not independent proof of a process/native execution exit.
func (s *agyMetadataSlot) cleanup(ctx context.Context, observe bool) (agyMetadataObservation, error) {
	var result agyMetadataObservation
	var observedErr error
	if _, err := os.Lstat(s.metadata.root); errors.Is(err, os.ErrNotExist) {
		if observe {
			return result, domain.ErrNotFound
		}
		return result, nil
	}
	err := s.metadata.locked(ctx, func(fd int) error {
		copied, e := unix.Dup(fd)
		if e != nil {
			return domain.ErrUnsafe
		}
		file := os.NewFile(uintptr(copied), "private AGY metadata inventory")
		entries, e := file.ReadDir(6)
		_ = file.Close()
		if e != nil && !errors.Is(e, io.EOF) {
			return domain.ErrUnsafe
		}
		if len(entries) > 4 {
			return domain.ErrLimit
		}
		for _, entry := range entries {
			switch entry.Name() {
			case "intent.json", "observation.json", "settings.json", "registry.lock":
			default:
				return domain.ErrUnsupported
			}
			var st unix.Stat_t
			if unix.Fstatat(fd, entry.Name(), &st, unix.AT_SYMLINK_NOFOLLOW) != nil || !privateRegular(st) {
				return domain.ErrUnsafe
			}
		}
		intent, e := readAgyMetadataIntent(s.metadata, fd)
		if e != nil {
			if !errors.Is(e, domain.ErrNotFound) || len(entries) != 1 || entries[0].Name() != "registry.lock" {
				return domain.ErrChanged
			}
		} else {
			if intent != s.intent {
				return domain.ErrChanged
			}
			record, stamp, e := readAgyMetadataObservation(s.metadata, fd, intent)
			if e != nil {
				return e
			}
			if observe {
				if stamp == nil {
					observedErr = domain.ErrNotFound
				} else {
					result = record.observation()
					result.Closed = true
				}
			}
		}
		settings, stamp, e := s.metadata.readMetadata(fd, "settings.json", maxNativeMarkerBytes)
		if e != nil {
			return e
		}
		if stamp != nil && (len(s.settings) == 0 || !bytes.Equal(settings, s.settings)) {
			return domain.ErrChanged
		}
		if e = s.metadata.verifyRoot(fd); e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return xerrors.Errorf("finish AGY metadata: %w", e)
		}
		for _, entry := range entries {
			if unix.Unlinkat(fd, entry.Name(), 0) != nil {
				return domain.ErrUnsafe
			}
		}
		return nil
	})
	if err != nil {
		return agyMetadataObservation{}, err
	}
	if os.Remove(s.metadata.root) != nil {
		return agyMetadataObservation{}, domain.ErrChanged
	}
	if observedErr != nil {
		return agyMetadataObservation{}, observedErr
	}
	return result, nil
}
