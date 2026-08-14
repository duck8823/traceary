package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/attestation"
)

var attestationAnchorProcessLocks sync.Map

// AttestationAnchorPath is the append-only sidecar beside the store.
func AttestationAnchorPath(storePath string) string {
	return storePath + attestation.AnchorFileSuffix
}

// PublishAttestationAnchorFromDB appends the current store head when the
// sidecar's last record is not already that head. Missing tables are a no-op.
func PublishAttestationAnchorFromDB(ctx context.Context, db *sql.DB, storePath string) error {
	if storePath == "" || db == nil {
		return nil
	}
	seq, head, ok, err := readAttestationHead(ctx, db)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return publishAttestationAnchor(AttestationAnchorPath(storePath), attestation.AnchorRecord{
		Version:     attestation.AnchorFormatVersion,
		Seq:         seq,
		Head:        head,
		PublishedAt: formatTimestamp(time.Now().UTC()),
	})
}

// AttestationAnchorInspector implements application.AttestationAnchorInspector.
type AttestationAnchorInspector struct {
	db *Database
}

// NewAttestationAnchorInspector binds inspector reads to the given store.
func NewAttestationAnchorInspector(db *Database) *AttestationAnchorInspector {
	return &AttestationAnchorInspector{db: db}
}

// InspectAttestationAnchor reports the sidecar and, when OpenStore is set,
// the live head after VerifyAttestationChain. Behind/missing files are
// published only when the chain verifies.
func (i *AttestationAnchorInspector) InspectAttestationAnchor(
	ctx context.Context,
	opts application.AttestationAnchorInspectOptions,
) (application.AttestationAnchorState, error) {
	storePath := opts.StorePath
	if storePath == "" && i != nil && i.db != nil {
		storePath = i.db.Path()
	}
	state := application.AttestationAnchorState{Path: AttestationAnchorPath(storePath)}
	records, present, err := readAttestationAnchorFile(state.Path)
	if err != nil {
		return state, err
	}
	state.FilePresent = present
	if present && len(records) > 0 {
		last := records[len(records)-1]
		state.FileSeq = last.Seq
		state.FileHead = last.Head
	}

	if !opts.OpenStore {
		if !present {
			state.Relation = string(attestation.AnchorMissing)
		} else {
			state.Relation = "file_ok"
		}
		return state, nil
	}

	if i == nil || i.db == nil {
		return state, xerrors.Errorf("attestation anchor inspector has no store")
	}
	conn, err := i.db.openAt(ctx, storePath)
	if err != nil {
		return state, xerrors.Errorf("open store for attestation anchor: %w", err)
	}
	defer func() { _ = conn.Close() }()

	snapshot, err := verifyAttestationChainSnapshot(ctx, conn)
	if err != nil {
		state.ChainOK = false
		state.Relation = "chain_broken"
		return state, err
	}
	state.ChainOK = true
	if afterVerifiedAttestationSnapshot != nil {
		afterVerifiedAttestationSnapshot(ctx, conn)
	}
	if !snapshot.Present {
		state.Relation = string(attestation.AnchorMissing)
		return state, nil
	}
	state.StoreSeq = snapshot.Seq
	state.StoreHead = snapshot.Head
	last := lastOrZero(records)
	if present && last.Seq <= snapshot.Seq && !historicalAnchorMatches(snapshot, last) {
		state.Relation = string(attestation.AnchorMismatch)
		return state, nil
	}
	relation := attestation.RelateAnchor(snapshot.Seq, snapshot.Head, last, present)
	state.Relation = string(relation)
	switch relation {
	case attestation.AnchorMissing, attestation.AnchorBehind:
		if err := publishAttestationAnchor(state.Path, attestation.AnchorRecord{
			Version:     attestation.AnchorFormatVersion,
			Seq:         snapshot.Seq,
			Head:        snapshot.Head,
			PublishedAt: formatTimestamp(time.Now().UTC()),
		}); err != nil {
			return state, err
		}
		state.Published = true
		state.FilePresent = true
		state.FileSeq = snapshot.Seq
		state.FileHead = snapshot.Head
		state.Relation = string(attestation.AnchorMatches)
	}
	return state, nil
}

