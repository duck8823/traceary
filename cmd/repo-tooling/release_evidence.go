package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

const (
	bodyFreeEvidenceSchema        = "traceary.body-free-release-evidence.v1"
	bodyFreeEvidencePhaseAMarker  = "TRACEARY_PHASE_A_EVIDENCE="
	bodyFreeEvidencePhaseBCMarker = "TRACEARY_PHASE_BC_EVIDENCE="
	bodyFreeEvidencePhaseDMarker  = "TRACEARY_PHASE_D_EVIDENCE="

	v0330EvidenceRequiredScratchBytes uint64 = 12 << 30
	v0330EvidenceCommandOutputBytes          = 2 << 20
	v0330EvidenceRunTimeout                  = 45 * time.Minute
)

var bodyFreeEvidenceGoVersionRe = regexp.MustCompile(`^go[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[a-z0-9.-]+)?$`)

// BodyFreeEvidence is the only durable artifact emitted by the v0.33.0
// multi-GiB release probe. It deliberately has no free-form string, identity,
// cursor, body, prompt, workspace, or path field.
type BodyFreeEvidence struct {
	Schema      string                    `json:"schema"`
	Status      string                    `json:"status"`
	BlockReason string                    `json:"block_reason,omitempty"`
	Host        bodyFreeEvidenceHost      `json:"host"`
	Preflight   bodyFreeEvidencePreflight `json:"preflight"`
	PhaseA      *bodyFreeEvidencePhaseA   `json:"phase_a,omitempty"`
	PhaseB      *bodyFreeEvidencePhaseB   `json:"phase_b,omitempty"`
	PhaseC      []bodyFreeEvidenceProbe   `json:"phase_c,omitempty"`
	PhaseD      *bodyFreeEvidencePhaseD   `json:"phase_d,omitempty"`
	PhaseE      *bodyFreeEvidencePhaseE   `json:"phase_e,omitempty"`
	Privacy     bodyFreeEvidencePrivacy   `json:"privacy"`
}

type bodyFreeEvidenceHost struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
}

type bodyFreeEvidencePreflight struct {
	RequiredScratchBytes  uint64 `json:"required_scratch_bytes"`
	AvailableScratchBytes uint64 `json:"available_scratch_bytes"`
	Capable               bool   `json:"capable"`
	Reason                string `json:"reason,omitempty"`
}

type bodyFreeEvidencePhaseA struct {
	ManagedBytes        int64   `json:"managed_bytes"`
	StoredBodyBytes     int64   `json:"stored_body_bytes"`
	Events              int64   `json:"events"`
	ProjectionRows      int64   `json:"projection_rows"`
	MissingBodyMetadata int64   `json:"missing_body_metadata"`
	ProjectionOnly      bool    `json:"projection_only"`
	ReturnedBodyBytes   int64   `json:"returned_body_bytes"`
	Runs                int     `json:"runs"`
	P95MS               float64 `json:"p95_ms"`
	TargetP95MS         float64 `json:"target_p95_ms"`
	Passed              bool    `json:"passed"`
}

type bodyFreeEvidencePhaseB struct {
	SourceManagedBytes          int64   `json:"source_managed_bytes"`
	ScratchBytesAfterCheckpoint int64   `json:"scratch_bytes_after_checkpoint"`
	Events                      int64   `json:"events"`
	MigrationMS                 float64 `json:"migration_ms"`
	ResumeBackfillMS            float64 `json:"resume_backfill_ms"`
	Migrations31Through34       bool    `json:"migrations_31_34_applied"`
	ProjectionRows              int64   `json:"projection_rows"`
	IntegrityOK                 bool    `json:"integrity_ok"`
	ForeignKeyViolations        int64   `json:"foreign_key_violations"`
	SourceUnchanged             bool    `json:"source_unchanged"`
	InitialFTSDocuments         int64   `json:"initial_fts_documents"`
	InitialFTSComplete          bool    `json:"initial_fts_complete"`
	FinalFTSDocuments           int64   `json:"final_fts_documents"`
	FinalFTSComplete            bool    `json:"final_fts_complete"`
}

