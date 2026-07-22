package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const oneShotRepairManifestSchema = "one-shot-repair-evidence/v1"
const maxOneShotRepairManifestBytes = 64 << 20

type oneShotRepairManifest struct {
	SchemaVersion string                       `json:"schema_version"`
	Entries       []oneShotRepairManifestEntry `json:"entries"`
}

type oneShotRepairManifestEntry struct {
	SessionID      string    `json:"session_id"`
	RuntimeMode    string    `json:"runtime_mode"`
	TerminalReason string    `json:"terminal_reason"`
	CompletedAt    time.Time `json:"completed_at"`
	EvidenceSource string    `json:"evidence_source"`
	EvidenceRef    string    `json:"evidence_ref"`
}

type sessionRepairOneShotInput struct {
	dbPath       string
	evidenceFile string
	backupPath   string
	staleAfter   time.Duration
	apply        bool
	asJSON       bool
}

func (c *RootCLI) newSessionRepairOneShotCommand() *cobra.Command {
	input := sessionRepairOneShotInput{}
	cmd := &cobra.Command{
		Use:   "repair-one-shot",
		Short: Localize("Repair stale one-shot sessions from explicit process-exit evidence", "明示されたプロセス終了証拠から stale な完結型セッションを修復する"),
		Long: Localize(
			"Inspect stale sessions using an explicit evidence manifest. The command is a dry-run unless --apply is provided. Apply also requires --backup and creates that SQLite backup before the atomic repair transaction. Transcript text, idle time, and workspace membership are never treated as one-shot evidence.",
			"明示された証拠 manifest を使って stale session を検査します。--apply を付けない限り dry-run です。適用には --backup も必須で、原子的な修復 transaction の前に SQLite backup を作成します。transcript 本文、idle 時間、workspace 所属だけを完結型の証拠とは扱いません。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionRepairOneShot(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	cmd.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().StringVar(&input.evidenceFile, "evidence-file", "", Localize("authoritative one-shot evidence manifest", "完結型セッションの権威ある証拠 manifest"))
	cmd.Flags().DurationVar(&input.staleAfter, "stale-after", 24*time.Hour, Localize("require no activity for this duration", "この期間 activity がないことを要求する"))
	cmd.Flags().BoolVar(&input.apply, "apply", false, Localize("apply eligible repairs (default is dry-run)", "対象となる修復を適用する (既定は dry-run)"))
	cmd.Flags().StringVar(&input.backupPath, "backup", "", Localize("required pre-apply SQLite backup path", "適用前に必須の SQLite backup path"))
	cmd.Flags().BoolVar(&input.asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	if err := configureRequiredFlag(cmd, "evidence-file"); err != nil {
		cmd.RunE = func(*cobra.Command, []string) error { return err }
	}
	return cmd
}

func (c *RootCLI) runSessionRepairOneShot(ctx context.Context, output io.Writer, input sessionRepairOneShotInput) error {
	if c.storeManagement == nil || c.oneShotRepair == nil {
		return xerrors.New(Localize("one-shot repair dependencies are not configured", "完結型セッション修復の依存関係が設定されていません"))
	}
	if input.staleAfter <= 0 {
		return xerrors.New("--stale-after must be greater than 0")
	}
	if input.apply && strings.TrimSpace(input.backupPath) == "" {
		return xerrors.New(Localize("--backup is required with --apply", "--apply には --backup が必要です"))
	}
	if !input.apply && strings.TrimSpace(input.backupPath) != "" {
		return xerrors.New(Localize("--backup is only valid with --apply", "--backup は --apply と一緒に指定してください"))
	}
	manifest, evidenceHash, err := readOneShotRepairManifest(input.evidenceFile)
	if err != nil {
		return err
	}
	entries := make([]apptypes.OneShotRepairEvidenceEntry, 0, len(manifest.Entries))
	for index, raw := range manifest.Entries {
		if oneShotRepairManifestEntryHasControl(raw) {
			return xerrors.Errorf("invalid one-shot repair manifest entry %d: text fields must not contain control characters", index)
		}
		sessionID, err := types.SessionIDFrom(raw.SessionID)
		if err != nil {
			return xerrors.Errorf("invalid one-shot repair manifest entry %d session_id: %w", index, err)
		}
		mode, err := types.RuntimeModeFrom(raw.RuntimeMode)
		if err != nil {
			return xerrors.Errorf("invalid one-shot repair manifest entry %s runtime_mode: %w", sessionID, err)
		}
		reason, err := types.TerminalReasonFrom(raw.TerminalReason)
		if err != nil {
			return xerrors.Errorf("invalid one-shot repair manifest entry %s terminal_reason: %w", sessionID, err)
		}
		entries = append(entries, apptypes.OneShotRepairEvidenceEntry{
			SessionID: sessionID, RuntimeMode: mode, TerminalReason: reason, CompletedAt: raw.CompletedAt,
			EvidenceSource: apptypes.OneShotRepairEvidenceSource(strings.TrimSpace(raw.EvidenceSource)),
			EvidenceRef:    strings.TrimSpace(raw.EvidenceRef),
		})
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolvedDBPath)
	if input.apply {
		if err := c.storeManagement.CreateBackup(ctx, input.backupPath, false); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to create required pre-repair backup", "修復前に必須の backup を作成できませんでした"), err)
		}
		if err := c.storeManagement.Initialize(ctx); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "ストアの初期化に失敗しました"), err)
		}
	}
	params := apptypes.OneShotRepairParams{
		Entries: entries, EvidenceHash: evidenceHash, StaleAfter: input.staleAfter, Now: time.Now(),
	}
	var result apptypes.OneShotRepairResult
	if input.apply {
		result, err = c.oneShotRepair.Apply(ctx, params)
	} else {
		result, err = c.oneShotRepair.Preview(ctx, params)
	}
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to repair one-shot sessions", "完結型セッションを修復できませんでした"), err)
	}
	if input.asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return xerrors.Errorf("failed to print one-shot repair JSON: %w", err)
		}
		return nil
	}
	return writeOneShotRepairText(output, result)
}

