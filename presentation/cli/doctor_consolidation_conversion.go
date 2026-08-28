package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
)

const (
	consolidationConversionWindow      = 7 * 24 * time.Hour
	consolidationConversionMinRequests = 5
	consolidationConversionWarnNumer   = 1
	consolidationConversionWarnDenom   = 4
)

func (c *RootCLI) inspectConsolidationConversion(ctx context.Context) doctorCheck {
	const name = "consolidation-conversion"
	if c.consolidationConversion == nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusSkip,
			Message: Localize("consolidation conversion query service is not configured", "consolidation conversion query service が設定されていません"),
		}
	}

	since := time.Now().UTC().Add(-consolidationConversionWindow)
	rows, err := c.consolidationConversion.ConversionSince(ctx, since)
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to read consolidation conversion: %v", "consolidation conversion の読み取りに失敗しました: %v", err),
		}
	}
	authorship, err := c.consolidationConversion.RefinementAuthorshipSince(ctx, since)
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("failed to read consolidation conversion: %v", "consolidation conversion の読み取りに失敗しました: %v", err),
		}
	}
	if len(rows) == 0 {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: Localize("no consolidation requests in the last 7 days", "過去 7 日の consolidation request はありません"),
		}
	}

	status := doctorStatusPass
	for _, row := range rows {
		if consolidationConversionWarns(row) {
			status = doctorStatusWarn
			break
		}
	}

	check := doctorCheck{
		Name:    name,
		Status:  status,
		Message: formatConsolidationConversionMessage(rows, authorship),
	}
	if status == doctorStatusWarn {
		check.Hint = Localize(
			"Agents are asked via the Stop hook; a low ratio means the reason text or the refine skill is not being followed (see #2275).",
			"Stop hook 経由でエージェントに依頼しています。比率が低い場合は reason 文面か refine skill が従われていません（#2275）。",
		)
	}
	return check
}

func consolidationConversionWarns(row queryservice.ConsolidationConversionRow) bool {
	if row.Requests < consolidationConversionMinRequests {
		return false
	}
	return row.SessionsRefined*consolidationConversionWarnDenom < row.SessionsRequested*consolidationConversionWarnNumer
}

func formatConsolidationConversionMessage(
	rows []queryservice.ConsolidationConversionRow,
	authorship []queryservice.RefinementAuthorshipRow,
) string {
	clauses := make([]string, 0, len(rows))
	for _, row := range rows {
		percent := 0
		if row.SessionsRequested > 0 {
			percent = row.SessionsRefined * 100 / row.SessionsRequested
		}
		clauses = append(clauses, localizef(
			"%s: %d requests / %d sessions asked / %d sessions refined (%d%%)",
			"%s: %d requests / %d sessions asked / %d sessions refined (%d%%)",
			row.Client, row.Requests, row.SessionsRequested, row.SessionsRefined, percent,
		))
	}
	authParts := make([]string, 0, len(authorship))
	for _, row := range authorship {
		producedBy := row.ProducedBy
		if producedBy == "" {
			producedBy = "none"
		}
		authParts = append(authParts, fmt.Sprintf("%s/%s=%d", row.Client, producedBy, row.Sessions))
	}
	authClause := localizef(
		"refinements by produced_by (7d, asked sessions): %s",
		"refinements by produced_by (7d, asked sessions): %s",
		strings.Join(authParts, ", "),
	)
	if len(authParts) == 0 {
		authClause = Localize(
			"refinements by produced_by (7d, asked sessions): none",
			"refinements by produced_by (7d, asked sessions): none",
		)
	}
	return strings.Join(append(clauses, authClause), "; ")
}
