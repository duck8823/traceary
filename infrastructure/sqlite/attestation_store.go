package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/attestation"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// VerifyAttestationChain walks the stored hash chain and recomputes each
// content digest from canonical rows. It is the shipped verifier for tests
// and for #1678; store open does not call it.
//
// A store that predates migration 61 (no tables) is treated as an empty
// chain. Historical events without a link do not fail verification.
func VerifyAttestationChain(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return xerrors.Errorf("attestation verify requires a database")
	}
	enabled, err := databaseTableExists(ctx, db, "attestation_links")
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	hasHead, err := databaseTableExists(ctx, db, "attestation_head")
	if err != nil {
		return err
	}
	if !hasHead {
		return nil
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return xerrors.Errorf("begin attestation verify: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var headHex string
	var headSeq int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT head_sha256, seq FROM attestation_head WHERE singleton = 1`,
	).Scan(&headHex, &headSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return xerrors.Errorf("attestation head is missing")
		}
		return xerrors.Errorf("read attestation head: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT seq, event_id, kind, content_sha256, prev_sha256, link_sha256
		  FROM attestation_links
		 ORDER BY seq`)
	if err != nil {
		return xerrors.Errorf("list attestation links: %w", err)
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
			return xerrors.Errorf("scan attestation link: %w", err)
		}
		links = append(links, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return xerrors.Errorf("iterate attestation links: %w", err)
	}
	if err := rows.Close(); err != nil {
		return xerrors.Errorf("close attestation links: %w", err)
	}

	prevHex := attestation.GenesisHex()
	for i, item := range links {
		count := int64(i + 1)
		if item.seq != count {
			return xerrors.Errorf("attestation link seq = %d, want %d", item.seq, count)
		}
		if item.prevHex != prevHex {
			return xerrors.Errorf("attestation link %d predecessor mismatch", item.seq)
		}
		recomputedContent, err := recomputeAttestationContent(ctx, tx, item.eventID, item.kind)
		if err != nil {
			return err
		}
		if item.contentHex != recomputedContent {
			return xerrors.Errorf("attestation link %d content digest does not match store", item.seq)
		}
		prev, err := attestation.ParseHex(prevHex)
		if err != nil {
			return xerrors.Errorf("parse attestation predecessor: %w", err)
		}
		content, err := attestation.ParseHex(item.contentHex)
		if err != nil {
			return xerrors.Errorf("parse attestation content digest: %w", err)
		}
		wantLink := attestation.EncodeHex(attestation.LinkSHA256(prev, content))
		if item.linkHex != wantLink {
			return xerrors.Errorf("attestation link %d digest does not match predecessor and content", item.seq)
		}
		prevHex = item.linkHex
	}
	if int64(len(links)) != headSeq {
		return xerrors.Errorf("attestation head seq = %d, want %d", headSeq, len(links))
	}
	if headHex != prevHex {
		return xerrors.Errorf("attestation head digest does not match the last link")
	}
	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("commit attestation verify: %w", err)
	}
	return nil
}

func appendAttestationLink(ctx context.Context, tx *sql.Tx, event *model.Event, audit *model.CommandAudit) error {
	if event == nil {
		return xerrors.Errorf("event must not be nil")
	}
	enabled, err := tableExistsInTransaction(ctx, tx, "attestation_links")
	if err != nil {
		return err
	}
	if !enabled {
		return nil
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
		return nil
	}
	if err != nil {
		return xerrors.Errorf("hash attestation content: %w", err)
	}

	var prevHex string
	var seq int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT head_sha256, seq FROM attestation_head WHERE singleton = 1`,
	).Scan(&prevHex, &seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return xerrors.Errorf("attestation head is missing")
		}
		return xerrors.Errorf("read attestation head: %w", err)
	}
	prev, err := attestation.ParseHex(prevHex)
	if err != nil {
		return xerrors.Errorf("parse attestation head: %w", err)
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
		return xerrors.Errorf("insert attestation link: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE attestation_head SET head_sha256 = ?, seq = ? WHERE singleton = 1`,
		linkHex,
		nextSeq,
	); err != nil {
		return xerrors.Errorf("advance attestation head: %w", err)
	}
	return nil
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

func eventIsAttested(ctx context.Context, tx *sql.Tx, eventID string) (bool, error) {
	enabled, err := tableExistsInTransaction(ctx, tx, "attestation_links")
	if err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	var exists int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM attestation_links WHERE event_id = ?)`,
		eventID,
	).Scan(&exists); err != nil {
		return false, xerrors.Errorf("inspect attestation link for %s: %w", eventID, err)
	}
	return exists != 0, nil
}
