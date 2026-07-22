package usecase

import (
	"context"
	"encoding/hex"
	"strings"
	"unicode"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// OneShotRepairUsecase validates authoritative repair evidence and delegates
// one consistent dry-run or atomic apply operation.
type OneShotRepairUsecase interface {
	Preview(ctx context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error)
	Apply(ctx context.Context, params apptypes.OneShotRepairApplyParams) (apptypes.OneShotRepairResult, error)
}

const (
	maxOneShotRepairEntries          = 50_000
	maxOneShotRepairSessionIDBytes   = 1_024
	maxOneShotRepairEvidenceRefBytes = 4_096
)

type oneShotRepairUsecase struct {
	store       application.OneShotRepairStore
	safetyStore application.OneShotRepairSafetyStore
}

// NewOneShotRepairUsecase creates the evidence-backed repair use case.
func NewOneShotRepairUsecase(store application.OneShotRepairStore, safetyStore application.OneShotRepairSafetyStore) OneShotRepairUsecase {
	return &oneShotRepairUsecase{store: store, safetyStore: safetyStore}
}

func (u *oneShotRepairUsecase) Preview(ctx context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error) {
	if err := u.validate(params); err != nil {
		return apptypes.OneShotRepairResult{}, err
	}
	result, err := u.store.PreviewOneShotSessions(ctx, params)
	if err != nil {
		return apptypes.OneShotRepairResult{}, xerrors.Errorf("failed to preview one-shot sessions: %w", err)
	}
	return result, nil
}

func (u *oneShotRepairUsecase) Apply(ctx context.Context, params apptypes.OneShotRepairApplyParams) (apptypes.OneShotRepairResult, error) {
	if err := u.validate(params.Repair); err != nil {
		return apptypes.OneShotRepairResult{}, err
	}
	if u.safetyStore == nil {
		return apptypes.OneShotRepairResult{}, xerrors.New("one-shot repair safety store is not configured")
	}
	backupPath := strings.TrimSpace(params.BackupPath)
	if backupPath == "" || strings.ContainsFunc(backupPath, unicode.IsControl) {
		return apptypes.OneShotRepairResult{}, xerrors.New("one-shot repair apply requires a safe backup path")
	}
	if err := u.safetyStore.CreateBackup(ctx, backupPath, false); err != nil {
		return apptypes.OneShotRepairResult{}, xerrors.Errorf("failed to create required pre-repair backup: %w", err)
	}
	if err := u.safetyStore.Initialize(ctx); err != nil {
		return apptypes.OneShotRepairResult{}, xerrors.Errorf("failed to initialize store after backup: %w", err)
	}
	result, err := u.store.ApplyOneShotSessions(ctx, params.Repair)
	if err != nil {
		return apptypes.OneShotRepairResult{}, xerrors.Errorf("failed to apply one-shot repair: %w", err)
	}
	return result, nil
}

func (u *oneShotRepairUsecase) validate(params apptypes.OneShotRepairParams) error {
	if u.store == nil {
		return xerrors.New("one-shot repair store is not configured")
	}
	if params.StaleAfter <= 0 {
		return xerrors.New("one-shot repair stale duration must be greater than zero")
	}
	if params.Now.IsZero() {
		return xerrors.New("one-shot repair current time must not be zero")
	}
	hashBytes, err := hex.DecodeString(strings.TrimSpace(params.EvidenceHash))
	if err != nil || len(hashBytes) != 32 {
		return xerrors.New("one-shot repair evidence hash must be a SHA-256 hex digest")
	}
	if len(params.Entries) == 0 {
		return xerrors.New("one-shot repair evidence must contain at least one entry")
	}
	if len(params.Entries) > maxOneShotRepairEntries {
		return xerrors.Errorf("one-shot repair evidence exceeds %d entries", maxOneShotRepairEntries)
	}
	seen := make(map[types.SessionID]struct{}, len(params.Entries))
	for index, entry := range params.Entries {
		_, sessionIDErr := types.SessionIDFrom(entry.SessionID.String())
		if sessionIDErr != nil {
			return xerrors.Errorf("one-shot repair entry %d has invalid session ID: %w", index, sessionIDErr)
		}
		if strings.ContainsFunc(entry.SessionID.String(), unicode.IsControl) || len(entry.SessionID.String()) > maxOneShotRepairSessionIDBytes {
			return xerrors.Errorf("one-shot repair entry %d has unsafe session ID", index)
		}
		if _, duplicate := seen[entry.SessionID]; duplicate {
			return xerrors.Errorf("one-shot repair evidence repeats session %s", entry.SessionID)
		}
		seen[entry.SessionID] = struct{}{}
		if entry.RuntimeMode != types.RuntimeModeOneShot {
			return xerrors.Errorf("one-shot repair entry %s must explicitly attest runtime_mode=one_shot", entry.SessionID)
		}
		reason, err := types.TerminalReasonFrom(entry.TerminalReason.String())
		if err != nil || reason == types.TerminalReasonLegacyUnknown {
			return xerrors.Errorf("one-shot repair entry %s has unsupported terminal reason %q", entry.SessionID, entry.TerminalReason)
		}
		if entry.CompletedAt.IsZero() || entry.CompletedAt.After(params.Now) {
			return xerrors.Errorf("one-shot repair entry %s has invalid completion time", entry.SessionID)
		}
		if !knownOneShotRepairEvidenceSource(entry.EvidenceSource) || strings.TrimSpace(entry.EvidenceRef) == "" || strings.ContainsFunc(entry.EvidenceRef, unicode.IsControl) || len(entry.EvidenceRef) > maxOneShotRepairEvidenceRefBytes {
			return xerrors.Errorf("one-shot repair entry %s lacks authoritative process-exit evidence", entry.SessionID)
		}
	}
	return nil
}

func knownOneShotRepairEvidenceSource(source apptypes.OneShotRepairEvidenceSource) bool {
	switch source {
	case apptypes.OneShotRepairEvidenceSupervisedProcess,
		apptypes.OneShotRepairEvidenceCodexExec,
		apptypes.OneShotRepairEvidenceBatchRunner,
		apptypes.OneShotRepairEvidenceOperatorAttested:
		return true
	default:
		return false
	}
}
