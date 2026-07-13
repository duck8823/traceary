package cli

import (
	"context"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
	appusecase "github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) inspectAntigravityEventCoverage(ctx context.Context, projectDir string, threshold float64) doctorCheck {
	const checkName = "antigravity-event-coverage"
	if c.event == nil {
		return doctorCheck{Name: checkName, Status: doctorStatusSkip, Message: localizef("event usecase is not configured", "event usecase が設定されていません")}
	}

	workspace := resolveDoctorEventCoverageWorkspace(ctx, projectDir)
	criteria := apptypes.NewEventListCriteriaBuilder(doctorEventCoverageScanLimit).Agent(types.Agent("antigravity"))
	if workspace.String() != "" {
		criteria.Workspace(workspace)
	}
	events, err := c.event.List(ctx, criteria.Build())
	if err != nil {
		return doctorCheck{Name: checkName, Status: doctorStatusFail, Message: localizef("failed to list recent Antigravity events: %v", "recent Antigravity event の取得に失敗しました: %v", err)}
	}

	inputs := make([]appusecase.EventCoverageInput, 0, len(events))
	for _, event := range events {
		if event != nil {
			inputs = append(inputs, appusecase.EventCoverageInput{SessionID: event.SessionID().String(), Kind: event.Kind()})
		}
	}
	return buildAntigravityEventCoverageCheck(len(events), appusecase.SummarizeSessionEventCoverage(inputs), threshold)
}

func buildAntigravityEventCoverageCheck(eventCount int, coverage appusecase.SessionEventCoverage, threshold float64) doctorCheck {
	const checkName = "antigravity-event-coverage"
	if coverage.Sessions < doctorEventCoverageMinSample {
		return doctorCheck{Name: checkName, Status: doctorStatusPass, Message: localizef(
			"scanned %d recent Antigravity event(s); only %d session(s) observed (minimum sample %d), so prompt/transcript coverage is not judged yet",
			"%d 件の recent Antigravity event を検査しました。観測できた session は %d 件だけです (minimum sample %d) のため、prompt/transcript coverage はまだ判定しません",
			eventCount, coverage.Sessions, doctorEventCoverageMinSample,
		)}
	}

	missing := coverage.PromptTranscriptMissing
	ratio := coverage.PromptTranscriptMissingRatio()
	details := fmt.Sprintf("sessions=%d complete=%d prompt_or_transcript_missing=%d with_prompt=%d with_transcript=%d with_command=%d ratio=%.2f threshold=%.2f",
		coverage.Sessions, coverage.Complete, missing, coverage.WithPrompt, coverage.WithTranscript, coverage.WithCommand, ratio, threshold)
	if ratio <= threshold {
		return doctorCheck{Name: checkName, Status: doctorStatusPass, Message: localizef(
			"scanned %d recent Antigravity event(s); prompt/transcript coverage is healthy (%s)",
			"%d 件の recent Antigravity event を検査しました。prompt/transcript coverage は健全です (%s)",
			eventCount, details,
		)}
	}

	return doctorCheck{
		Name: checkName, Status: doctorStatusWarn,
		Hint: Localize(
			"Antigravity exposes prompt and response text through transcriptPath rather than a direct prompt field. Inspect Stop hook delivery, transcript file access, and recent event IDs with `traceary list --agent antigravity`.",
			"Antigravity は prompt/response 本文を直接の prompt field ではなく transcriptPath 経由で提供します。Stop hook の配送、transcript file の読み取り、recent event ID を `traceary list --agent antigravity` で確認してください。",
		),
		Message: localizef(
			"scanned %d recent Antigravity event(s); prompt/transcript coverage is below the configured threshold (%s)",
			"%d 件の recent Antigravity event を検査しました。prompt/transcript coverage が設定 threshold を下回っています (%s)",
			eventCount, details,
		),
	}
}
