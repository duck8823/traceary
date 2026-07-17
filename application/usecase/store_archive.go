package usecase

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

func (u *storeManagementUsecase) asArchiver() (application.StoreArchiver, error) {
	archiver, ok := u.storeManager.(application.StoreArchiver)
	if !ok {
		return nil, xerrors.Errorf("store archive is not supported by this store manager")
	}
	return archiver, nil
}

// CreateStoreArchive exports eligible cold rows and optionally deletes after verify.
func (u *storeManagementUsecase) CreateStoreArchive(
	ctx context.Context,
	params apptypes.StoreArchiveCreateParams,
) (apptypes.StoreArchiveResult, error) {
	if params.Before.IsZero() {
		return apptypes.StoreArchiveResult{}, xerrors.Errorf("before timestamp is required")
	}
	if _, ok := apptypes.GarbageCollectionTargetFrom(params.Target.String()); !ok {
		return apptypes.StoreArchiveResult{}, xerrors.Errorf("unsupported garbage-collection target: %s", params.Target)
	}
	if !params.DryRun && strings.TrimSpace(params.OutputPath) == "" {
		return apptypes.StoreArchiveResult{}, xerrors.Errorf("output path must not be empty")
	}
	archiver, err := u.asArchiver()
	if err != nil {
		return apptypes.StoreArchiveResult{}, err
	}

	tables, err := archiver.ListArchiveEligible(ctx, params.Before, params.Target)
	if err != nil {
		return apptypes.StoreArchiveResult{}, xerrors.Errorf("list archive-eligible rows: %w", err)
	}

	result := apptypes.StoreArchiveResult{
		DryRun: params.DryRun,
		Tables: make([]apptypes.StoreArchiveTableCount, 0, len(tables)),
	}
	codecTables := make([]storeArchiveTableData, 0, len(tables))
	for _, t := range tables {
		result.Tables = append(result.Tables, apptypes.StoreArchiveTableCount{
			Name: t.Name, RowCount: len(t.Rows),
		})
		result.TotalRows += len(t.Rows)
		codecTables = append(codecTables, storeArchiveTableData{
			Name: t.Name, PrimaryKey: t.PrimaryKey, Rows: t.Rows,
		})
	}

	if params.DryRun {
		return result, nil
	}

	plan := storeArchivePlan{
		Target:   params.Target.String(),
		KeepDays: params.KeepDays,
		Cutoff:   params.Before.UTC().Format(time.RFC3339Nano),
		DryRun:   false,
	}
	toolVersion := strings.TrimSpace(params.ToolVersion)
	if toolVersion == "" {
		toolVersion = "dev"
	}
	payload, _, err := buildStoreArchivePackage(codecTables, plan, toolVersion, params.SourceDBPath, params.Passphrase)
	if err != nil {
		return apptypes.StoreArchiveResult{}, xerrors.Errorf("build archive package: %w", err)
	}

	outPath := strings.TrimSpace(params.OutputPath)
	if err := writeFileAtomic(outPath, payload); err != nil {
		return apptypes.StoreArchiveResult{}, err
	}
	result.Path = outPath

	if err := u.VerifyStoreArchive(ctx, outPath, params.Passphrase); err != nil {
		return result, xerrors.Errorf("post-write verify failed (live DB unchanged): %w", err)
	}
	result.Verified = true

	if !params.DeleteAfterVerify {
		return result, nil
	}

	idsByTable := map[string][]string{}
	for _, t := range codecTables {
		ids := make([]string, 0, len(t.Rows))
		for _, row := range t.Rows {
			id := compositeID(row, t.PrimaryKey)
			if id == "" {
				return result, xerrors.Errorf("cannot delete archive row without primary key in table %s", t.Name)
			}
			ids = append(ids, id)
		}
		idsByTable[t.Name] = ids
	}
	deleted, err := archiver.DeleteArchiveRows(ctx, idsByTable)
	if err != nil {
		return result, xerrors.Errorf("delete archived rows after verify: %w", err)
	}
	result.DeletedCount = deleted
	result.DeletedAfterVerify = true
	return result, nil
}

// VerifyStoreArchive validates magic, optional decrypt, and manifest digests.
func (u *storeManagementUsecase) VerifyStoreArchive(ctx context.Context, path string, passphrase []byte) error {
	_ = ctx
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return xerrors.Errorf("read archive: %w", err)
	}
	manifest, files, err := openStoreArchivePackage(data, passphrase)
	if err != nil {
		return err
	}
	if err := verifyStoreArchiveContents(manifest, files); err != nil {
		return err
	}
	return nil
}

// RestoreStoreArchive imports package rows idempotently.
func (u *storeManagementUsecase) RestoreStoreArchive(
	ctx context.Context,
	path string,
	passphrase []byte,
	dryRun bool,
) (apptypes.StoreArchiveRestoreResult, error) {
	archiver, err := u.asArchiver()
	if err != nil {
		return apptypes.StoreArchiveRestoreResult{}, err
	}
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return apptypes.StoreArchiveRestoreResult{}, xerrors.Errorf("read archive: %w", err)
	}
	manifest, files, err := openStoreArchivePackage(data, passphrase)
	if err != nil {
		return apptypes.StoreArchiveRestoreResult{}, err
	}
	if err := verifyStoreArchiveContents(manifest, files); err != nil {
		return apptypes.StoreArchiveRestoreResult{}, xerrors.Errorf("verify before restore: %w", err)
	}
	parsed, err := parseStoreArchiveTables(manifest, files)
	if err != nil {
		return apptypes.StoreArchiveRestoreResult{}, err
	}
	var tables []application.ArchiveTableData
	total := 0
	for _, meta := range manifest.Tables {
		rows := parsed[meta.Name]
		tables = append(tables, application.ArchiveTableData{
			Name: meta.Name, PrimaryKey: meta.PrimaryKey, Rows: rows,
		})
		total += len(rows)
	}
	inserted, skipped, conflicts, err := archiver.RestoreArchiveRows(ctx, tables, dryRun)
	if err != nil {
		return apptypes.StoreArchiveRestoreResult{}, xerrors.Errorf("restore archive rows: %w", err)
	}
	return apptypes.StoreArchiveRestoreResult{
		DryRun:         dryRun,
		Inserted:       inserted,
		Skipped:        skipped,
		Conflicts:      conflicts,
		TotalInArchive: total,
	}, nil
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return xerrors.Errorf("create archive directory: %w", err)
	}
	partial := path + ".partial"
	if err := os.WriteFile(partial, data, 0o600); err != nil {
		return xerrors.Errorf("write partial archive: %w", err)
	}
	if err := os.Rename(partial, path); err != nil {
		_ = os.Remove(partial)
		return xerrors.Errorf("finalize archive: %w", err)
	}
	return nil
}
