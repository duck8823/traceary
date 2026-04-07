package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) newSessionCommand() *cobra.Command {
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "セッション境界を記録する",
	}
	sessionCmd.AddCommand(c.newSessionStartCommand())
	sessionCmd.AddCommand(c.newSessionEndCommand())
	sessionCmd.AddCommand(c.newSessionLatestCommand())
	sessionCmd.AddCommand(c.newSessionActiveCommand())

	return sessionCmd
}

func (c *RootCLI) newSessionLatestCommand() *cobra.Command {
	var (
		dbPath string
		client string
		agent  string
		repo   string
	)

	latestCmd := &cobra.Command{
		Use:   "latest",
		Short: "直近のセッション ID を表示する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionLatest(cmd.Context(), cmd.OutOrStdout(), sessionLatestCommandInput{
				dbPath: dbPath,
				client: client,
				agent:  agent,
				repo:   repo,
			})
		},
	}
	latestCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	latestCmd.Flags().StringVar(&client, "client", "", "記録経路で絞り込む")
	latestCmd.Flags().StringVar(&agent, "agent", "", "作業主体で絞り込む")
	latestCmd.Flags().StringVar(&repo, "repo", "", "補助的なコンテキスト識別子で絞り込む")

	return latestCmd
}

func (c *RootCLI) newSessionStartCommand() *cobra.Command {
	var (
		dbPath    string
		client    string
		agent     string
		sessionID string
		repo      string
	)

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "セッション開始を記録する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionBoundary(cmd.Context(), cmd.OutOrStdout(), sessionBoundaryCommandInput{
				dbPath:    dbPath,
				client:    client,
				agent:     agent,
				sessionID: strings.TrimSpace(sessionID),
				repo:      repo,
				kind:      types.EventKindSessionStarted,
			})
		},
	}
	startCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	startCmd.Flags().StringVar(&client, "client", "", "記録経路 (env: TRACEARY_CLIENT)")
	startCmd.Flags().StringVar(&agent, "agent", "", "作業主体 (env: TRACEARY_AGENT)")
	startCmd.Flags().StringVar(&sessionID, "session-id", "", "開始するセッション ID")
	startCmd.Flags().StringVar(&repo, "repo", "", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)")

	return startCmd
}

func (c *RootCLI) newSessionEndCommand() *cobra.Command {
	var (
		dbPath    string
		client    string
		agent     string
		sessionID string
		repo      string
	)

	endCmd := &cobra.Command{
		Use:   "end",
		Short: "セッション終了を記録する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionBoundary(cmd.Context(), cmd.OutOrStdout(), sessionBoundaryCommandInput{
				dbPath:    dbPath,
				client:    client,
				agent:     agent,
				sessionID: resolveOptionalValue(sessionID, "TRACEARY_SESSION_ID", ""),
				repo:      repo,
				kind:      types.EventKindSessionEnded,
			})
		},
	}
	endCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	endCmd.Flags().StringVar(&client, "client", "", "記録経路 (env: TRACEARY_CLIENT)")
	endCmd.Flags().StringVar(&agent, "agent", "", "作業主体 (env: TRACEARY_AGENT)")
	endCmd.Flags().StringVar(&sessionID, "session-id", "", "終了するセッション ID (env: TRACEARY_SESSION_ID)")
	endCmd.Flags().StringVar(&repo, "repo", "", "補助的なコンテキスト識別子 (env: TRACEARY_REPO)")

	return endCmd
}

type sessionBoundaryCommandInput struct {
	dbPath    string
	client    string
	agent     string
	sessionID string
	repo      string
	kind      types.EventKind
}

type sessionLatestCommandInput struct {
	dbPath     string
	client     string
	agent      string
	repo       string
	activeOnly bool
}

type sessionLookupNotFoundCLIError struct {
	err error
}

func (e *sessionLookupNotFoundCLIError) Error() string { return e.err.Error() }

func (e *sessionLookupNotFoundCLIError) Unwrap() error { return e.err }

