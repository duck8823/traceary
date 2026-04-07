package sqlite

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
)

var _ usecase.CommandAuditSaver = (*Datasource)(nil)

// SaveCommandAudit はイベントとコマンド監査情報を同一トランザクションで保存します。
func (d *Datasource) SaveCommandAudit(
	ctx context.Context,
	dbPath string,
	event *model.Event,
	commandAudit *model.CommandAudit,
) error {
	if event == nil {
		return xerrors.Errorf("イベントは nil にできません")
	}
	if commandAudit == nil {
		return xerrors.Errorf("コマンド監査情報は nil にできません")
	}

	db, err := d.openDB(ctx, dbPath)
	if err != nil {
		return xerrors.Errorf("コマンド監査保存用の DB オープンに失敗しました: %w", err)
	}
	defer func() { _ = db.Close() }()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("コマンド監査保存トランザクション開始に失敗しました: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO events(id, kind, client, agent, session_id, repo, body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID().String(),
		event.Kind().String(),
		event.Client(),
		event.Agent().String(),
		event.SessionID().String(),
		event.Repo(),
		event.Body(),
		formatTimestamp(event.CreatedAt()),
	); err != nil {
		return xerrors.Errorf("イベントの INSERT に失敗しました: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO command_audits(event_id, command_text, input_text, output_text, input_truncated, output_truncated)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		commandAudit.EventID().String(),
		commandAudit.Command(),
		commandAudit.Input(),
		commandAudit.Output(),
		commandAudit.InputTruncated(),
		commandAudit.OutputTruncated(),
	); err != nil {
		return xerrors.Errorf("コマンド監査情報の INSERT に失敗しました: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("コマンド監査保存トランザクションの commit に失敗しました: %w", err)
	}

	return nil
}