func readOneShotRepairManifest(path string) (oneShotRepairManifest, string, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return oneShotRepairManifest{}, "", xerrors.Errorf("failed to read one-shot repair evidence manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxOneShotRepairManifestBytes+1))
	if err != nil {
		return oneShotRepairManifest{}, "", xerrors.Errorf("failed to read one-shot repair evidence manifest: %w", err)
	}
	if len(data) > maxOneShotRepairManifestBytes {
		return oneShotRepairManifest{}, "", xerrors.Errorf("one-shot repair evidence manifest exceeds %d bytes", maxOneShotRepairManifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest oneShotRepairManifest
	if err := decoder.Decode(&manifest); err != nil {
		return oneShotRepairManifest{}, "", xerrors.Errorf("failed to decode one-shot repair evidence manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return oneShotRepairManifest{}, "", xerrors.New("one-shot repair evidence manifest contains trailing JSON")
		}
		return oneShotRepairManifest{}, "", xerrors.Errorf("failed to inspect one-shot repair evidence manifest trailer: %w", err)
	}
	if manifest.SchemaVersion != oneShotRepairManifestSchema {
		return oneShotRepairManifest{}, "", xerrors.Errorf("unsupported one-shot repair evidence schema %q", manifest.SchemaVersion)
	}
	digest := sha256.Sum256(data)
	return manifest, hex.EncodeToString(digest[:]), nil
}

func oneShotRepairManifestEntryHasControl(entry oneShotRepairManifestEntry) bool {
	for _, value := range []string{entry.SessionID, entry.RuntimeMode, entry.TerminalReason, entry.EvidenceSource, entry.EvidenceRef} {
		if strings.ContainsFunc(value, unicode.IsControl) {
			return true
		}
	}
	return false
}

func writeOneShotRepairText(output io.Writer, result apptypes.OneShotRepairResult) error {
	mode := "dry-run"
	if result.Applied {
		mode = "applied"
	}
	if _, err := fmt.Fprintf(output, "one-shot repair: %s evidence_sha256=%s changed=%d\n", mode, result.EvidenceHash, result.AppliedCount()); err != nil {
		return xerrors.Errorf("failed to print one-shot repair summary: %w", err)
	}
	if _, err := fmt.Fprintf(output, "before active=%d stale=%d completed=%d failed=%d\n", result.Before.ActiveCount, result.Before.StaleCount, result.Before.CompletedCount, result.Before.FailedCount); err != nil {
		return xerrors.Errorf("failed to print one-shot repair before statistics: %w", err)
	}
	if _, err := fmt.Fprintf(output, "after active=%d stale=%d completed=%d failed=%d\n", result.After.ActiveCount, result.After.StaleCount, result.After.CompletedCount, result.After.FailedCount); err != nil {
		return xerrors.Errorf("failed to print one-shot repair after statistics: %w", err)
	}
	for _, candidate := range result.Candidates {
		if _, err := fmt.Fprintf(
			output,
			"session=%s stored_mode=%s proposed_reason=%s completed_at=%s latest_activity_at=%s evidence_source=%s evidence_ref=%s eligible=%t applied=%t decision=%s\n",
			candidate.SessionID, candidate.StoredRuntimeMode, candidate.ProposedReason,
			candidate.CompletedAt.UTC().Format(time.RFC3339Nano), formatOptionalRepairTime(candidate.LatestActivityAt),
			candidate.EvidenceSource, candidate.EvidenceRef, candidate.Eligible, candidate.Applied, candidate.Decision,
		); err != nil {
			return xerrors.Errorf("failed to print one-shot repair candidate: %w", err)
		}
	}
	return nil
}

func formatOptionalRepairTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339Nano)
}
