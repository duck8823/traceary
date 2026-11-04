//go:build darwin || linux

package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
	"io"
	"reflect"
	"unicode/utf8"

	domain "github.com/duck8823/traceary/domain/runtime"
)

const maxRecoveryMetadataBytes = 16 * 1024 * 1024

// The private codec stores bookkeeping only. Reading it never reconstructs a
// process owner, continues a reserved attempt or certifies a native version.
type recoveryEnvelope struct {
	Version  int                   `json:"version"`
	Revision *uint64               `json:"revision"`
	Records  []recoveryRecord      `json:"records"`
	Attempts []resumeAttemptRecord `json:"attempts"`
}

type recoveryBindingRecord struct {
	Identity               launchIdentityRecord `json:"identity"`
	Worktree               worktreeRecord       `json:"worktree"`
	Profile                profileRecord        `json:"profile"`
	PID                    *int                 `json:"pid"`
	NativeSessionID        string               `json:"native_session_id"`
	NativeHistoryPath      string               `json:"native_history_path"`
	NativeNamespaceWitness string               `json:"native_namespace_witness"`
	Model                  string               `json:"model"`
	Provider               string               `json:"provider"`
}

type recoveryRecord struct {
	Binding      recoveryBindingRecord `json:"binding"`
	ExitObserved *bool                 `json:"exit_observed"`
	ExitCode     *int                  `json:"exit_code"`
}

type resumeAttemptRecord struct {
	RequestID           string                 `json:"request_id"`
	Binding             recoveryBindingRecord  `json:"binding"`
	PredecessorExitCode *int                   `json:"predecessor_exit_code"`
	Next                launchIdentityRecord   `json:"next"`
	Phase               string                 `json:"phase"`
	Confirmed           *recoveryBindingRecord `json:"confirmed,omitempty"`
}

func recordRecoveryBinding(binding domain.RecoveryBinding) recoveryBindingRecord {
	s := binding.Snapshot()
	w := s.Target.Worktree()
	return recoveryBindingRecord{
		Identity: launchIdentityRecord{s.Identity.Run, s.Identity.Generation, s.Identity.Epoch},
		Worktree: worktreeRecord{string(w.ID()), string(w.Project()), w.Path(), w.CommonDir(), w.Store(), w.Witness()},
		Profile:  recordProfile(s.Profile), PID: &s.PID, NativeSessionID: s.NativeSessionID,
		NativeHistoryPath: s.NativeHistoryPath, NativeNamespaceWitness: s.NativeNamespaceWitness, Model: s.Model, Provider: s.Provider,
	}
}

func (r recoveryBindingRecord) domainBinding() (domain.RecoveryBinding, error) {
	if r.PID == nil {
		return domain.RecoveryBinding{}, domain.ErrInvalid
	}
	worktree, err := domain.WorktreeOf(domain.ProjectID(r.Worktree.Project), r.Worktree.Path, r.Worktree.Common, r.Worktree.Store, r.Worktree.Witness)
	if err != nil || string(worktree.ID()) != r.Worktree.ID {
		return domain.RecoveryBinding{}, domain.ErrInvalid
	}
	profile, err := r.Profile.domainProfile()
	if err != nil {
		return domain.RecoveryBinding{}, err
	}
	target, err := domain.TargetOf(worktree, string(profile.ID()))
	if err != nil {
		return domain.RecoveryBinding{}, xerrors.Errorf("decode recovery target: %w", err)
	}
	binding, err := domain.RecoveryBindingOf(domain.RecoveryBindingState{
		Identity: domain.LaunchIdentity{Run: r.Identity.Run, Generation: r.Identity.Generation, Epoch: r.Identity.Epoch},
		Target:   target, Profile: profile, PID: *r.PID, NativeSessionID: r.NativeSessionID,
		NativeHistoryPath: r.NativeHistoryPath, NativeNamespaceWitness: r.NativeNamespaceWitness, Model: r.Model, Provider: r.Provider,
	})
	if err != nil {
		return domain.RecoveryBinding{}, xerrors.Errorf("decode recovery binding: %w", err)
	}
	return binding, nil
}

func recordRecovery(s domain.RecoveryRecordState) recoveryRecord {
	return recoveryRecord{Binding: recordRecoveryBinding(s.Binding), ExitObserved: &s.ExitObserved, ExitCode: &s.ExitCode}
}

