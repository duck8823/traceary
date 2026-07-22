//go:build unix

package filesystem

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
	// Register the pure-Go SQLite driver for read-only backup verification.
	_ "modernc.org/sqlite"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	fileRetentionReservedPrefix = ".traceary-retention-"
	fileRetentionLockName       = ".traceary-retention-lock"
	fileRetentionCatalogName    = ".traceary-retention-catalog.json"
	fileRetentionLedgerName     = ".traceary-retention-ledger.json"
	fileRetentionLeaseDuration  = time.Minute
)

// FileRetentionDatasource provides fd-confined inventory and crash-retryable deletion.
type FileRetentionDatasource struct {
	afterBoundary func(string) error
	inspectName   func(int, string) (bool, error)
}

// NewFileRetentionDatasource creates the local archive/backup adapter.
func NewFileRetentionDatasource() *FileRetentionDatasource { return &FileRetentionDatasource{} }

var (
	_ application.FileRetentionInventory = (*FileRetentionDatasource)(nil)
	_ application.FileRetentionExecutor  = (*FileRetentionDatasource)(nil)
)

// InspectFileRetention returns a complete direct-child inventory without writes.
func (datasource *FileRetentionDatasource) InspectFileRetention(ctx context.Context, request apptypes.FileRetentionInventoryRequest) (apptypes.FileRetentionInventorySnapshot, error) {
	if request.Class != "archive" && request.Class != "backup" {
		return apptypes.FileRetentionInventorySnapshot{}, xerrors.Errorf("unsupported file retention class %q", request.Class)
	}
	root, err := filepath.Abs(strings.TrimSpace(request.Root))
	if err != nil || root == "" {
		return apptypes.FileRetentionInventorySnapshot{}, xerrors.Errorf("resolve file retention root: %w", err)
	}
	rootFD, rootStat, err := openFileRetentionRoot(root)
	if err != nil {
		return apptypes.FileRetentionInventorySnapshot{}, err
	}
	defer func() { _ = unix.Close(rootFD) }()
	rootIdentity := fileRetentionRootIdentity(rootStat)
	liveGeneration, err := fileRetentionLiveGeneration(ctx, request.Class, request.DatabasePath)
	if err != nil {
		return apptypes.FileRetentionInventorySnapshot{}, err
	}
	entries, err := datasource.inspectFileRetentionEntries(ctx, rootFD, root, rootStat, rootIdentity, request, liveGeneration)
	if err != nil {
		return apptypes.FileRetentionInventorySnapshot{}, err
	}
	return apptypes.FileRetentionInventorySnapshot{
		Class: request.Class, Root: root, RootIdentity: rootIdentity, LiveGeneration: liveGeneration, Entries: entries,
	}, nil
}

func (datasource *FileRetentionDatasource) inspectFileRetentionEntries(
	ctx context.Context,
	rootFD int,
	root string,
	rootStat unix.Stat_t,
	rootIdentity string,
	request apptypes.FileRetentionInventoryRequest,
	liveGeneration string,
) ([]apptypes.FileRetentionInventoryEntry, error) {
	duplicateFD, err := unix.Dup(rootFD)
	if err != nil {
		return nil, xerrors.Errorf("duplicate file retention root descriptor: %w", err)
	}
	directory := os.NewFile(uintptr(duplicateFD), root)
	dirEntries, err := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err != nil {
		return nil, xerrors.Errorf("read file retention root: %w", err)
	}
	if closeErr != nil {
		return nil, xerrors.Errorf("close file retention root listing: %w", closeErr)
	}
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })
	result := make([]apptypes.FileRetentionInventoryEntry, 0, len(dirEntries))
	backupManifests := map[string]backupRetentionManifestEvidence{}
	if request.Class == "backup" {
		var blockers []apptypes.FileRetentionInventoryEntry
		backupManifests, blockers = readBackupRetentionManifests(rootFD, dirEntries, rootIdentity)
		result = append(result, blockers...)
	}
	for _, dirEntry := range dirEntries {
		if err := ctx.Err(); err != nil {
			return nil, xerrors.Errorf("inspect file retention root: %w", err)
		}
		name := dirEntry.Name()
		if strings.HasPrefix(name, fileRetentionReservedPrefix) || strings.HasSuffix(name, ".partial") {
			continue
		}
		entry, err := datasource.inspectFileRetentionEntry(rootFD, rootStat, rootIdentity, request, liveGeneration, backupManifests, name)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if !left.GenerationCreatedAt.Equal(right.GenerationCreatedAt) {
			return left.GenerationCreatedAt.Before(right.GenerationCreatedAt)
		}
		if left.Generation != right.Generation {
			return left.Generation < right.Generation
		}
		if left.ContentSHA256 != right.ContentSHA256 {
			return left.ContentSHA256 < right.ContentSHA256
		}
		return left.RelativePath < right.RelativePath
	})
	return result, nil
}