func (c *RootCLI) newSessionActiveCommand() *cobra.Command {
	var (
		dbPath string
		client string
		agent  string
		repo   string
	)

	activeCmd := &cobra.Command{
		Use:   "active",
		Short: "現在アクティブなセッション ID を表示する",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runSessionLatest(cmd.Context(), cmd.OutOrStdout(), sessionLatestCommandInput{
				dbPath:     dbPath,
				client:     client,
				agent:      agent,
				repo:       repo,
				activeOnly: true,
			})
		},
	}
	activeCmd.Flags().StringVar(&dbPath, "db-path", "", "SQLite DB パス")
	activeCmd.Flags().StringVar(&client, "client", "", "記録経路で絞り込む")
	activeCmd.Flags().StringVar(&agent, "agent", "", "作業主体で絞り込む")
	activeCmd.Flags().StringVar(&repo, "repo", "", "補助的なコンテキスト識別子で絞り込む")

	return activeCmd
}

func (c *RootCLI) runSessionBoundary(
	ctx context.Context,
	output io.Writer,
	input sessionBoundaryCommandInput,
) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.recordSessionBoundaryUsecase == nil {
		return xerrors.Errorf("session 境界ユースケースが設定されていません")
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	event, err := c.recordSessionBoundaryUsecase.Run(ctx, usecase.RecordSessionBoundaryInput{
		DBPath:    resolvedPath,
		Client:    resolveOptionalValue(input.client, "TRACEARY_CLIENT", defaultClientValue),
		Agent:     resolveOptionalValue(input.agent, "TRACEARY_AGENT", defaultAgentValue),
		SessionID: input.sessionID,
		Repo:      resolveRepoValue(ctx, input.repo),
		Kind:      input.kind,
	})
	if err != nil {
		return xerrors.Errorf("session 境界の記録に失敗しました: %w", err)
	}

	if _, err := fmt.Fprintln(output, event.SessionID()); err != nil {
		return xerrors.Errorf("session ID の出力に失敗しました: %w", err)
	}

	return nil
}

func (c *RootCLI) runSessionLatest(
	ctx context.Context,
	output io.Writer,
	input sessionLatestCommandInput,
) error {
	if c.initializeStoreUsecase == nil {
		return xerrors.Errorf("ストア初期化ユースケースが設定されていません")
	}
	if c.findLatestSessionQueryService == nil {
		return xerrors.Errorf("直近セッションクエリサービスが設定されていません")
	}

	resolvedPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("DB パスの解決に失敗しました: %w", err)
	}
	if err := c.initializeStoreUsecase.Run(ctx, resolvedPath); err != nil {
		return xerrors.Errorf("ストアの初期化に失敗しました: %w", err)
	}

	event, err := c.findLatestSessionQueryService.Run(ctx, resolvedPath, queryservice.FindLatestSessionInput{
		Client:     resolveOptionalValue(input.client, "TRACEARY_CLIENT", ""),
		Agent:      resolveOptionalValue(input.agent, "TRACEARY_AGENT", ""),
		Repo:       resolveRepoValue(ctx, input.repo),
		ActiveOnly: input.activeOnly,
	})
	if err != nil {
		if queryservice.IsSessionLookupNotFound(err) {
			return wrapSessionLookupNotFoundError(err)
		}
		if input.activeOnly {
			return xerrors.Errorf("アクティブ session の取得に失敗しました: %w", err)
		}
		return xerrors.Errorf("直近セッションの取得に失敗しました: %w", err)
	}

	if _, err := fmt.Fprintln(output, event.SessionID()); err != nil {
		return xerrors.Errorf("session ID の出力に失敗しました: %w", err)
	}

	return nil
}

func wrapSessionLookupNotFoundError(err error) error {
	if err == nil {
		return nil
	}
	if wrappedErr, ok := err.(*sessionLookupNotFoundCLIError); ok {
		return wrappedErr
	}

	return &sessionLookupNotFoundCLIError{err: err}
}