var afterVerifiedAttestationSnapshot func(context.Context, *sql.DB)

func historicalAnchorMatches(snapshot attestationChainSnapshot, last attestation.AnchorRecord) bool {
	want, ok := snapshot.LinkHeads[last.Seq]
	if !ok {
		return false
	}
	return strings.EqualFold(want, last.Head)
}

func lastOrZero(records []attestation.AnchorRecord) attestation.AnchorRecord {
	if len(records) == 0 {
		return attestation.AnchorRecord{}
	}
	return records[len(records)-1]
}

func readAttestationHead(ctx context.Context, db *sql.DB) (int64, string, bool, error) {
	enabled, err := databaseTableExists(ctx, db, "attestation_head")
	if err != nil {
		return 0, "", false, err
	}
	if !enabled {
		return 0, "", false, nil
	}
	var head string
	var seq int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT head_sha256, seq FROM attestation_head WHERE singleton = 1`,
	).Scan(&head, &seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", false, nil
		}
		return 0, "", false, xerrors.Errorf("read attestation head: %w", err)
	}
	return seq, head, true, nil
}

func readAttestationAnchorFile(path string) ([]attestation.AnchorRecord, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, xerrors.Errorf("read attestation anchor: %w", err)
	}
	if len(body) == 0 {
		return nil, false, nil
	}
	records, err := attestation.ParseAnchorFile(body)
	if err != nil {
		return nil, true, xerrors.Errorf("parse attestation anchor: %w", err)
	}
	return records, true, nil
}

func publishAttestationAnchor(path string, record attestation.AnchorRecord) error {
	unlock, err := lockAttestationAnchor(path)
	if err != nil {
		return err
	}
	defer unlock()

	records, present, err := readAttestationAnchorFile(path)
	if err != nil {
		return err
	}
	appendLine, err := attestation.DecideAnchorAppend(lastOrZero(records), present && len(records) > 0, record)
	if err != nil {
		return xerrors.Errorf("decide attestation anchor append: %w", err)
	}
	if !appendLine {
		return nil
	}
	line, err := attestation.FormatAnchorLine(record)
	if err != nil {
		return xerrors.Errorf("format attestation anchor: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return xerrors.Errorf("open attestation anchor: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Debug("failed to close attestation anchor", "error", closeErr)
		}
	}()
	if _, err := file.Write(append(append([]byte{}, line...), '\n')); err != nil {
		return xerrors.Errorf("append attestation anchor: %w", err)
	}
	if err := file.Sync(); err != nil {
		return xerrors.Errorf("sync attestation anchor: %w", err)
	}
	return nil
}

func lockAttestationAnchor(path string) (func(), error) {
	muAny, _ := attestationAnchorProcessLocks.LoadOrStore(path, &sync.Mutex{})
	mu, ok := muAny.(*sync.Mutex)
	if !ok {
		return nil, xerrors.Errorf("attestation anchor process lock has unexpected type")
	}
	mu.Lock()
	fileLock := flock.New(path + ".lock")
	if err := fileLock.Lock(); err != nil {
		mu.Unlock()
		return nil, xerrors.Errorf("lock attestation anchor: %w", err)
	}
	return func() {
		if unlockErr := fileLock.Unlock(); unlockErr != nil {
			slog.Debug("failed to unlock attestation anchor", "error", unlockErr)
		}
		mu.Unlock()
	}, nil
}

func publishAttestationAnchorAfterCommit(storePath string, record attestation.AnchorRecord) {
	if storePath == "" {
		return
	}
	if err := publishAttestationAnchor(AttestationAnchorPath(storePath), record); err != nil {
		slog.Debug("failed to publish attestation anchor", "error", err)
	}
}