func readBackupRetentionManifests(
	rootFD int,
	dirEntries []os.DirEntry,
	rootIdentity string,
) (map[string]backupRetentionManifestEvidence, []apptypes.FileRetentionInventoryEntry) {
	manifests := make(map[string]backupRetentionManifestEvidence)
	var blockers []apptypes.FileRetentionInventoryEntry
	for _, dirEntry := range dirEntries {
		name := dirEntry.Name()
		if !strings.HasPrefix(name, apptypes.BackupRetentionManifestPrefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		var manifest apptypes.BackupRetentionManifest
		manifestBytes, err := readRootFile(rootFD, name)
		if err == nil {
			err = decodeRootJSON(name, manifestBytes, &manifest)
		}
		validDigest := isCanonicalSHA256(manifest.BackupSHA256) && manifest.RelativePath != "" && filepath.Base(manifest.RelativePath) == manifest.RelativePath && name == apptypes.BackupRetentionManifestName(manifest.RelativePath)
		createdAt, timeErr := time.Parse(time.RFC3339Nano, manifest.CreatedAt)
		if err != nil || manifest.SchemaVersion != apptypes.BackupRetentionManifestSchema || !validDigest || !isCanonicalSHA256(manifest.SourceLineage) || timeErr != nil {
			digest := sha256HexString("invalid-backup-manifest:" + name)
			blockers = append(blockers, apptypes.FileRetentionInventoryEntry{
				Identity: sha256HexString(rootIdentity + ":invalid-backup-manifest:" + name), RelativePath: name,
				AllocatedKnown: true, ModifiedAt: time.Unix(1, 0).UTC(), GenerationCreatedAt: time.Unix(1, 0).UTC(),
				GenerationProvenance: "backup_manifest", Generation: digest, ContentSHA256: digest, BlockingReason: "invalid_backup_manifest",
			})
			continue
		}
		manifest.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		manifests[manifest.RelativePath] = backupRetentionManifestEvidence{manifest: manifest, name: name, digest: sha256HexBytes(manifestBytes)}
	}
	return manifests, blockers
}

func (datasource *FileRetentionDatasource) inspectFileRetentionEntry(
	rootFD int,
	rootStat unix.Stat_t,
	rootIdentity string,
	request apptypes.FileRetentionInventoryRequest,
	liveGeneration string,
	backupManifests map[string]backupRetentionManifestEvidence,
	name string,
) (apptypes.FileRetentionInventoryEntry, error) {
	entry := apptypes.FileRetentionInventoryEntry{RelativePath: name, AllocatedKnown: true}
	fd, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return blockedFileRetentionPathEntry(rootFD, rootIdentity, name), nil
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() { _ = file.Close() }()
	if err := datasource.boundary("inventory-opened:" + name); err != nil {
		return entry, err
	}
	var openedStat unix.Stat_t
	if err := unix.Fstat(fd, &openedStat); err != nil {
		return entry, xerrors.Errorf("stat open file retention entry %s: %w", name, err)
	}
	info, err := file.Stat()
	if err != nil {
		return entry, xerrors.Errorf("stat file retention entry %s: %w", name, err)
	}
	entry.Device, entry.Inode, entry.LinkCount = uint64(openedStat.Dev), uint64(openedStat.Ino), uint64(openedStat.Nlink)
	entry.LogicalBytes, entry.AllocatedBytes = openedStat.Size, openedStat.Blocks*512
	entry.ModifiedAt = info.ModTime().UTC()
	if openedStat.Mode&unix.S_IFMT != unix.S_IFREG {
		entry.BlockingReason = "non_regular"
		entry.GenerationCreatedAt = time.Unix(1, 0).UTC()
		entry.ContentSHA256 = sha256HexString(fmt.Sprintf("non-regular:%s:%d:%d", name, openedStat.Dev, openedStat.Ino))
		entry.Identity = fileRetentionIdentity(rootIdentity, name, openedStat, entry.ModifiedAt, entry.ContentSHA256)
		return entry, nil
	}
	if openedStat.Dev != rootStat.Dev {
		entry.BlockingReason = "device_boundary"
	}
	if openedStat.Nlink != 1 {
		entry.BlockingReason = "hard_link"
	}
	if !fileRetentionPathMatchesStat(rootFD, name, openedStat) {
		entry.BlockingReason = "changed_during_inventory"
	}
	data, err := io.ReadAll(file)
	if err != nil {
		entry.BlockingReason = "unreadable"
		data = nil
	}
	var finalStat unix.Stat_t
	finalInfo, finalInfoErr := file.Stat()
	if statErr := unix.Fstat(fd, &finalStat); statErr != nil || finalInfoErr != nil || !sameFileRetentionStat(openedStat, finalStat) || !info.ModTime().Equal(finalInfo.ModTime()) || !fileRetentionPathMatchesStat(rootFD, name, finalStat) {
		entry.BlockingReason = "changed_during_inventory"
	}
	contentDigest := sha256.Sum256(data)
	entry.ContentSHA256 = hex.EncodeToString(contentDigest[:])
	entry.Identity = fileRetentionIdentity(rootIdentity, name, openedStat, entry.ModifiedAt, entry.ContentSHA256)
	entry.GenerationCreatedAt = entry.ModifiedAt
	entry.GenerationProvenance = "filesystem_mtime"
	if entry.BlockingReason != "" {
		return entry, nil
	}

	verification := fileRetentionVerification{generation: sha256HexString("unverified:" + name), createdAt: entry.ModifiedAt}
	if request.Class == "archive" {
		verification = verifyFileRetentionArchive(data, request.DatabasePath, liveGeneration)
	} else {
		verification = verifyFileRetentionBackup(fd, name, entry.ContentSHA256, entry.ModifiedAt, liveGeneration, backupManifests)
	}
	entry.Verified = verification.verified
	entry.Generation = verification.generation
	entry.GenerationCreatedAt = verification.createdAt
	entry.GenerationProvenance = verification.provenance
	entry.VerificationReason = verification.reason
	entry.VerificationDigest = verification.digest
	entry.MetadataRelativePath = verification.metadataRelativePath
	entry.MetadataSHA256 = verification.metadataSHA256
	return entry, nil
}

func blockedFileRetentionPathEntry(rootFD int, rootIdentity, name string) apptypes.FileRetentionInventoryEntry {
	entry := apptypes.FileRetentionInventoryEntry{RelativePath: name, AllocatedKnown: true, BlockingReason: "unreadable"}
	var stat unix.Stat_t
	if err := unix.Fstatat(rootFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		entry.Device, entry.Inode, entry.LinkCount = uint64(stat.Dev), uint64(stat.Ino), uint64(stat.Nlink)
		entry.LogicalBytes, entry.AllocatedBytes = stat.Size, stat.Blocks*512
		if stat.Mode&unix.S_IFMT != unix.S_IFREG {
			entry.BlockingReason = "non_regular"
		}
	}
	entry.GenerationCreatedAt = time.Unix(1, 0).UTC()
	entry.ModifiedAt = entry.GenerationCreatedAt
	entry.ContentSHA256 = sha256HexString(entry.BlockingReason + ":" + name)
	entry.Identity = fileRetentionIdentity(rootIdentity, name, stat, entry.ModifiedAt, entry.ContentSHA256)
	return entry
}

func fileRetentionPathMatchesStat(rootFD int, name string, expected unix.Stat_t) bool {
	var current unix.Stat_t
	return unix.Fstatat(rootFD, name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil &&
		current.Mode&unix.S_IFMT == unix.S_IFREG && current.Dev == expected.Dev && current.Ino == expected.Ino
}

func sameFileRetentionStat(left, right unix.Stat_t) bool {
	return left.Mode&unix.S_IFMT == right.Mode&unix.S_IFMT && left.Dev == right.Dev && left.Ino == right.Ino &&
		left.Nlink == right.Nlink && left.Size == right.Size && left.Blocks == right.Blocks
}

type fileRetentionVerification struct {
	verified             bool
	generation           string
	createdAt            time.Time
	provenance           string
	reason               string
	digest               string
	metadataRelativePath string
	metadataSHA256       string
}

type backupRetentionManifestEvidence struct {
	manifest apptypes.BackupRetentionManifest
	name     string
	digest   string
}

type fileRetentionArchiveManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Format        string `json:"format"`
	CreatedAt     string `json:"created_at"`
	SourceDB      *struct {
		Path string `json:"path"`
	} `json:"source_db_fingerprint"`
	Tables []struct {
		Name         string `json:"name"`
		NDJSONSHA256 string `json:"ndjson_sha256"`
	} `json:"tables"`
	PayloadSHA256 string `json:"payload_sha256"`
	Encryption    struct {
		Mode string `json:"mode"`
	} `json:"encryption"`
}

func verifyFileRetentionArchive(data []byte, databasePath, liveGeneration string) fileRetentionVerification {
	failure := func(reason string) fileRetentionVerification {
		return fileRetentionVerification{generation: sha256HexString("archive-unverified:" + reason), createdAt: time.Unix(1, 0).UTC(), provenance: "archive_manifest", reason: reason}
	}
	if len(data) < 10 || string(data[:8]) != "TRCARYAR" || data[8] != 1 || data[9] != 0x1f {
		return failure("invalid_or_encrypted_archive")
	}
	gzipReader, err := gzip.NewReader(strings.NewReader(string(data[9:])))
	if err != nil {
		return failure("invalid_archive_gzip")
	}
	tarReader := tar.NewReader(gzipReader)
	files := make(map[string][]byte)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || header.Size < 0 || header.Size > 64*1024*1024 {
			_ = gzipReader.Close()
			return failure("invalid_archive_tar")
		}
		payload, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil || int64(len(payload)) != header.Size {
			_ = gzipReader.Close()
			return failure("invalid_archive_member")
		}
		files[header.Name] = payload
	}
	if err := gzipReader.Close(); err != nil {
		return failure("invalid_archive_close")
	}
	var manifest fileRetentionArchiveManifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil || manifest.SchemaVersion != 1 || manifest.Format != "traceary.store.archive" {
		return failure("invalid_archive_manifest")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt)
	if err != nil {
		return failure("invalid_archive_created_at")
	}
	var payloadDigestInput bytes.Buffer
	for _, table := range manifest.Tables {
		payload, ok := files["tables/"+table.Name+".ndjson"]
		if !ok || sha256HexBytes(payload) != table.NDJSONSHA256 {
			return failure("archive_table_digest_mismatch")
		}
		_, _ = payloadDigestInput.WriteString(table.Name + ":" + table.NDJSONSHA256 + "\n")
	}
	if manifest.Encryption.Mode != "none" || sha256HexBytes(payloadDigestInput.Bytes()) != manifest.PayloadSHA256 {
		return failure("archive_payload_digest_mismatch")
	}
	if manifest.SourceDB == nil || !sameCleanPath(manifest.SourceDB.Path, databasePath) {
		return failure("archive_source_mismatch")
	}
	evidence := sha256HexBytes(files["manifest.json"])
	return fileRetentionVerification{verified: true, generation: liveGeneration, createdAt: createdAt.UTC(), provenance: "archive_manifest", digest: evidence}
}

