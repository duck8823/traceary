package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// MemoryToolFileDatasource is the SQLite-backed implementation of the
// Anthropic memory-tool file repository.
type MemoryToolFileDatasource struct {
	db *Database
}

var _ model.MemoryToolFileRepository = (*MemoryToolFileDatasource)(nil)

// NewMemoryToolFileDatasource creates a new MemoryToolFileDatasource.
func NewMemoryToolFileDatasource(db *Database) *MemoryToolFileDatasource {
	return &MemoryToolFileDatasource{db: db}
}

// Save inserts or updates a memory-tool file.
func (d *MemoryToolFileDatasource) Save(ctx context.Context, file *model.MemoryToolFile) error {
	if file == nil {
		return xerrors.Errorf("memory tool file must not be nil")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for memory tool file save: %w", err)
	}
	defer closeSQLDB(db)

	const query = `
INSERT INTO memory_tool_files(path, content, created_at, updated_at, size_bytes)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    content = excluded.content,
    updated_at = excluded.updated_at,
    size_bytes = excluded.size_bytes;`
	if _, err := db.ExecContext(
		ctx,
		query,
		file.Path().String(),
		file.Content(),
		formatTimestamp(file.CreatedAt()),
		formatTimestamp(file.UpdatedAt()),
		file.SizeBytes(),
	); err != nil {
		return xerrors.Errorf("failed to save memory tool file: %w", err)
	}
	return nil
}

// FindByPath returns a memory-tool file by exact path.
func (d *MemoryToolFileDatasource) FindByPath(ctx context.Context, path types.MemoryToolPath) (types.Optional[*model.MemoryToolFile], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.MemoryToolFile](), xerrors.Errorf("failed to open DB for memory tool file lookup: %w", err)
	}
	defer closeSQLDB(db)

	const query = `
SELECT path, content, created_at, updated_at
FROM memory_tool_files
WHERE path = ?;`
	file, err := scanMemoryToolFile(db.QueryRowContext(ctx, query, path.String()))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.None[*model.MemoryToolFile](), nil
		}
		return types.None[*model.MemoryToolFile](), xerrors.Errorf("failed to scan memory tool file: %w", err)
	}
	return types.Some(file), nil
}

// List returns all memory-tool files ordered by path.
func (d *MemoryToolFileDatasource) List(ctx context.Context) ([]*model.MemoryToolFile, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for memory tool file listing: %w", err)
	}
	defer closeSQLDB(db)

	const query = `
SELECT path, content, created_at, updated_at
FROM memory_tool_files
ORDER BY path ASC;`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, xerrors.Errorf("failed to query memory tool files: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	files := make([]*model.MemoryToolFile, 0)
	for rows.Next() {
		file, err := scanMemoryToolFile(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to scan memory tool file: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate memory tool files: %w", err)
	}
	return files, nil
}

// DeletePathPrefix deletes an exact file or all files below a directory path.
func (d *MemoryToolFileDatasource) DeletePathPrefix(ctx context.Context, path types.MemoryToolPath) (int64, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for memory tool file delete: %w", err)
	}
	defer closeSQLDB(db)

	result, err := db.ExecContext(ctx, `DELETE FROM memory_tool_files WHERE path = ? OR path LIKE ?;`, path.String(), path.String()+"/%")
	if err != nil {
		return 0, xerrors.Errorf("failed to delete memory tool files: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, xerrors.Errorf("failed to read deleted memory tool file count: %w", err)
	}
	return count, nil
}

// RenamePathPrefix renames an exact file or all files below a directory path.
func (d *MemoryToolFileDatasource) RenamePathPrefix(
	ctx context.Context,
	oldPath types.MemoryToolPath,
	newPath types.MemoryToolPath,
	updatedAt time.Time,
) (int64, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for memory tool file rename: %w", err)
	}
	defer closeSQLDB(db)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, xerrors.Errorf("failed to begin memory tool file rename transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	rows, err := tx.QueryContext(ctx, `SELECT path FROM memory_tool_files WHERE path = ? OR path LIKE ? ORDER BY path ASC;`, oldPath.String(), oldPath.String()+"/%")
	if err != nil {
		return 0, xerrors.Errorf("failed to query renamed memory tool files: %w", err)
	}
	oldPaths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return 0, xerrors.Errorf("failed to scan renamed memory tool file path: %w", err)
		}
		oldPaths = append(oldPaths, path)
	}
	if err := rows.Close(); err != nil {
		return 0, xerrors.Errorf("failed to close renamed memory tool file rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, xerrors.Errorf("failed to iterate renamed memory tool file paths: %w", err)
	}

	for _, oldFilePath := range oldPaths {
		newFilePath := newPath.String() + strings.TrimPrefix(oldFilePath, oldPath.String())
		if _, err := tx.ExecContext(ctx, `UPDATE memory_tool_files SET path = ?, updated_at = ? WHERE path = ?;`, newFilePath, formatTimestamp(updatedAt), oldFilePath); err != nil {
			return 0, xerrors.Errorf("failed to rename memory tool file: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, xerrors.Errorf("failed to commit memory tool file rename: %w", err)
	}
	return int64(len(oldPaths)), nil
}

type memoryToolFileScanner interface {
	Scan(dest ...any) error
}

func scanMemoryToolFile(scanner memoryToolFileScanner) (*model.MemoryToolFile, error) {
	var (
		pathString      string
		content         []byte
		createdAtString string
		updatedAtString string
	)
	if err := scanner.Scan(&pathString, &content, &createdAtString, &updatedAtString); err != nil {
		return nil, xerrors.Errorf("failed to scan memory tool file row: %w", err)
	}
	path, err := types.NewMemoryToolPath(pathString)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore memory tool path: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtString)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse memory tool file created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtString)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse memory tool file updated_at: %w", err)
	}
	return model.MemoryToolFileOf(path, content, createdAt, updatedAt), nil
}

func closeSQLDB(db *sql.DB) {
	if err := db.Close(); err != nil {
		slog.Debug("failed to close resource", "error", err)
	}
}
