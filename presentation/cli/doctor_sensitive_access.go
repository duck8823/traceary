package cli

import (
	"context"
	"fmt"

	"github.com/duck8823/traceary/application/sensitivepath"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const doctorSensitiveAccessScanLimit = 500

// inspectSensitiveAccessAuditCoverage explains whether recent command audits
// yield trustworthy sensitive-path claims. It never treats redaction or host
// coverage as the same signal as a sensitive match.
func (c *RootCLI) inspectSensitiveAccessAuditCoverage(ctx context.Context) doctorCheck {
	const checkName = "sensitive-access-audit"
	if c.event == nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusSkip,
			Message: localizef("event usecase is not configured", "event usecase が設定されていません"),
		}
	}

	events, err := c.event.List(ctx, apptypes.NewEventListCriteriaBuilder(doctorSensitiveAccessScanLimit).
		Kind(types.EventKindCommandExecuted).
		Build())
	if err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to list recent command audits: %v", "recent command audit の取得に失敗しました: %v", err),
		}
	}

	var matched, intentOnly, partial, structured int
	for _, event := range events {
		if event == nil {
			continue
		}
		cls := sensitivepath.ClassifyCommandBody(event.Body(), nil)
		if !cls.Matched {
			continue
		}
		matched++
		if cls.IntentOnly {
			intentOnly++
		}
		if cls.Coverage == sensitivepath.CoveragePartial || cls.Coverage == sensitivepath.CoverageUnobservable {
			partial++
		}
		if cls.Evidence == sensitivepath.EvidenceStructuredFileTool {
			structured++
		}
	}

	details := fmt.Sprintf("scanned=%d matched=%d intent_only=%d structured=%d weak_coverage=%d",
		len(events), matched, intentOnly, structured, partial)
	if matched == 0 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent command audit(s); no sensitive-path matches (sensitive, redaction, and host coverage remain separate claims) (%s)",
				"%d 件の recent command audit を検査しました。sensitive-path 一致はありません（sensitive / redaction / host coverage は別 claim です）(%s)",
				len(events), details,
			),
			Hint: Localize(
				"use `traceary list --sensitive --kind audit` after sessions that touch .env / SSH / cloud credential paths",
				".env / SSH / cloud credential に触れた session の後は `traceary list --sensitive --kind audit` で確認してください",
			),
		}
	}

	status := doctorStatusPass
	if intentOnly > 0 || partial > 0 {
		status = doctorStatusWarn
	}
	return doctorCheck{
		Name:   checkName,
		Status: status,
		Message: localizef(
			"scanned %d recent command audit(s); sensitive-path matches present (%s). Intent-only rows are not proof of file open",
			"%d 件の recent command audit を検査しました。sensitive-path 一致があります (%s)。intent-only 行は file open の証明ではありません",
			len(events), details,
		),
		Hint: Localize(
			"sensitive-path detection is passive flagging only; redaction and capture coverage are separate claims. Prefer structured file-tool evidence when available.",
			"sensitive-path 検出は受動的な flag のみです。redaction と capture coverage は別 claim です。可能な場合は structured file-tool 証拠を優先してください。",
		),
	}
}