func verifyFileRetentionBackup(
	fileFD int,
	name string,
	contentDigest string,
	modifiedAt time.Time,
	liveGeneration string,
	manifests map[string]backupRetentionManifestEvidence,
) fileRetentionVerification {
	evidence, exists := manifests[name]
	if !exists {
		return fileRetentionVerification{generation: sha256HexString("backup-unverified:" + name), createdAt: modifiedAt.UTC(), provenance: "filesystem_mtime", reason: "backup_manifest_missing"}
	}
	manifest := evidence.manifest
	generation, integrity, err := sqliteFileRetentionGenerationFromFD(fileFD)
	if err != nil {
		return fileRetentionVerification{generation: sha256HexString("backup-unverified:" + name), provenance: "sqlite", reason: "invalid_sqlite_backup"}
	}
	createdAt, parseErr := time.Parse(time.RFC3339Nano, manifest.CreatedAt)
	if parseErr != nil {
		return fileRetentionVerification{generation: manifest.SourceLineage, provenance: "backup_manifest", reason: "invalid_backup_manifest_time"}
	}
	verified := integrity == "ok" && manifest.SourceLineage == liveGeneration && manifest.BackupSHA256 == contentDigest
	reason := ""
	if integrity != "ok" {
		reason = "sqlite_integrity_failed"
	} else if manifest.SourceLineage != liveGeneration {
		reason = "backup_generation_mismatch"
	}
	return fileRetentionVerification{
		verified: verified, generation: manifest.SourceLineage, createdAt: createdAt.UTC(), provenance: "backup_manifest",
		reason: reason, digest: sha256HexString(generation + ":" + integrity + ":" + manifest.SourceLineage + ":" + contentDigest),
		metadataRelativePath: evidence.name, metadataSHA256: evidence.digest,
	}
}

func sqliteFileRetentionGenerationFromFD(fileFD int) (string, string, error) {
	var lastErr error
	for _, path := range []string{fmt.Sprintf("/dev/fd/%d", fileFD), fmt.Sprintf("/proc/self/fd/%d", fileFD)} {
		generation, integrity, err := sqliteFileRetentionGeneration(path)
		if err == nil {
			return generation, integrity, nil
		}
		lastErr = err
	}
	return "", "", lastErr
}

func fileRetentionLiveGeneration(ctx context.Context, class, databasePath string) (string, error) {
	lineage, err := domtypes.FileRetentionLineageFromPath(class, databasePath)
	if err != nil {
		return "", xerrors.Errorf("derive file retention lineage: %w", err)
	}
	_, integrity, err := sqliteFileRetentionGeneration(databasePath)
	if err != nil {
		return "", xerrors.Errorf("inspect live SQLite generation: %w", err)
	}
	if integrity != "ok" {
		return "", xerrors.Errorf("live SQLite integrity check returned %q", integrity)
	}
	if err := ctx.Err(); err != nil {
		return "", xerrors.Errorf("inspect file retention live generation: %w", err)
	}
	return lineage, nil
}

