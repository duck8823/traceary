package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/attestation"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// attestationVerifyBusyAttempts bounds the retry on SQLite lock contention
// (initial attempt + 2 retries). See AttestationChainUndeterminedError.
const attestationVerifyBusyAttempts = 3

// attestationVerifyBusyBackoff holds the fixed delay before each retry,
// indexed by attempt number (attempt 1 has no prior delay).
var attestationVerifyBusyBackoff = [attestationVerifyBusyAttempts]time.Duration{
	0, 25 * time.Millisecond, 50 * time.Millisecond,
}

// AttestationChainUndeterminedError reports that verification could not
// obtain a read snapshot (SQLite lock contention). It is not an integrity
// verdict: the chain is neither proven intact nor proven broken.
type AttestationChainUndeterminedError struct{ Err error }

func (e *AttestationChainUndeterminedError) Error() string {
	return "attestation chain verification undetermined: " + e.Err.Error()
}

func (e *AttestationChainUndeterminedError) Unwrap() error { return e.Err }

// attestationChainSnapshot is the verified head and per-seq link digests
// from one read-only transaction.
type attestationChainSnapshot struct {
	Seq       int64
	Head      string
	LinkHeads map[int64]string
	Present   bool
}

// VerifyAttestationChain walks the stored hash chain and recomputes each
// content digest from canonical rows. It is the shipped verifier for tests
// and for #1678; store open does not call it.
//
// A store that predates migration 61 (no tables) is treated as an empty
// chain. Historical events without a link do not fail verification.
func VerifyAttestationChain(ctx context.Context, db *sql.DB) error {
	_, err := verifyAttestationChainSnapshot(ctx, db)
	return err
}

// verifyAttestationChainSnapshot applies the attempt policy around one
// snapshot read: retry while the failure is SQLite lock contention, up to
// attestationVerifyBusyAttempts, honoring ctx cancellation between attempts.
// Any non-busy error is returned unchanged so a real chain break can never
// be relabeled as busy. A terminal busy is wrapped as
// AttestationChainUndeterminedError, which is explicitly not an integrity
// verdict.
func verifyAttestationChainSnapshot(ctx context.Context, db *sql.DB) (attestationChainSnapshot, error) {
	var snapshot attestationChainSnapshot
	var err error
	for attempt := 1; attempt <= attestationVerifyBusyAttempts; attempt++ {
		if attempt > 1 {
			timer := time.NewTimer(attestationVerifyBusyBackoff[attempt-1])
			select {
			case <-ctx.Done():
				timer.Stop()
				return attestationChainSnapshot{}, xerrors.Errorf("attestation verify retry cancelled: %w", ctx.Err())
			case <-timer.C:
			}
		}
		snapshot, err = verifyAttestationChainSnapshotOnce(ctx, db)
		if err == nil || !isSQLiteBusy(err) {
			return snapshot, err
		}
	}
	return attestationChainSnapshot{}, &AttestationChainUndeterminedError{Err: err}
}