func (r recoveryRecord) domainRecord() (*domain.RecoveryRecord, error) {
	if r.ExitObserved == nil || r.ExitCode == nil {
		return nil, domain.ErrInvalid
	}
	binding, err := r.Binding.domainBinding()
	if err != nil {
		return nil, err
	}
	record, err := domain.RecoveryRecordFrom(domain.RecoveryRecordState{Binding: binding, ExitObserved: *r.ExitObserved, ExitCode: *r.ExitCode})
	if err != nil {
		return nil, xerrors.Errorf("decode recovery record: %w", err)
	}
	return record, nil
}

func recordResume(s domain.ResumeAttemptState) resumeAttemptRecord {
	r := resumeAttemptRecord{RequestID: s.RequestID, Binding: recordRecoveryBinding(s.Binding), PredecessorExitCode: &s.PredecessorExitCode,
		Next: launchIdentityRecord{s.Next.Run, s.Next.Generation, s.Next.Epoch}, Phase: string(s.Phase)}
	if s.Phase == domain.ResumeConfirmed {
		value := recordRecoveryBinding(s.Confirmed)
		r.Confirmed = &value
	}
	return r
}

func (r resumeAttemptRecord) domainAttempt() (*domain.ResumeAttempt, error) {
	if r.PredecessorExitCode == nil {
		return nil, domain.ErrInvalid
	}
	binding, err := r.Binding.domainBinding()
	if err != nil {
		return nil, err
	}
	state := domain.ResumeAttemptState{RequestID: r.RequestID, Binding: binding, PredecessorExitCode: *r.PredecessorExitCode,
		Next: domain.LaunchIdentity{Run: r.Next.Run, Generation: r.Next.Generation, Epoch: r.Next.Epoch}, Phase: domain.ResumePhase(r.Phase)}
	if r.Confirmed != nil {
		state.Confirmed, err = r.Confirmed.domainBinding()
		if err != nil {
			return nil, err
		}
	}
	attempt, err := domain.ResumeAttemptFrom(state)
	if err != nil {
		return nil, xerrors.Errorf("decode resume attempt: %w", err)
	}
	return attempt, nil
}

func encodeRecovery(doc recoveryEnvelope, execution executionEnvelope) ([]byte, error) {
	if err := validateRecovery(doc, execution); err != nil {
		return nil, err
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, domain.ErrInvalid
	}
	if len(data) > maxRecoveryMetadataBytes {
		return nil, domain.ErrLimit
	}
	return data, nil
}

func decodeRecovery(data []byte, execution executionEnvelope) (recoveryEnvelope, error) {
	if len(data) > maxRecoveryMetadataBytes {
		return recoveryEnvelope{}, domain.ErrLimit
	}
	if !utf8.Valid(data) {
		return recoveryEnvelope{}, domain.ErrInvalid
	}
	if err := uniqueJSONValue(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return recoveryEnvelope{}, err
	}
	var header struct {
		Version *int `json:"version"`
	}
	if json.Unmarshal(data, &header) != nil || header.Version == nil {
		return recoveryEnvelope{}, domain.ErrInvalid
	}
	if *header.Version != 1 {
		return recoveryEnvelope{}, domain.ErrUnsupported
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var doc recoveryEnvelope
	if decoder.Decode(&doc) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return recoveryEnvelope{}, domain.ErrInvalid
	}
	encoded, err := encodeRecovery(doc, execution)
	if err != nil {
		return recoveryEnvelope{}, err
	}
	// Compare JSON trees (including exact key spelling and field presence), not
	// whitespace/order. Go's struct decoder otherwise accepts Unicode case-fold
	// aliases and null optional fields. UseNumber preserves full revision values.
	if !sameRecoveryJSON(data, encoded) {
		return recoveryEnvelope{}, domain.ErrInvalid
	}
	return doc, nil
}

func sameRecoveryJSON(a, b []byte) bool {
	var values [2]any
	for i, data := range [][]byte{a, b} {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if decoder.Decode(&values[i]) != nil {
			return false
		}
	}
	return reflect.DeepEqual(values[0], values[1])
}

