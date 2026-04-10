package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/port"
)

type manualSessionResolution struct {
	sessionID string
	notice    string
}

func (c *RootCLI) resolveManualSessionID(
	ctx context.Context,
	dbPath string,
	explicitSessionID string,
	repo string,
) (*manualSessionResolution, error) {
	if trimmedSessionID := strings.TrimSpace(explicitSessionID); trimmedSessionID != "" {
		return &manualSessionResolution{sessionID: trimmedSessionID}, nil
	}

	trimmedRepo := strings.TrimSpace(repo)
	if trimmedRepo == "" || c.findLatestSessionQueryService == nil {
		slog.Debug("no work context or query service, using default session", "repo", trimmedRepo, "has_query_service", c.findLatestSessionQueryService != nil)
		return &manualSessionResolution{
			sessionID: defaultSessionIDValue,
			notice: Localize(
				"No work context was detected; using default session ID",
				"作業コンテキストを検出できなかったため、既定の session ID を使います",
			),
		}, nil
	}

	event, err := c.findLatestSessionQueryService.Run(ctx, dbPath, port.FindLatestSessionInput{
		Repo:       trimmedRepo,
		ActiveOnly: true,
	})
	if err != nil {
		if queryservice.IsSessionLookupNotFound(err) {
			slog.Debug("no active session found for repo, using default", "repo", trimmedRepo)
			return &manualSessionResolution{
				sessionID: defaultSessionIDValue,
				notice: localizef(
					"No active session found for %s; using default session ID",
					"%s に対応する active session が見つからなかったため、既定の session ID を使います",
					trimmedRepo,
				),
			}, nil
		}
		return nil, xerrors.Errorf(
			"%s: %w",
			Localize("failed to resolve active session", "active session の解決に失敗しました"),
			err,
		)
	}

	if isStaleSession(event, defaultActiveSessionStaleAfter) {
		slog.Debug("active session is stale, using default", "session_id", event.SessionID(), "created_at", event.CreatedAt())
		return &manualSessionResolution{
			sessionID: defaultSessionIDValue,
			notice: localizef(
				"Active session %s is stale; using default session ID",
				"active session %s は stale のため、既定の session ID を使います",
				event.SessionID(),
			),
		}, nil
	}

	return &manualSessionResolution{
		sessionID: event.SessionID().String(),
		notice: localizef(
			"Using active session: %s",
			"active session を利用します: %s",
			event.SessionID(),
		),
	}, nil
}

func writeManualSessionNotice(output io.Writer, notice string) error {
	trimmedNotice := strings.TrimSpace(notice)
	if trimmedNotice == "" {
		return nil
	}

	if _, err := fmt.Fprintln(output, trimmedNotice); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print session selection notice", "session 選択通知の出力に失敗しました"), err)
	}

	return nil
}

func isStaleSession(event *model.Event, staleAfter time.Duration) bool {
	if event == nil || staleAfter <= 0 {
		return false
	}

	return event.CreatedAt().Before(time.Now().Add(-staleAfter))
}