func verifyAttestationChainSnapshotOnce(ctx context.Context, db *sql.DB) (attestationChainSnapshot, error) {
	if db == nil {
		return attestationChainSnapshot{}, xerrors.Errorf("attestation verify requires a database")
	}
	enabled, err := databaseTableExists(ctx, db, "attestation_links")
	if err != nil {
		return attestationChainSnapshot{}, err
	}
	if !enabled {
		return attestationChainSnapshot{}, nil
	}
	hasHead, err := databaseTableExists(ctx, db, "attestation_head")
	if err != nil {
		return attestationChainSnapshot{}, err
	}
	if !hasHead {
		return attestationChainSnapshot{}, nil
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return attestationChainSnapshot{}, xerrors.Errorf("begin attestation verify: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var headHex string
	var headSeq int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT head_sha256, seq FROM attestation_head WHERE singleton = 1`,
	).Scan(&headHex, &headSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return attestationChainSnapshot{}, xerrors.Errorf("attestation head is missing")
		}
		return attestationChainSnapshot{}, xerrors.Errorf("read attestation head: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT seq, event_id, kind, content_sha256, prev_sha256, link_sha256
		  FROM attestation_links
		 ORDER BY seq`)
	if err != nil {
		return attestationChainSnapshot{}, xerrors.Errorf("list attestation links: %w", err)
	}
	type storedLink struct {
		seq                       int64
		eventID, kind, contentHex string
		prevHex, linkHex          string
	}
	var links []storedLink
	for rows.Next() {
		var item storedLink
		if err := rows.Scan(&item.seq, &item.eventID, &item.kind, &item.contentHex, &item.prevHex, &item.linkHex); err != nil {
			_ = rows.Close()
			return attestationChainSnapshot{}, xerrors.Errorf("scan attestation link: %w", err)
		}
		links = append(links, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return attestationChainSnapshot{}, xerrors.Errorf("iterate attestation links: %w", err)
	}
	if err := rows.Close(); err != nil {
		return attestationChainSnapshot{}, xerrors.Errorf("close attestation links: %w", err)
	}

	linkHeads := map[int64]string{0: attestation.GenesisHex()}
	prevHex := attestation.GenesisHex()
	for i, item := range links {
		count := int64(i + 1)
		if item.seq != count {
			return attestationChainSnapshot{}, xerrors.Errorf("attestation link seq = %d, want %d", item.seq, count)
		}
		if item.prevHex != prevHex {
			return attestationChainSnapshot{}, xerrors.Errorf("attestation link %d predecessor mismatch", item.seq)
		}
		recomputedContent, err := recomputeAttestationContent(ctx, tx, item.eventID, item.kind)
		if err != nil {
			return attestationChainSnapshot{}, err
		}
		if item.contentHex != recomputedContent {
			return attestationChainSnapshot{}, xerrors.Errorf("attestation link %d content digest does not match store", item.seq)
		}
		prev, err := attestation.ParseHex(prevHex)
		if err != nil {
			return attestationChainSnapshot{}, xerrors.Errorf("parse attestation predecessor: %w", err)
		}
		content, err := attestation.ParseHex(item.contentHex)
		if err != nil {
			return attestationChainSnapshot{}, xerrors.Errorf("parse attestation content digest: %w", err)
		}
		wantLink := attestation.EncodeHex(attestation.LinkSHA256(prev, content))
		if item.linkHex != wantLink {
			return attestationChainSnapshot{}, xerrors.Errorf("attestation link %d digest does not match predecessor and content", item.seq)
		}
		prevHex = item.linkHex
		linkHeads[item.seq] = item.linkHex
	}
	if int64(len(links)) != headSeq {
		return attestationChainSnapshot{}, xerrors.Errorf("attestation head seq = %d, want %d", headSeq, len(links))
	}
	if headHex != prevHex {
		return attestationChainSnapshot{}, xerrors.Errorf("attestation head digest does not match the last link")
	}
	if err := tx.Commit(); err != nil {
		return attestationChainSnapshot{}, xerrors.Errorf("commit attestation verify: %w", err)
	}
	return attestationChainSnapshot{
		Seq:       headSeq,
		Head:      headHex,
		LinkHeads: linkHeads,
		Present:   true,
	}, nil
}

func appendAttestationLink(ctx context.Context, tx *sql.Tx, event *model.Event, audit *model.CommandAudit) (*attestation.AnchorRecord, error) {
	if event == nil {
		return nil, xerrors.Errorf("event must not be nil")
	}
	enabled, err := tableExistsInTransaction(ctx, tx, "attestation_links")
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}

	createdAt := formatTimestamp(event.CreatedAt())
	kind := ""
	var content [32]byte
	switch {
	case audit != nil:
		kind = attestation.KindCommand
		content, err = attestation.CommandContentSHA256(attestation.CommandContent{
			EventID:     event.EventID().String(),
			CreatedAt:   createdAt,
			CommandText: []byte(audit.Command()),
			InputText:   []byte(audit.Input()),
		})
	case event.Kind() == types.EventKindPrompt:
		kind = attestation.KindPrompt
		content, err = attestation.PromptContentSHA256(attestation.PromptContent{
			EventID:   event.EventID().String(),
			CreatedAt: createdAt,
			Body:      []byte(event.Body()),
		})
	default:
		return nil, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("hash attestation content: %w", err)
	}

	var prevHex string
	var seq int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT head_sha256, seq FROM attestation_head WHERE singleton = 1`,
	).Scan(&prevHex, &seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, xerrors.Errorf("attestation head is missing")
		}
		return nil, xerrors.Errorf("read attestation head: %w", err)
	}
	prev, err := attestation.ParseHex(prevHex)
	if err != nil {
		return nil, xerrors.Errorf("parse attestation head: %w", err)
	}
	linkHex := attestation.EncodeHex(attestation.LinkSHA256(prev, content))
	contentHex := attestation.EncodeHex(content)
	nextSeq := seq + 1
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO attestation_links (
			seq, event_id, kind, content_sha256, prev_sha256, link_sha256, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		nextSeq,
		event.EventID().String(),
		kind,
		contentHex,
		prevHex,
		linkHex,
		createdAt,
	); err != nil {
		return nil, xerrors.Errorf("insert attestation link: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE attestation_head SET head_sha256 = ?, seq = ? WHERE singleton = 1`,
		linkHex,
		nextSeq,
	); err != nil {
		return nil, xerrors.Errorf("advance attestation head: %w", err)
	}
	return &attestation.AnchorRecord{
		Version:     attestation.AnchorFormatVersion,
		Seq:         nextSeq,
		Head:        linkHex,
		PublishedAt: createdAt,
	}, nil
}

