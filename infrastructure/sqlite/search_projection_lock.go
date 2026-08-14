package sqlite

import (
	"context"
	"database/sql"

	"golang.org/x/xerrors"
)

// projectionTx is one BEGIN IMMEDIATE session on a reserved connection.
// Rollback/Commit always close the connection.
type projectionTx struct {
	conn *sql.Conn
}

func beginImmediate(ctx context.Context, db *sql.DB) (*projectionTx, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, xerrors.Errorf("reserve projection write connection: %w", err)
	}
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		_ = conn.Close()
		return nil, xerrors.Errorf("begin immediate projection write: %w", err)
	}
	return &projectionTx{conn: conn}, nil
}

func (t *projectionTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	//nolint:wrapcheck // Matches database/sql.Tx so ApplyBatch can classify driver errors.
	return t.conn.ExecContext(ctx, query, args...)
}

func (t *projectionTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.conn.QueryRowContext(ctx, query, args...)
}

func (t *projectionTx) Commit() error {
	_, err := t.conn.ExecContext(context.Background(), `COMMIT`)
	closeErr := t.conn.Close()
	t.conn = nil
	if err != nil {
		return xerrors.Errorf("commit projection write: %w", err)
	}
	if closeErr != nil {
		return xerrors.Errorf("close projection write connection: %w", closeErr)
	}
	return nil
}

func (t *projectionTx) Rollback() error {
	if t == nil || t.conn == nil {
		return nil
	}
	_, err := t.conn.ExecContext(context.Background(), `ROLLBACK`)
	closeErr := t.conn.Close()
	t.conn = nil
	if err != nil {
		return xerrors.Errorf("rollback projection write: %w", err)
	}
	if closeErr != nil {
		return xerrors.Errorf("close projection write connection: %w", closeErr)
	}
	return nil
}

type projectionExec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