type bodyFreeEvidenceProbe struct {
	Operation         string  `json:"operation"`
	Projection        string  `json:"projection"`
	FTSPhase          string  `json:"fts_phase"`
	Runs              int     `json:"runs"`
	P95MS             float64 `json:"p95_ms"`
	ReturnedItems     int     `json:"returned_items"`
	ReturnedBodyBytes int     `json:"returned_body_bytes"`
}

type bodyFreeEvidencePhaseD struct {
	MaxItems                      int  `json:"max_items"`
	MaxAggregateBodyBytes         int  `json:"max_aggregate_body_bytes"`
	ObservedMaxItems              int  `json:"observed_max_items"`
	ObservedMaxAggregateBodyBytes int  `json:"observed_max_aggregate_body_bytes"`
	Pages                         int  `json:"pages"`
	TotalItems                    int  `json:"total_items"`
	MultibyteObserved             bool `json:"multibyte_observed"`
	BodyBlocksObserved            bool `json:"body_blocks_observed"`
	TruncationMetadataObserved    bool `json:"truncation_metadata_observed"`
	ContinuationNoDuplicateOrSkip bool `json:"continuation_no_duplicate_or_skip"`
}

type bodyFreeEvidencePhaseE struct {
	HostCount      int  `json:"host_count"`
	ManifestParity bool `json:"manifest_parity"`
}

type bodyFreeEvidencePrivacy struct {
	MetricsOnly    bool `json:"metrics_only"`
	ScratchPrivate bool `json:"scratch_private"`
	ScratchCleaned bool `json:"scratch_cleaned"`
}

type bodyFreeEvidencePhaseBC struct {
	PhaseB bodyFreeEvidencePhaseB  `json:"phase_b"`
	PhaseC []bodyFreeEvidenceProbe `json:"phase_c"`
}

type releaseEvidencePhase string

const (
	releaseEvidencePhaseA  releaseEvidencePhase = "phase_a"
	releaseEvidencePhaseBC releaseEvidencePhase = "phase_bc"
	releaseEvidencePhaseD  releaseEvidencePhase = "phase_d"
)

type releaseEvidenceDependencies struct {
	availableScratchBytes func(string) (uint64, error)
	runPhase              func(context.Context, string, string, releaseEvidencePhase) ([]byte, error)
	verifyHosts           func(string) error
	makeTemp              func(string, string) (string, error)
	removeAll             func(string) error
	stat                  func(string) (os.FileInfo, error)
}

func defaultReleaseEvidenceDependencies() releaseEvidenceDependencies {
	return releaseEvidenceDependencies{
		availableScratchBytes: availableScratchBytes,
		runPhase:              runReleaseEvidencePhase,
		verifyHosts: func(root string) error {
			return verifyIntegrations(root, false)
		},
		makeTemp:  os.MkdirTemp,
		removeAll: os.RemoveAll,
		stat:      os.Stat,
	}
}