func recomputeAttestationContent(ctx context.Context, q queryRowContexter, eventID, kind string) (string, error) {
	switch kind {
	case attestation.KindPrompt:
		createdAt, body, err := loadAttestedPromptPlaintext(ctx, q, eventID)
		if err != nil {
			return "", err
		}
		sum, err := attestation.PromptContentSHA256(attestation.PromptContent{
			EventID:   eventID,
			CreatedAt: createdAt,
			Body:      body,
		})
		if err != nil {
			return "", xerrors.Errorf("rehash attested prompt %s: %w", eventID, err)
		}
		return attestation.EncodeHex(sum), nil
	case attestation.KindCommand:
		createdAt, commandText, inputText, err := loadAttestedCommandPlaintext(ctx, q, eventID)
		if err != nil {
			return "", err
		}
		sum, err := attestation.CommandContentSHA256(attestation.CommandContent{
			EventID:     eventID,
			CreatedAt:   createdAt,
			CommandText: commandText,
			InputText:   inputText,
		})
		if err != nil {
			return "", xerrors.Errorf("rehash attested command %s: %w", eventID, err)
		}
		return attestation.EncodeHex(sum), nil
	default:
		return "", xerrors.Errorf("unknown attestation kind %q for event %s", kind, eventID)
	}
}

func loadAttestedPromptPlaintext(ctx context.Context, q queryRowContexter, eventID string) (string, []byte, error) {
	var createdAt string
	var payload payloadRow
	if err := q.QueryRowContext(ctx, `
		SELECT created_at, body, body_codec, body_format_version,
		       body_plaintext_bytes, body_encoded_bytes, body_sha256
		  FROM events
		 WHERE id = ?`, eventID,
	).Scan(append([]any{&createdAt}, payload.scanDestinations()...)...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, xerrors.Errorf("attested prompt %s is missing", eventID)
		}
		return "", nil, xerrors.Errorf("load attested prompt %s: %w", eventID, err)
	}
	plain, err := payload.decode(maxDecodedPayloadBytes)
	if err != nil {
		return "", nil, xerrors.Errorf("decode attested prompt %s: %w", eventID, annotatePayloadError(err, eventID, "body"))
	}
	return createdAt, plain, nil
}

func loadAttestedCommandPlaintext(ctx context.Context, q queryRowContexter, eventID string) (string, []byte, []byte, error) {
	var createdAt string
	var command payloadRow
	var input payloadRow
	if err := q.QueryRowContext(ctx, `
		SELECT e.created_at,
		       a.command_text, a.command_codec, a.command_format_version,
		       a.command_plaintext_bytes, a.command_encoded_bytes, a.command_sha256,
		       a.input_text, a.input_codec, a.input_format_version,
		       a.input_plaintext_bytes, a.input_encoded_bytes, a.input_sha256
		  FROM events AS e
		  JOIN command_audits AS a ON a.event_id = e.id
		 WHERE e.id = ?`, eventID,
	).Scan(append(append([]any{&createdAt}, command.scanDestinations()...), input.scanDestinations()...)...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, nil, xerrors.Errorf("attested command %s is missing", eventID)
		}
		return "", nil, nil, xerrors.Errorf("load attested command %s: %w", eventID, err)
	}
	commandPlain, err := command.decode(maxDecodedPayloadBytes)
	if err != nil {
		return "", nil, nil, xerrors.Errorf("decode attested command_text %s: %w", eventID, annotatePayloadError(err, eventID, "command_text"))
	}
	inputPlain, err := input.decode(maxDecodedPayloadBytes)
	if err != nil {
		return "", nil, nil, xerrors.Errorf("decode attested input_text %s: %w", eventID, annotatePayloadError(err, eventID, "input_text"))
	}
	return createdAt, commandPlain, inputPlain, nil
}