func sqliteFileRetentionGeneration(path string) (string, string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", "", xerrors.Errorf("resolve SQLite verification path: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro&immutable=1"}).String()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", "", xerrors.Errorf("open SQLite verification database: %w", err)
	}
	defer func() { _ = database.Close() }()
	var integrity string
	if err := database.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return "", "", xerrors.Errorf("run SQLite integrity check: %w", err)
	}
	var userVersion int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		return "", "", xerrors.Errorf("read SQLite user version: %w", err)
	}
	rows, err := database.Query(`SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name, tbl_name, sql`)
	if err != nil {
		return "", "", xerrors.Errorf("read SQLite schema objects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "user_version=%d\n", userVersion)
	for rows.Next() {
		var objectType, name, table, statement string
		if err := rows.Scan(&objectType, &name, &table, &statement); err != nil {
			return "", "", xerrors.Errorf("scan SQLite schema object: %w", err)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\n", objectType, name, table, statement)
	}
	if err := rows.Err(); err != nil {
		return "", "", xerrors.Errorf("iterate SQLite schema objects: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), integrity, nil
}

// ApplyFileRetention executes exact candidates serially under root locks.
func (datasource *FileRetentionDatasource) ApplyFileRetention(ctx context.Context, plan apptypes.FileRetentionPlan, confirmedPlanID string, now time.Time) (apptypes.FileRetentionApplyResult, error) {
	if plan.PlanID != confirmedPlanID {
		return apptypes.FileRetentionApplyResult{}, xerrors.New("file retention confirmation mismatch")
	}
	result := apptypes.FileRetentionApplyResult{PlanID: plan.PlanID}
	for _, classPlan := range plan.CanonicalPayload.Classes {
		classResult, err := datasource.applyFileRetentionClass(ctx, plan, classPlan, now)
		if err != nil {
			return result, err
		}
		result.CandidateCount += classResult.CandidateCount
		result.DeletedCount += classResult.DeletedCount
		result.AlreadyCommitted += classResult.AlreadyCommitted
		result.ConflictedCount += classResult.ConflictedCount
	}
	return result, nil
}

type fileRetentionCatalog struct {
	NextToken uint64                              `json:"next_token"`
	Lease     *fileRetentionLease                 `json:"lease,omitempty"`
	Items     map[string]fileRetentionCatalogItem `json:"items"`
}

type fileRetentionLease struct {
	PlanID    string `json:"plan_id"`
	Token     uint64 `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type fileRetentionCatalogItem struct {
	State  string `json:"state"`
	PlanID string `json:"plan_id"`
	Token  uint64 `json:"token"`
}

type fileRetentionLedger struct {
	Entries []fileRetentionLedgerEntry `json:"entries"`
}

type fileRetentionLedgerEntry struct {
	PlanID    string `json:"plan_id"`
	Identity  string `json:"identity"`
	Committed string `json:"committed_at"`
}

type fileRetentionJournal struct {
	Sequence      uint64 `json:"sequence"`
	PlanID        string `json:"plan_id"`
	Identity      string `json:"identity"`
	RelativePath  string `json:"relative_path"`
	TombstonePath string `json:"tombstone_path"`
	Token         uint64 `json:"token"`
	State         string `json:"state"`
}

func (datasource *FileRetentionDatasource) applyFileRetentionClass(ctx context.Context, plan apptypes.FileRetentionPlan, classPlan apptypes.FileRetentionClassPlan, now time.Time) (apptypes.FileRetentionApplyResult, error) {
	result := apptypes.FileRetentionApplyResult{PlanID: plan.PlanID, CandidateCount: len(classPlan.Candidates)}
	rootFD, rootStat, err := openFileRetentionRoot(classPlan.Root)
	if err != nil {
		return result, err
	}
	defer func() { _ = unix.Close(rootFD) }()
	if fileRetentionRootIdentity(rootStat) != classPlan.RootIdentity {
		return result, xerrors.New("file retention root identity changed")
	}
	if rootStat.Uid != uint32(os.Geteuid()) || rootStat.Mode&0o022 != 0 {
		return result, xerrors.New("file retention apply requires a caller-owned root without group/other write access")
	}
	lockFD, err := openAndLockFileRetentionRoot(rootFD, rootStat)
	if err != nil {
		return result, err
	}
	defer func() {
		_ = unix.Flock(lockFD, unix.LOCK_UN)
		_ = unix.Close(lockFD)
	}()
	catalog, err := readFileRetentionCatalog(rootFD)
	if err != nil {
		return result, err
	}
	ledger, err := readFileRetentionLedger(rootFD)
	if err != nil {
		return result, err
	}
	if catalog.Items == nil {
		catalog.Items = make(map[string]fileRetentionCatalogItem)
	}
	for _, item := range catalog.Items {
		if item.State == "deleting" && item.PlanID != plan.PlanID {
			return result, xerrors.New("file retention root has an incomplete different plan")
		}
	}
	if catalog.Lease != nil {
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, catalog.Lease.ExpiresAt)
		if parseErr != nil || !now.UTC().After(expiresAt) {
			return result, xerrors.New("file retention root has an active lease")
		}
	}
	catalog.NextToken++
	token := catalog.NextToken
	catalog.Lease = &fileRetentionLease{PlanID: plan.PlanID, Token: token, ExpiresAt: now.UTC().Add(fileRetentionLeaseDuration).Format(time.RFC3339Nano)}
	if err := writeRootJSON(rootFD, fileRetentionCatalogName, catalog); err != nil {
		return result, err
	}
	if err := datasource.boundary("lease-acquired"); err != nil {
		return result, err
	}

	for _, candidate := range classPlan.Candidates {
		if err := ctx.Err(); err != nil {
			return result, xerrors.Errorf("apply file retention: %w", err)
		}
		journalName := fileRetentionJournalName(plan.PlanID, candidate.Identity)
		journal, exists, err := readFileRetentionJournal(rootFD, journalName)
		if err != nil {
			return result, err
		}
		ledgerCommitted := fileRetentionLedgerContains(ledger, plan.PlanID, candidate.Identity)
		if ledgerCommitted && (!exists || journal.State == "committed") {
			item, catalogExists := catalog.Items[candidate.Identity]
			if !catalogExists || item.State != "deleted" || item.PlanID != plan.PlanID {
				return result, xerrors.New("file retention ledger and catalog do not converge")
			}
			result.AlreadyCommitted++
			continue
		}
		if err := datasource.verifyFileRetentionPlanSnapshot(ctx, plan, classPlan, ledger, journal, exists); err != nil {
			return result, err
		}
		if !exists {
			if ledgerCommitted {
				return result, xerrors.New("file retention ledger exists without a recoverable journal")
			}
			journal = fileRetentionJournal{
				Sequence: 1, PlanID: plan.PlanID, Identity: candidate.Identity, RelativePath: candidate.RelativePath,
				TombstonePath: fileRetentionTombstoneName(plan.PlanID, candidate.Identity), Token: token, State: "pending",
			}
			if err := writeRootJSON(rootFD, journalName, journal); err != nil {
				return result, err
			}
			if err := datasource.boundary("pending"); err != nil {
				return result, err
			}
		} else if journal.Token != token {
			if catalog.Lease == nil || catalog.Lease.Token != token {
				return result, xerrors.New("file retention token takeover is not catalog-fenced")
			}
			journal.Token = token
			journal.Sequence++
			if err := writeRootJSON(rootFD, journalName, journal); err != nil {
				return result, err
			}
			if err := datasource.boundary("token-takeover"); err != nil {
				return result, err
			}
		}
		if err := datasource.resumeFileRetentionCandidate(ctx, rootFD, rootStat, plan, classPlan, candidate, &catalog, &ledger, journalName, &journal, now); err != nil {
			result.ConflictedCount++
			return result, err
		}
		result.DeletedCount++
	}
	catalog.Lease = nil
	if err := writeRootJSON(rootFD, fileRetentionCatalogName, catalog); err != nil {
		return result, err
	}
	if err := datasource.boundary("lease-released"); err != nil {
		return result, err
	}
	return result, nil
}

func (datasource *FileRetentionDatasource) resumeFileRetentionCandidate(
	ctx context.Context,
	rootFD int,
	rootStat unix.Stat_t,
	plan apptypes.FileRetentionPlan,
	classPlan apptypes.FileRetentionClassPlan,
	candidate apptypes.FileRetentionCandidatePlan,
	catalog *fileRetentionCatalog,
	ledger *fileRetentionLedger,
	journalName string,
	journal *fileRetentionJournal,
	now time.Time,
) error {
	for journal.State != "committed" {
		switch journal.State {
		case "pending":
			catalog.Items[candidate.Identity] = fileRetentionCatalogItem{State: "deleting", PlanID: plan.PlanID, Token: journal.Token}
			if err := writeRootJSON(rootFD, fileRetentionCatalogName, catalog); err != nil {
				return err
			}
			if err := datasource.boundary("catalog-deleting"); err != nil {
				return err
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "running"); err != nil {
				return err
			}
		case "running":
			if err := verifyFileRetentionCandidateAt(rootFD, rootStat, classPlan, candidate); err != nil {
				return xerrors.Errorf("file retention candidate conflicted: %w", err)
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "tombstone_intent"); err != nil {
				return err
			}
		case "tombstone_intent":
			if err := datasource.verifyFileRetentionPlanSnapshot(ctx, plan, classPlan, *ledger, *journal, true); err != nil {
				return err
			}
			if err := datasource.ensureFileRetentionTombstone(rootFD, rootStat, classPlan, candidate, journal.TombstonePath); err != nil {
				return err
			}
			if err := datasource.boundary("tombstone-linked"); err != nil {
				return err
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "tombstoned"); err != nil {
				return err
			}
		case "tombstoned":
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "unlink_original_intent"); err != nil {
				return err
			}
		case "unlink_original_intent":
			if err := datasource.verifyFileRetentionPlanSnapshot(ctx, plan, classPlan, *ledger, *journal, true); err != nil {
				return err
			}
			if err := requireFileRetentionNameAbsent(rootFD, candidate.RelativePath); err != nil {
				return err
			}
			if err := datasource.boundary("original-unlinked"); err != nil {
				return err
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "original_unlinked"); err != nil {
				return err
			}
		case "original_unlinked":
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "unlink_tombstone_intent"); err != nil {
				return err
			}
		case "unlink_tombstone_intent":
			if err := datasource.verifyFileRetentionPlanSnapshot(ctx, plan, classPlan, *ledger, *journal, true); err != nil {
				return err
			}
			tombstoneExists, err := datasource.fileRetentionNameExists(rootFD, journal.TombstonePath)
			if err != nil {
				return err
			}
			if tombstoneExists {
				entry, _ := findFileRetentionInventoryPlan(classPlan, candidate.Identity)
				if err := verifyFileRetentionNameAt(rootFD, rootStat, classPlan, candidate, journal.TombstonePath); err != nil {
					return err
				}
				if err := verifyFileRetentionMetadataAt(rootFD, rootStat, entry); err != nil {
					return err
				}
				if err := unlinkFileRetentionName(rootFD, journal.TombstonePath); err != nil {
					return err
				}
			}
			if err := datasource.boundary("tombstone-unlinked"); err != nil {
				return err
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "tombstone_unlinked"); err != nil {
				return err
			}
		case "tombstone_unlinked":
			entry, _ := findFileRetentionInventoryPlan(classPlan, candidate.Identity)
			nextState := "catalog_commit_intent"
			if entry.MetadataRelativePath != "" {
				nextState = "metadata_unlink_intent"
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, nextState); err != nil {
				return err
			}
		case "metadata_unlink_intent":
			entry, _ := findFileRetentionInventoryPlan(classPlan, candidate.Identity)
			if err := datasource.verifyFileRetentionPlanSnapshot(ctx, plan, classPlan, *ledger, *journal, true); err != nil {
				return err
			}
			if err := datasource.moveAndUnlinkFileRetentionMetadata(rootFD, rootStat, entry, journal.TombstonePath+".metadata"); err != nil {
				return err
			}
			if err := datasource.boundary("metadata-unlinked"); err != nil {
				return err
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "metadata_unlinked"); err != nil {
				return err
			}
		case "metadata_unlinked":
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "catalog_commit_intent"); err != nil {
				return err
			}
		case "catalog_commit_intent":
			if !fileRetentionLedgerContains(*ledger, plan.PlanID, candidate.Identity) {
				ledger.Entries = append(ledger.Entries, fileRetentionLedgerEntry{PlanID: plan.PlanID, Identity: candidate.Identity, Committed: now.UTC().Format(time.RFC3339Nano)})
				if err := writeRootJSON(rootFD, fileRetentionLedgerName, ledger); err != nil {
					return err
				}
			}
			if err := datasource.boundary("ledger-recorded"); err != nil {
				return err
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "ledger_recorded"); err != nil {
				return err
			}
		case "ledger_recorded":
			catalog.Items[candidate.Identity] = fileRetentionCatalogItem{State: "deleted", PlanID: plan.PlanID, Token: journal.Token}
			if err := writeRootJSON(rootFD, fileRetentionCatalogName, catalog); err != nil {
				return err
			}
			if err := datasource.boundary("catalog-deleted"); err != nil {
				return err
			}
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "catalog_deleted"); err != nil {
				return err
			}
		case "catalog_deleted":
			if err := datasource.advanceFileRetentionJournal(rootFD, journalName, journal, "committed"); err != nil {
				return err
			}
		default:
			return xerrors.Errorf("unknown file retention journal state %q", journal.State)
		}
	}
	return nil
}

func (datasource *FileRetentionDatasource) verifyFileRetentionPlanSnapshot(
	ctx context.Context,
	plan apptypes.FileRetentionPlan,
	classPlan apptypes.FileRetentionClassPlan,
	ledger fileRetentionLedger,
	journal fileRetentionJournal,
	hasJournal bool,
) error {
	snapshot, err := datasource.InspectFileRetention(ctx, apptypes.FileRetentionInventoryRequest{Class: classPlan.Class, Root: classPlan.Root, DatabasePath: plan.CanonicalPayload.DatabasePath})
	if err != nil {
		return err
	}
	if snapshot.RootIdentity != classPlan.RootIdentity || snapshot.LiveGeneration != classPlan.LiveGeneration {
		return xerrors.New("file retention root or live generation changed")
	}
	expected := make(map[string]apptypes.FileRetentionInventoryPlan, len(classPlan.Inventory))
	for _, entry := range classPlan.Inventory {
		if fileRetentionLedgerContains(ledger, plan.PlanID, entry.Identity) {
			continue
		}
		expected[entry.Identity] = entry
	}
	currentEntries := snapshot.Entries
	if hasJournal && fileRetentionJournalOwnsCandidate(journal.State) {
		delete(expected, journal.Identity)
		filtered := make([]apptypes.FileRetentionInventoryEntry, 0, len(currentEntries))
		for _, entry := range currentEntries {
			if entry.RelativePath != journal.RelativePath {
				filtered = append(filtered, entry)
			}
		}
		currentEntries = filtered
	}
	if len(currentEntries) != len(expected) {
		return xerrors.New("file retention inventory entry set changed")
	}
	for _, entry := range currentEntries {
		planned, exists := expected[entry.Identity]
		if !exists || !fileRetentionInventoryMatchesPlan(planned, entry) {
			return xerrors.Errorf("file retention inventory changed at %s", entry.RelativePath)
		}
	}
	if classPlan.Floor != nil {
		floor, exists := expected[classPlan.Floor.Identity]
		if !exists || !floor.Verified || floor.Generation != classPlan.LiveGeneration || floor.ContentSHA256 != classPlan.Floor.ContentSHA256 {
			return xerrors.New("file retention recovery floor changed")
		}
	}
	return nil
}

func fileRetentionInventoryMatchesPlan(planned apptypes.FileRetentionInventoryPlan, current apptypes.FileRetentionInventoryEntry) bool {
	allocated := ""
	if current.AllocatedKnown {
		allocated = strconv.FormatInt(current.AllocatedBytes, 10)
	}
	return planned.Identity == current.Identity &&
		planned.RelativePath == current.RelativePath &&
		planned.Device == strconv.FormatUint(current.Device, 10) &&
		planned.Inode == strconv.FormatUint(current.Inode, 10) &&
		planned.LinkCount == strconv.FormatUint(current.LinkCount, 10) &&
		planned.LogicalBytes == strconv.FormatInt(current.LogicalBytes, 10) &&
		planned.AllocatedBytes == allocated &&
		planned.AllocatedKnown == current.AllocatedKnown &&
		planned.ModifiedAt == current.ModifiedAt.UTC().Format(time.RFC3339Nano) &&
		planned.GenerationCreatedAt == current.GenerationCreatedAt.UTC().Format(time.RFC3339Nano) &&
		planned.GenerationProvenance == current.GenerationProvenance &&
		planned.Generation == current.Generation &&
		planned.ContentSHA256 == current.ContentSHA256 &&
		planned.Verified == current.Verified &&
		planned.VerificationDigest == current.VerificationDigest &&
		planned.VerificationReason == current.VerificationReason &&
		planned.MetadataRelativePath == current.MetadataRelativePath &&
		planned.MetadataSHA256 == current.MetadataSHA256 &&
		planned.Pinned == current.Pinned &&
		planned.BlockingReason == current.BlockingReason
}

func fileRetentionJournalOwnsCandidate(state string) bool {
	switch state {
	case "tombstone_intent", "tombstoned", "unlink_original_intent", "original_unlinked", "unlink_tombstone_intent", "tombstone_unlinked", "metadata_unlink_intent", "metadata_unlinked", "catalog_commit_intent", "ledger_recorded", "catalog_deleted", "committed":
		return true
	default:
		return false
	}
}

func verifyFileRetentionCandidateAt(rootFD int, rootStat unix.Stat_t, classPlan apptypes.FileRetentionClassPlan, candidate apptypes.FileRetentionCandidatePlan) error {
	entry, ok := findFileRetentionInventoryPlan(classPlan, candidate.Identity)
	if !ok || entry.RelativePath != candidate.RelativePath || entry.Protected || !entry.Verified || entry.Pinned || entry.BlockingReason != "" {
		return xerrors.New("candidate is not eligible in reviewed inventory")
	}
	if err := verifyFileRetentionNameAt(rootFD, rootStat, classPlan, candidate, candidate.RelativePath); err != nil {
		return err
	}
	return verifyFileRetentionMetadataAt(rootFD, rootStat, entry)
}

func verifyFileRetentionNameAt(rootFD int, rootStat unix.Stat_t, classPlan apptypes.FileRetentionClassPlan, candidate apptypes.FileRetentionCandidatePlan, name string) error {
	entry, ok := findFileRetentionInventoryPlan(classPlan, candidate.Identity)
	if !ok {
		return xerrors.New("candidate is absent from reviewed inventory")
	}
	fd, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return xerrors.Errorf("open file retention candidate name %s: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() { _ = file.Close() }()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return xerrors.Errorf("stat file retention candidate: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Dev != rootStat.Dev {
		return xerrors.New("candidate type, link count, or device changed")
	}
	info, err := file.Stat()
	if err != nil {
		return xerrors.Errorf("read file retention candidate metadata: %w", err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return xerrors.Errorf("read file retention candidate: %w", err)
	}
	digest := sha256HexBytes(data)
	identity := fileRetentionIdentity(classPlan.RootIdentity, candidate.RelativePath, stat, info.ModTime().UTC(), digest)
	if identity != candidate.Identity || digest != entry.ContentSHA256 || !fileRetentionPathMatchesStat(rootFD, name, stat) {
		return xerrors.New("candidate identity or digest changed")
	}
	return nil
}

func verifyFileRetentionMetadataAt(rootFD int, rootStat unix.Stat_t, entry apptypes.FileRetentionInventoryPlan) error {
	if entry.MetadataRelativePath == "" {
		if entry.MetadataSHA256 != "" {
			return xerrors.New("file retention metadata digest has no path")
		}
		return nil
	}
	fd, err := unix.Openat(rootFD, entry.MetadataRelativePath, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return xerrors.Errorf("open file retention metadata: %w", err)
	}
	file := os.NewFile(uintptr(fd), entry.MetadataRelativePath)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return xerrors.Errorf("stat file retention metadata: %w", err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return xerrors.Errorf("read file retention metadata: %w", readErr)
	}
	if closeErr != nil {
		return xerrors.Errorf("close file retention metadata: %w", closeErr)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Dev != rootStat.Dev || sha256HexBytes(data) != entry.MetadataSHA256 {
		return xerrors.New("file retention metadata identity changed")
	}
	return nil
}

func (datasource *FileRetentionDatasource) ensureFileRetentionTombstone(rootFD int, rootStat unix.Stat_t, classPlan apptypes.FileRetentionClassPlan, candidate apptypes.FileRetentionCandidatePlan, tombstone string) error {
	var tombstoneStat unix.Stat_t
	err := unix.Fstatat(rootFD, tombstone, &tombstoneStat, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		if err := verifyFileRetentionNameAt(rootFD, rootStat, classPlan, candidate, tombstone); err != nil {
			return xerrors.New("file retention tombstone collision")
		}
		entry, _ := findFileRetentionInventoryPlan(classPlan, candidate.Identity)
		if err := verifyFileRetentionMetadataAt(rootFD, rootStat, entry); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(err, unix.ENOENT) {
		return xerrors.Errorf("inspect file retention tombstone: %w", err)
	}
	if err := verifyFileRetentionCandidateAt(rootFD, rootStat, classPlan, candidate); err != nil {
		return err
	}
	if err := datasource.boundary("candidate-verified-before-rename"); err != nil {
		return err
	}
	if err := renameFileRetentionNoReplace(rootFD, candidate.RelativePath, tombstone); err != nil {
		return xerrors.Errorf("atomically move file retention candidate to tombstone: %w", err)
	}
	if err := unix.Fsync(rootFD); err != nil {
		return xerrors.Errorf("sync file retention tombstone: %w", err)
	}
	if err := verifyFileRetentionNameAt(rootFD, rootStat, classPlan, candidate, tombstone); err != nil {
		restoreErr := renameFileRetentionNoReplace(rootFD, tombstone, candidate.RelativePath)
		if restoreErr == nil {
			restoreErr = unix.Fsync(rootFD)
		}
		if restoreErr != nil {
			return xerrors.Errorf("verify moved file retention tombstone: %v; preserve unexpected file at %s: %w", err, tombstone, restoreErr)
		}
		return xerrors.Errorf("verify moved file retention tombstone: %w", err)
	}
	return nil
}

func requireFileRetentionNameAbsent(rootFD int, name string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(rootFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return xerrors.Errorf("inspect moved file retention original %s: %w", name, err)
	}
	return xerrors.Errorf("file retention original %s was replaced after tombstone move", name)
}

func (datasource *FileRetentionDatasource) fileRetentionNameExists(rootFD int, name string) (bool, error) {
	if datasource.inspectName != nil {
		return datasource.inspectName(rootFD, name)
	}
	var stat unix.Stat_t
	err := unix.Fstatat(rootFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("inspect file retention name %s: %w", name, err)
	}
	return true, nil
}

func unlinkFileRetentionName(rootFD int, name string) error {
	if err := unix.Unlinkat(rootFD, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return xerrors.Errorf("unlink file retention name %s: %w", name, err)
	}
	if err := unix.Fsync(rootFD); err != nil {
		return xerrors.Errorf("sync file retention root after unlink: %w", err)
	}
	return nil
}

func (datasource *FileRetentionDatasource) moveAndUnlinkFileRetentionMetadata(rootFD int, rootStat unix.Stat_t, entry apptypes.FileRetentionInventoryPlan, tombstone string) error {
	if entry.MetadataRelativePath == "" || entry.MetadataSHA256 == "" {
		return xerrors.New("file retention metadata identity is missing")
	}
	var tombstoneStat unix.Stat_t
	if err := unix.Fstatat(rootFD, tombstone, &tombstoneStat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		metadataExists, existsErr := datasource.fileRetentionNameExists(rootFD, entry.MetadataRelativePath)
		if existsErr != nil {
			return existsErr
		}
		if !metadataExists {
			return nil
		}
		if err := verifyFileRetentionMetadataAt(rootFD, rootStat, entry); err != nil {
			return err
		}
		if err := renameFileRetentionNoReplace(rootFD, entry.MetadataRelativePath, tombstone); err != nil {
			return xerrors.Errorf("atomically move file retention metadata to tombstone: %w", err)
		}
		if err := unix.Fsync(rootFD); err != nil {
			return xerrors.Errorf("sync file retention metadata tombstone: %w", err)
		}
	} else if err != nil {
		return xerrors.Errorf("inspect file retention metadata tombstone: %w", err)
	}
	if err := verifyFileRetentionMetadataNameAt(rootFD, rootStat, entry.MetadataSHA256, tombstone); err != nil {
		restoreErr := renameFileRetentionNoReplace(rootFD, tombstone, entry.MetadataRelativePath)
		if restoreErr == nil {
			restoreErr = unix.Fsync(rootFD)
		}
		if restoreErr != nil {
			return xerrors.Errorf("verify moved file retention metadata: %v; preserve unexpected metadata at %s: %w", err, tombstone, restoreErr)
		}
		return err
	}
	return unlinkFileRetentionName(rootFD, tombstone)
}

func verifyFileRetentionMetadataNameAt(rootFD int, rootStat unix.Stat_t, digest, name string) error {
	fd, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return xerrors.Errorf("open file retention metadata: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = file.Close()
		return xerrors.Errorf("stat file retention metadata: %w", err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return xerrors.Errorf("read file retention metadata: %w", readErr)
	}
	if closeErr != nil {
		return xerrors.Errorf("close file retention metadata: %w", closeErr)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Dev != rootStat.Dev || sha256HexBytes(data) != digest {
		return xerrors.New("file retention metadata identity changed")
	}
	return nil
}

func (datasource *FileRetentionDatasource) advanceFileRetentionJournal(rootFD int, name string, journal *fileRetentionJournal, state string) error {
	journal.Sequence++
	journal.State = state
	if err := writeRootJSON(rootFD, name, journal); err != nil {
		return err
	}
	return datasource.boundary("journal-" + state)
}

func openFileRetentionRoot(root string) (int, unix.Stat_t, error) {
	rootFD, err := descendToDir(root)
	if err != nil {
		return -1, unix.Stat_t{}, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(rootFD, &stat); err != nil {
		_ = unix.Close(rootFD)
		return -1, unix.Stat_t{}, xerrors.Errorf("stat file retention root: %w", err)
	}
	return rootFD, stat, nil
}

func openAndLockFileRetentionRoot(rootFD int, rootStat unix.Stat_t) (int, error) {
	fd, err := unix.Openat(rootFD, fileRetentionLockName, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return -1, xerrors.Errorf("open file retention lock: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Dev != rootStat.Dev || stat.Nlink != 1 {
		_ = unix.Close(fd)
		return -1, xerrors.New("file retention lock identity is unsafe")
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		_ = unix.Close(fd)
		return -1, xerrors.Errorf("lock file retention root: %w", err)
	}
	return fd, nil
}

func readFileRetentionCatalog(rootFD int) (fileRetentionCatalog, error) {
	var value fileRetentionCatalog
	exists, err := readRootJSON(rootFD, fileRetentionCatalogName, &value)
	if err != nil {
		return value, err
	}
	if !exists {
		value.Items = make(map[string]fileRetentionCatalogItem)
	}
	return value, nil
}

func readFileRetentionLedger(rootFD int) (fileRetentionLedger, error) {
	var value fileRetentionLedger
	_, err := readRootJSON(rootFD, fileRetentionLedgerName, &value)
	return value, err
}

func readFileRetentionJournal(rootFD int, name string) (fileRetentionJournal, bool, error) {
	var value fileRetentionJournal
	exists, err := readRootJSON(rootFD, name, &value)
	return value, exists, err
}

func readRootJSON(rootFD int, name string, target any) (bool, error) {
	data, err := readRootFile(rootFD, name)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := decodeRootJSON(name, data, target); err != nil {
		return false, err
	}
	return true, nil
}

func decodeRootJSON(name string, data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return xerrors.Errorf("decode file retention state %s: %w", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return xerrors.Errorf("file retention state %s has trailing JSON", name)
	}
	return nil
}

func isCanonicalSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func readRootFile(rootFD int, name string) ([]byte, error) {
	fd, err := unix.Openat(rootFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, xerrors.Errorf("open file retention state %s: %w", name, err)
	}
	file := os.NewFile(uintptr(fd), name)
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, xerrors.Errorf("read file retention state %s: %w", name, readErr)
	}
	if closeErr != nil {
		return nil, xerrors.Errorf("close file retention state %s: %w", name, closeErr)
	}
	return data, nil
}

func writeRootJSON(rootFD int, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return xerrors.Errorf("marshal file retention state %s: %w", name, err)
	}
	data = append(data, '\n')
	tempName := name + ".tmp"
	fd, err := unix.Openat(rootFD, tempName, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return xerrors.Errorf("create file retention state %s: %w", tempName, err)
	}
	file := os.NewFile(uintptr(fd), tempName)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return xerrors.Errorf("write file retention state %s: %w", name, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return xerrors.Errorf("sync file retention state %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return xerrors.Errorf("close file retention state %s: %w", name, err)
	}
	if err := unix.Renameat(rootFD, tempName, rootFD, name); err != nil {
		return xerrors.Errorf("replace file retention state %s: %w", name, err)
	}
	if err := unix.Fsync(rootFD); err != nil {
		return xerrors.Errorf("sync file retention state %s: %w", name, err)
	}
	return nil
}

func (datasource *FileRetentionDatasource) boundary(name string) error {
	if datasource.afterBoundary == nil {
		return nil
	}
	return datasource.afterBoundary(name)
}

func fileRetentionLedgerContains(ledger fileRetentionLedger, planID, identity string) bool {
	for _, entry := range ledger.Entries {
		if entry.PlanID == planID && entry.Identity == identity {
			return true
		}
	}
	return false
}

func findFileRetentionInventoryPlan(classPlan apptypes.FileRetentionClassPlan, identity string) (apptypes.FileRetentionInventoryPlan, bool) {
	for _, entry := range classPlan.Inventory {
		if entry.Identity == identity {
			return entry, true
		}
	}
	return apptypes.FileRetentionInventoryPlan{}, false
}

func fileRetentionJournalName(planID, identity string) string {
	return fileRetentionReservedPrefix + "journal-" + planID[:12] + "-" + identity[:12] + ".json"
}

func fileRetentionTombstoneName(planID, identity string) string {
	return fileRetentionReservedPrefix + "tombstone-" + planID[:12] + "-" + identity[:12]
}

func fileRetentionRootIdentity(stat unix.Stat_t) string {
	return sha256HexString(fmt.Sprintf("root:%d:%d", stat.Dev, stat.Ino))
}

func fileRetentionIdentity(rootIdentity, name string, stat unix.Stat_t, modifiedAt time.Time, digest string) string {
	return sha256HexString(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%s\x00%s", rootIdentity, name, stat.Dev, stat.Ino, stat.Nlink, stat.Size, modifiedAt.UTC().Format(time.RFC3339Nano), digest))
}

func sha256HexString(value string) string { return sha256HexBytes([]byte(value)) }

func sha256HexBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func sameCleanPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(strings.TrimSpace(left))
	rightAbs, rightErr := filepath.Abs(strings.TrimSpace(right))
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}