func addReleaseEvidenceCommands(release *cobra.Command) {
	var runOutput string
	var scratchRoot string
	run := &cobra.Command{
		Use:   "run-v0.33.0-evidence",
		Short: "Run private multi-GiB release probes and write metrics-only evidence",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := findRepoRoot()
			if err != nil {
				return err
			}
			evidence := collectV0330BodyFreeEvidence(
				cmd.Context(),
				root,
				scratchRoot,
				defaultReleaseEvidenceDependencies(),
			)
			if err := writeBodyFreeEvidence(cmd.OutOrStdout(), runOutput, evidence); err != nil {
				return err
			}
			if evidence.Status != "pass" {
				return xerrors.Errorf("v0.33.0 release evidence is blocked: %s", evidence.BlockReason)
			}
			return nil
		},
	}
	run.Flags().StringVar(&runOutput, "output", "", "write the metrics-only JSON artifact to this file (default: stdout)")
	run.Flags().StringVar(&scratchRoot, "scratch-root", "", "private temporary parent directory (default: operating-system temp directory)")

	var verifyInput string
	var allowBlocked bool
	verify := &cobra.Command{
		Use:   "verify-body-free-evidence",
		Short: "Validate a metrics-only release evidence artifact",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			file, err := os.Open(verifyInput) // #nosec G304 -- explicit maintainer-supplied evidence path
			if err != nil {
				return xerrors.Errorf("failed to open evidence input")
			}
			defer func() { _ = file.Close() }()
			evidence, err := decodeBodyFreeEvidence(file)
			if err != nil {
				return err
			}
			if evidence.Status != "pass" && !allowBlocked {
				return xerrors.Errorf("release evidence status is %s", evidence.Status)
			}
			message := "ok: body-free release evidence is complete"
			if evidence.Status == "blocked" {
				message = "ok: blocked body-free release evidence is structurally valid"
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
				return xerrors.Errorf("failed to write evidence verification result: %w", err)
			}
			return nil
		},
	}
	verify.Flags().StringVar(&verifyInput, "input", "", "metrics-only evidence JSON")
	verify.Flags().BoolVar(&allowBlocked, "allow-blocked", false, "validate a sanitized blocked artifact without declaring release readiness")
	if err := verify.MarkFlagRequired("input"); err != nil {
		panic(err) // programming error: the flag was just registered
	}

	release.AddCommand(run)
	release.AddCommand(verify)
}

func collectV0330BodyFreeEvidence(
	ctx context.Context,
	root string,
	scratchParent string,
	deps releaseEvidenceDependencies,
) BodyFreeEvidence {
	evidence := BodyFreeEvidence{
		Schema: bodyFreeEvidenceSchema,
		Status: "blocked",
		Host: bodyFreeEvidenceHost{
			GOOS:      runtime.GOOS,
			GOARCH:    runtime.GOARCH,
			GoVersion: runtime.Version(),
		},
		Preflight: bodyFreeEvidencePreflight{
			RequiredScratchBytes: v0330EvidenceRequiredScratchBytes,
		},
		Privacy: bodyFreeEvidencePrivacy{
			MetricsOnly:    true,
			ScratchCleaned: true,
		},
	}
	if scratchParent == "" {
		scratchParent = os.TempDir()
	}

	available, err := deps.availableScratchBytes(scratchParent)
	evidence.Preflight.AvailableScratchBytes = available
	if err != nil {
		evidence.Preflight.Reason = "filesystem_preflight_unavailable"
		evidence.BlockReason = evidence.Preflight.Reason
		return evidence
	}
	if available < v0330EvidenceRequiredScratchBytes {
		evidence.Preflight.Reason = "insufficient_disk"
		evidence.BlockReason = evidence.Preflight.Reason
		return evidence
	}
	evidence.Preflight.Capable = true

	scratch, err := deps.makeTemp(scratchParent, "traceary-v0330-evidence-")
	if err != nil {
		evidence.BlockReason = "scratch_create_failed"
		return evidence
	}
	if err := os.Chmod(scratch, 0o700); err != nil {
		evidence.BlockReason = "scratch_privacy_failed"
	} else if info, statErr := deps.stat(scratch); statErr != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		evidence.BlockReason = "scratch_privacy_failed"
	} else {
		evidence.Privacy.ScratchPrivate = true
	}

	if evidence.BlockReason == "" {
		if err := deps.verifyHosts(root); err != nil {
			evidence.BlockReason = "host_parity_failed"
		} else {
			evidence.PhaseE = &bodyFreeEvidencePhaseE{HostCount: 6, ManifestParity: true}
		}
	}

	phaseCtx, cancel := context.WithTimeout(ctx, v0330EvidenceRunTimeout)
	defer cancel()
	phasesAllowed := evidence.Privacy.ScratchPrivate && evidence.PhaseE != nil
	setBlockReason := func(reason string) {
		if evidence.BlockReason == "" {
			evidence.BlockReason = reason
		}
	}
	if phasesAllowed {
		output, phaseErr := deps.runPhase(phaseCtx, root, scratch, releaseEvidencePhaseA)
		parseErr := extractEvidenceMarker(output, bodyFreeEvidencePhaseAMarker, &evidence.PhaseA)
		if phaseErr != nil {
			setBlockReason("phase_a_failed")
		} else if parseErr != nil {
			evidence.PhaseA = nil
			setBlockReason("phase_a_evidence_missing")
		}
	}
	if phasesAllowed {
		output, phaseErr := deps.runPhase(phaseCtx, root, scratch, releaseEvidencePhaseBC)
		var combined bodyFreeEvidencePhaseBC
		parseErr := extractEvidenceMarker(output, bodyFreeEvidencePhaseBCMarker, &combined)
		if parseErr == nil {
			evidence.PhaseB = &combined.PhaseB
			evidence.PhaseC = combined.PhaseC
		}
		if phaseErr != nil {
			setBlockReason("phase_bc_failed")
		} else if parseErr != nil {
			setBlockReason("phase_bc_evidence_missing")
		}
	}
	if phasesAllowed {
		output, phaseErr := deps.runPhase(phaseCtx, root, scratch, releaseEvidencePhaseD)
		parseErr := extractEvidenceMarker(output, bodyFreeEvidencePhaseDMarker, &evidence.PhaseD)
		if phaseErr != nil {
			setBlockReason("phase_d_failed")
		} else if parseErr != nil {
			evidence.PhaseD = nil
			setBlockReason("phase_d_evidence_missing")
		}
	}

	removeErr := deps.removeAll(scratch)
	_, statErr := deps.stat(scratch)
	evidence.Privacy.ScratchCleaned = removeErr == nil && errors.Is(statErr, os.ErrNotExist)
	if !evidence.Privacy.ScratchCleaned {
		evidence.BlockReason = "scratch_cleanup_failed"
	}

	if evidence.BlockReason == "" {
		evidence.Status = "pass"
	}
	if err := validateBodyFreeEvidence(evidence); err != nil {
		evidence.Status = "blocked"
		evidence.BlockReason = "evidence_validation_failed"
		evidence.PhaseA = nil
		evidence.PhaseB = nil
		evidence.PhaseC = nil
		evidence.PhaseD = nil
		evidence.PhaseE = nil
	}
	return evidence
}

