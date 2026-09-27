package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

type manualSessionResolution struct {
	sessionID string
	notice    string
}

func (c *RootCLI) resolveManualSessionID(
	ctx context.Context,
	explicitSessionID string,
	repo string,
) (*manualSessionResolution, error) {
	if trimmedSessionID := strings.TrimSpace(explicitSessionID); trimmedSessionID != "" {
		return &manualSessionResolution{sessionID: trimmedSessionID}, nil
	}

	trimmedRepo := strings.TrimSpace(repo)
	if trimmedRepo == "" || c.session == nil {
		slog.Debug("no workspace or query service, using default session", "workspace", trimmedRepo, "has_query_service", c.session != nil)
		return &manualSessionResolution{
			sessionID: defaultSessionIDValue,
			notice: Localize(
				"No workspace was detected; using default session ID",
				"workspace を検出できなかったため、既定の session ID を使います",
			),
		}, nil
	}

	criteria := apptypes.NewSessionLookupCriteriaBuilder().
		Workspace(types.Workspace(trimmedRepo)).
		Build()
	result, err := c.session.Latest(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf(
			"%s: %w",
			Localize("failed to resolve recorded session", "recorded session の解決に失敗しました"),
			err,
		)
	}
	if _, ok := result.Value(); !ok {
		slog.Debug("no recorded session found for repo, using default", "workspace", trimmedRepo)
		return &manualSessionResolution{
			sessionID: defaultSessionIDValue,
			notice: localizef(
				"No recorded session found for %s; using default session ID",
				"%s に対応する recorded session が見つからなかったため、既定の session ID を使います",
				trimmedRepo,
			),
		}, nil
	}

	event, _ := result.Value()

	return &manualSessionResolution{
		sessionID: event.SessionID().String(),
		notice: localizef(
			"Using recorded session: %s",
			"recorded session を利用します: %s",
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