func validateRecovery(doc recoveryEnvelope, execution executionEnvelope) error {
	if doc.Version != 1 || doc.Revision == nil || doc.Records == nil || doc.Attempts == nil {
		return domain.ErrInvalid
	}
	if len(doc.Records) > domain.MaxLaunchRequests || len(doc.Attempts) > domain.MaxLaunchRequests {
		return domain.ErrLimit
	}
	if *doc.Revision < uint64(len(doc.Records)+len(doc.Attempts)) {
		return domain.ErrInvalid
	}
	runs := make(map[string]domain.RunSnapshot, len(execution.Launches))
	runGenerations := make(map[string]string, len(execution.Launches))
	for _, row := range execution.Launches {
		run, err := row.domainSnapshot()
		if err != nil {
			return err
		}
		if _, exists := runs[run.Identity.Run]; exists {
			return domain.ErrInvalid
		}
		runs[run.Identity.Run] = run
		if _, exists := runGenerations[run.Identity.Generation]; exists {
			return domain.ErrInvalid
		}
		runGenerations[run.Identity.Generation] = run.Identity.Run
	}
	records := make(map[string]domain.RecoveryRecordState, len(doc.Records))
	for _, row := range doc.Records {
		record, err := row.domainRecord()
		if err != nil {
			return err
		}
		state := record.Snapshot()
		binding := state.Binding.Snapshot()
		run, found := runs[binding.Identity.Run]
		if !found || !recoveryBindingMatchesRun(binding, run) || run.State == domain.RunReserved || state.ExitObserved && (run.State != domain.RunExited || run.ExitCode != state.ExitCode) {
			return domain.ErrInvalid
		}
		if _, exists := records[binding.Identity.Run]; exists {
			return domain.ErrInvalid
		}
		records[binding.Identity.Run] = state
		if !recoveryFrameFits(row) {
			return domain.ErrLimit
		}
	}
	requests, successors, generations := map[string]bool{}, map[string]bool{}, map[string]bool{}
	occupiedOrigins, occupiedConversations := map[string]bool{}, map[string]bool{}
	for _, row := range doc.Attempts {
		attempt, err := row.domainAttempt()
		if err != nil {
			return err
		}
		s := attempt.Snapshot()
		origin := s.Binding.Snapshot().Identity.Run
		record, found := records[origin]
		if !found || !record.ExitObserved || record.Binding != s.Binding || record.ExitCode != s.PredecessorExitCode || requests[s.RequestID] || successors[s.Next.Run] || generations[s.Next.Generation] {
			return domain.ErrInvalid
		}
		requests[s.RequestID], successors[s.Next.Run], generations[s.Next.Generation] = true, true, true
		if run, exists := runGenerations[s.Next.Generation]; exists && run != s.Next.Run {
			return domain.ErrInvalid
		}
		next, exists := runs[s.Next.Run]
		if exists && (next.Identity != s.Next || next.Target != s.Binding.Snapshot().Target || next.Profile != s.Binding.Snapshot().Profile) {
			return domain.ErrInvalid
		}
		switch s.Phase {
		case domain.ResumeStarted, domain.ResumeConfirmed:
			if !exists || next.State == domain.RunReserved {
				return domain.ErrInvalid
			}
		case domain.ResumeFailedBeforeStart:
			if exists && next.State != domain.RunReserved {
				return domain.ErrInvalid
			}
		}
		if s.Phase == domain.ResumeConfirmed && !recoveryBindingMatchesRun(s.Confirmed.Snapshot(), next) {
			return domain.ErrInvalid
		}
		if s.Phase != domain.ResumeFailedBeforeStart {
			if occupiedOrigins[origin] {
				return domain.ErrInvalid
			}
			occupiedOrigins[origin] = true
		}
		active := s.Phase != domain.ResumeFailedBeforeStart && (s.Phase != domain.ResumeConfirmed || next.State != domain.RunExited)
		key := s.Binding.ConversationKey()
		if active && occupiedConversations[key] {
			return domain.ErrInvalid
		}
		if active {
			occupiedConversations[key] = true
		}
		if !recoveryFrameFits(row) {
			return domain.ErrLimit
		}
	}
	return nil
}

func recoveryBindingMatchesRun(binding domain.RecoveryBindingState, run domain.RunSnapshot) bool {
	return binding.Identity == run.Identity && binding.Target == run.Target && binding.Profile == run.Profile && binding.PID == run.PID
}

func recoveryFrameFits(value any) bool {
	encoded, err := json.Marshal(value)
	return err == nil && len(encoded) <= MaxRuntimeFrameBytes-1024
}

// readRecovery shares the execution root lock held by its caller. Missing data
// means no bindings, never evidence of an exited or resumable native process.
func (s *ExecutionStore) readRecovery(fd int, execution executionEnvelope) (recoveryEnvelope, *unix.Stat_t, error) {
	data, stamp, err := s.metadata.readMetadata(fd, "recovery.json", min(int64(maxRecoveryMetadataBytes), s.metadata.budget))
	if err != nil {
		return recoveryEnvelope{}, nil, err
	}
	if stamp == nil {
		revision := uint64(0)
		return recoveryEnvelope{Version: 1, Revision: &revision, Records: []recoveryRecord{}, Attempts: []resumeAttemptRecord{}}, nil, nil
	}
	doc, err := decodeRecovery(data, execution)
	return doc, stamp, err
}