func runReleaseEvidencePhase(
	ctx context.Context,
	root string,
	scratch string,
	phase releaseEvidencePhase,
) ([]byte, error) {
	var args []string
	extraEnv := []string{"TMPDIR=" + scratch}
	switch phase {
	case releaseEvidencePhaseA:
		args = []string{
			"test", "-v", "./infrastructure/sqlite", "-run", "^$",
			"-bench", "^BenchmarkMetadataDirectRangeMultiGiB$", "-benchtime=1x",
			"-count=1", "-timeout=45m",
		}
		extraEnv = append(extraEnv, "TRACEARY_RUN_MULTI_GIB_BENCHMARK=1")
	case releaseEvidencePhaseBC:
		args = []string{
			"test", "-v", "./infrastructure/sqlite", "-run", "^$",
			"-bench", "^BenchmarkV0330CopiedStoreReleaseEvidence$", "-benchtime=1x",
			"-count=1", "-timeout=45m",
		}
		extraEnv = append(extraEnv, "TRACEARY_RUN_V0330_RELEASE_EVIDENCE=1")
	case releaseEvidencePhaseD:
		args = []string{
			"test", "-v", "./presentation/mcpserver",
			"-run", "^TestV0330AggregateReleaseEvidence$", "-count=1", "-timeout=5m",
		}
	default:
		return nil, xerrors.Errorf("unsupported release evidence phase")
	}

	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	command.Env = append(os.Environ(), extraEnv...)
	output := &cappedEvidenceBuffer{remaining: v0330EvidenceCommandOutputBytes}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	return output.Bytes(), err
}

type cappedEvidenceBuffer struct {
	bytes.Buffer
	remaining int
}

func (b *cappedEvidenceBuffer) Write(data []byte) (int, error) {
	original := len(data)
	if b.remaining > 0 {
		write := data
		if len(write) > b.remaining {
			write = write[:b.remaining]
		}
		_, _ = b.Buffer.Write(write)
		b.remaining -= len(write)
	}
	return original, nil
}

func extractEvidenceMarker(data []byte, marker string, target any) error {
	index := bytes.LastIndex(data, []byte(marker))
	if index < 0 {
		return xerrors.Errorf("release evidence marker is missing")
	}
	value := data[index+len(marker):]
	if newline := bytes.IndexByte(value, '\n'); newline >= 0 {
		value = value[:newline]
	}
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return xerrors.Errorf("release evidence marker is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return xerrors.Errorf("release evidence marker is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return xerrors.Errorf("release evidence marker has trailing data")
	}
	return nil
}

func writeBodyFreeEvidence(out io.Writer, outputPath string, evidence BodyFreeEvidence) error {
	if err := validateBodyFreeEvidence(evidence); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return xerrors.Errorf("failed to encode body-free release evidence: %w", err)
	}
	encoded = append(encoded, '\n')
	if outputPath == "" {
		if _, err := out.Write(encoded); err != nil {
			return xerrors.Errorf("failed to write body-free release evidence: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(outputPath, encoded, 0o600); err != nil {
		return xerrors.Errorf("failed to write body-free release evidence")
	}
	return nil
}

func decodeBodyFreeEvidence(reader io.Reader) (BodyFreeEvidence, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil {
		return BodyFreeEvidence{}, xerrors.Errorf("failed to read body-free release evidence")
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return BodyFreeEvidence{}, xerrors.Errorf("body-free release evidence is invalid JSON")
	}
	if err := rejectSensitiveEvidenceKeys(generic); err != nil {
		return BodyFreeEvidence{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var evidence BodyFreeEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return BodyFreeEvidence{}, xerrors.Errorf("body-free release evidence has unknown or invalid fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BodyFreeEvidence{}, xerrors.Errorf("body-free release evidence has trailing data")
	}
	if err := validateBodyFreeEvidence(evidence); err != nil {
		return BodyFreeEvidence{}, err
	}
	return evidence, nil
}

func rejectSensitiveEvidenceKeys(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			switch key {
			case "body", "raw_body", "prompt", "response", "event_id", "session_id",
				"workspace", "path", "cursor", "continuation", "ids":
				return xerrors.Errorf("body-free release evidence contains a forbidden field")
			}
			if err := rejectSensitiveEvidenceKeys(nested); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := rejectSensitiveEvidenceKeys(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateBodyFreeEvidence(evidence BodyFreeEvidence) error {
	if evidence.Schema != bodyFreeEvidenceSchema {
		return xerrors.Errorf("body-free release evidence schema is invalid")
	}
	if evidence.Status != "pass" && evidence.Status != "blocked" {
		return xerrors.Errorf("body-free release evidence status is invalid")
	}
	if !validBodyFreeEvidenceGOOS(evidence.Host.GOOS) ||
		!validBodyFreeEvidenceGOARCH(evidence.Host.GOARCH) ||
		!bodyFreeEvidenceGoVersionRe.MatchString(evidence.Host.GoVersion) {
		return xerrors.Errorf("body-free release evidence host value is invalid")
	}
	if evidence.Preflight.RequiredScratchBytes != v0330EvidenceRequiredScratchBytes {
		return xerrors.Errorf("body-free release evidence scratch requirement is invalid")
	}
	if evidence.Preflight.Reason != "" &&
		evidence.Preflight.Reason != "filesystem_preflight_unavailable" &&
		evidence.Preflight.Reason != "insufficient_disk" {
		return xerrors.Errorf("body-free release evidence preflight reason is invalid")
	}
	if !evidence.Privacy.MetricsOnly || !evidence.Privacy.ScratchCleaned {
		return xerrors.Errorf("body-free release evidence privacy contract is incomplete")
	}

	if evidence.PhaseA != nil {
		if err := validateBodyFreeEvidencePhaseA(*evidence.PhaseA); err != nil {
			return err
		}
	}
	if evidence.PhaseB != nil {
		if err := validateBodyFreeEvidencePhaseB(*evidence.PhaseB); err != nil {
			return err
		}
	}
	if len(evidence.PhaseC) > 0 {
		if err := validateBodyFreeEvidenceProbes(evidence.PhaseC); err != nil {
			return err
		}
	}
	if evidence.PhaseD != nil {
		if err := validateBodyFreeEvidencePhaseD(*evidence.PhaseD); err != nil {
			return err
		}
	}
	if evidence.PhaseE != nil {
		if err := validateBodyFreeEvidencePhaseE(*evidence.PhaseE); err != nil {
			return err
		}
	}

	if evidence.Status == "blocked" {
		allowed := map[string]bool{
			"filesystem_preflight_unavailable": true,
			"insufficient_disk":                true,
			"scratch_create_failed":            true,
			"scratch_privacy_failed":           true,
			"host_parity_failed":               true,
			"phase_a_failed":                   true,
			"phase_a_evidence_missing":         true,
			"phase_bc_failed":                  true,
			"phase_bc_evidence_missing":        true,
			"phase_d_failed":                   true,
			"phase_d_evidence_missing":         true,
			"scratch_cleanup_failed":           true,
			"evidence_validation_failed":       true,
		}
		if !allowed[evidence.BlockReason] {
			return xerrors.Errorf("body-free release evidence block reason is invalid")
		}
		if evidence.Preflight.Capable && evidence.BlockReason == "insufficient_disk" {
			return xerrors.Errorf("body-free release evidence preflight state is inconsistent")
		}
		if evidence.Preflight.Reason != "" && evidence.Preflight.Reason != evidence.BlockReason {
			return xerrors.Errorf("body-free release evidence preflight reason is inconsistent")
		}
		if evidence.Preflight.Reason == "" &&
			(evidence.BlockReason == "filesystem_preflight_unavailable" ||
				evidence.BlockReason == "insufficient_disk") {
			return xerrors.Errorf("body-free release evidence preflight reason is missing")
		}
		return nil
	}

	if evidence.BlockReason != "" || !evidence.Preflight.Capable ||
		evidence.Preflight.AvailableScratchBytes < evidence.Preflight.RequiredScratchBytes ||
		evidence.Preflight.Reason != "" || !evidence.Privacy.ScratchPrivate {
		return xerrors.Errorf("body-free release evidence pass preconditions are incomplete")
	}
	if evidence.PhaseA == nil || evidence.PhaseB == nil || evidence.PhaseD == nil || evidence.PhaseE == nil {
		return xerrors.Errorf("body-free release evidence is missing a phase")
	}
	if !evidence.PhaseA.Passed {
		return xerrors.Errorf("body-free release evidence phase A failed")
	}
	if len(evidence.PhaseC) == 0 {
		return xerrors.Errorf("body-free release evidence is missing phase C")
	}
	return nil
}

func validBodyFreeEvidenceGOOS(value string) bool {
	switch value {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos",
		"ios", "js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1",
		"windows":
		return true
	default:
		return false
	}
}

func validBodyFreeEvidenceGOARCH(value string) bool {
	switch value {
	case "386", "amd64", "arm", "arm64", "loong64", "mips", "mips64",
		"mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm":
		return true
	default:
		return false
	}
}

func validateBodyFreeEvidencePhaseA(phaseA bodyFreeEvidencePhaseA) error {
	if phaseA.ManagedBytes < 2<<30 || phaseA.StoredBodyBytes < 2<<30 ||
		phaseA.Events != 8 || phaseA.ProjectionRows != phaseA.Events ||
		phaseA.MissingBodyMetadata != 0 || !phaseA.ProjectionOnly ||
		phaseA.ReturnedBodyBytes != 0 ||
		phaseA.Runs != 25 || phaseA.TargetP95MS != 250 || phaseA.P95MS < 0 ||
		phaseA.Passed != (phaseA.P95MS < phaseA.TargetP95MS) {
		return xerrors.Errorf("body-free release evidence phase A is invalid")
	}
	return nil
}

func validateBodyFreeEvidencePhaseB(phaseB bodyFreeEvidencePhaseB) error {
	if phaseB.SourceManagedBytes < 2<<30 ||
		phaseB.ScratchBytesAfterCheckpoint < 2*phaseB.SourceManagedBytes ||
		phaseB.Events != 130 || phaseB.MigrationMS < 0 || phaseB.ResumeBackfillMS < 0 ||
		!phaseB.Migrations31Through34 || phaseB.ProjectionRows != phaseB.Events ||
		!phaseB.IntegrityOK || phaseB.ForeignKeyViolations != 0 ||
		!phaseB.SourceUnchanged || phaseB.InitialFTSDocuments != 128 || phaseB.InitialFTSComplete ||
		phaseB.FinalFTSDocuments != phaseB.Events || !phaseB.FinalFTSComplete {
		return xerrors.Errorf("body-free release evidence phase B failed")
	}
	return nil
}

func validateBodyFreeEvidencePhaseD(phaseD bodyFreeEvidencePhaseD) error {
	if phaseD.MaxItems != 100 || phaseD.MaxAggregateBodyBytes != 64*1024 ||
		phaseD.ObservedMaxItems <= 0 || phaseD.ObservedMaxItems > phaseD.MaxItems ||
		phaseD.ObservedMaxAggregateBodyBytes <= 0 ||
		phaseD.ObservedMaxAggregateBodyBytes > phaseD.MaxAggregateBodyBytes ||
		phaseD.Pages < 2 || phaseD.TotalItems != 100 ||
		!phaseD.MultibyteObserved || !phaseD.BodyBlocksObserved ||
		!phaseD.TruncationMetadataObserved ||
		!phaseD.ContinuationNoDuplicateOrSkip {
		return xerrors.Errorf("body-free release evidence phase D failed")
	}
	return nil
}

func validateBodyFreeEvidencePhaseE(phaseE bodyFreeEvidencePhaseE) error {
	if phaseE.HostCount != 6 || !phaseE.ManifestParity {
		return xerrors.Errorf("body-free release evidence phase E failed")
	}
	return nil
}

func validateBodyFreeEvidenceProbes(probes []bodyFreeEvidenceProbe) error {
	expected := map[string]bool{
		"list|metadata|not_applicable":    true,
		"list|bounded|not_applicable":     true,
		"context|metadata|not_applicable": true,
		"context|bounded|not_applicable":  true,
		"search|metadata|incomplete":      true,
		"search|bounded|incomplete":       true,
		"search|metadata|complete":        true,
		"search|bounded|complete":         true,
	}
	seen := make(map[string]bool, len(probes))
	for _, probe := range probes {
		key := strings.Join([]string{probe.Operation, probe.Projection, probe.FTSPhase}, "|")
		if !expected[key] || seen[key] {
			return xerrors.Errorf("body-free release evidence phase C has an unexpected probe")
		}
		seen[key] = true
		if probe.Runs != 25 || probe.P95MS < 0 ||
			probe.ReturnedItems < 0 || probe.ReturnedItems > 100 ||
			probe.ReturnedBodyBytes < 0 {
			return xerrors.Errorf("body-free release evidence phase C probe is invalid")
		}
		if probe.Projection == "metadata" && probe.ReturnedBodyBytes != 0 {
			return xerrors.Errorf("body-free release evidence metadata probe returned body bytes")
		}
	}
	if len(seen) != len(expected) {
		keys := make([]string, 0, len(expected)-len(seen))
		for key := range expected {
			if !seen[key] {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		return xerrors.Errorf("body-free release evidence phase C is incomplete")
	}
	return nil
}
