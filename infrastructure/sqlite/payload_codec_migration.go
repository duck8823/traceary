// The migration-only decoder lives here so a 0.48 store can still be
// upgraded. It is unreachable from live read and write paths.

package sqlite

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

const (
	payloadFormatVersion         = 1
	payloadCodecIdentity         = "identity"
	payloadCodecZstd             = "zstd"
	maxDecodedPayloadBytes int64 = 16 << 20
	// zstd's MaxEncodedSize adds frame and block overhead to the plaintext.
	// Keep a bounded margin larger than that overhead for a 16 MiB frame while
	// retaining a hard allocation ceiling for corrupt physical values.
	maxStoredPayloadBytes int64 = maxDecodedPayloadBytes + (64 << 10)
)

// PayloadIntegrityError identifies corruption or an unsupported row without
// leaking payload contents.
type PayloadIntegrityError struct {
	Codec  string
	Reason string
	RowID  string
	Field  string
}

func (e *PayloadIntegrityError) Error() string {
	location := ""
	if e.RowID != "" {
		location = fmt.Sprintf(", row=%s, field=%s", e.RowID, e.Field)
	}
	return fmt.Sprintf("payload integrity error (codec=%s%s): %s", e.Codec, location, e.Reason)
}

type encodedPayload struct {
	Bytes          []byte
	Codec          string
	FormatVersion  int
	PlaintextBytes int64
	StoredBytes    int64
	SHA256         string
}

// payloadRow is the persisted payload contract used by the migration-only
// decoder and the v81 archive restore. Legacy rows have NULL metadata and
// are interpreted as identity.
type payloadRow struct {
	Stored         []byte
	Codec          sql.NullString
	FormatVersion  sql.NullInt64
	PlaintextBytes sql.NullInt64
	StoredBytes    sql.NullInt64
	SHA256         sql.NullString
}

func (r payloadRow) decode(limit int64) ([]byte, error) {
	metadataCount := 0
	for _, valid := range []bool{r.Codec.Valid, r.FormatVersion.Valid, r.PlaintextBytes.Valid, r.StoredBytes.Valid, r.SHA256.Valid} {
		if valid {
			metadataCount++
		}
	}
	if metadataCount == 0 {
		if int64(len(r.Stored)) > limit {
			return nil, &PayloadIntegrityError{Codec: payloadCodecIdentity, Reason: "decoded length exceeds limit"}
		}
		return bytes.Clone(r.Stored), nil
	}
	if metadataCount != 5 {
		codec := r.Codec.String
		if !r.Codec.Valid {
			codec = "unknown"
		}
		return nil, &PayloadIntegrityError{Codec: codec, Reason: "incomplete metadata"}
	}
	if r.StoredBytes.Int64 != int64(len(r.Stored)) {
		return nil, &PayloadIntegrityError{Codec: r.Codec.String, Reason: "stored length mismatch"}
	}
	return decodePayload(r.Stored, r.Codec.String, int(r.FormatVersion.Int64), r.PlaintextBytes.Int64, r.SHA256.String, limit)
}

func decodePayload(stored []byte, codec string, formatVersion int, plaintextBytes int64, checksum string, limit int64) ([]byte, error) {
	if formatVersion != payloadFormatVersion {
		return nil, &PayloadIntegrityError{Codec: codec, Reason: "unsupported format version"}
	}
	if limit < 0 || plaintextBytes < 0 || plaintextBytes > limit {
		return nil, &PayloadIntegrityError{Codec: codec, Reason: "decoded length exceeds limit"}
	}
	var reader io.Reader
	switch codec {
	case payloadCodecIdentity:
		reader = bytes.NewReader(stored)
	case payloadCodecZstd:
		decoder, err := zstd.NewReader(bytes.NewReader(stored), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(uint64(limit)+1))
		if err != nil {
			return nil, &PayloadIntegrityError{Codec: codec, Reason: "invalid compressed stream"}
		}
		defer decoder.Close()
		reader = decoder
	default:
		return nil, &PayloadIntegrityError{Codec: codec, Reason: "unsupported codec"}
	}
	bounded := io.LimitReader(reader, limit+1)
	decoded, err := io.ReadAll(bounded)
	if err != nil {
		return nil, &PayloadIntegrityError{Codec: codec, Reason: "decode failed"}
	}
	if int64(len(decoded)) > limit {
		return nil, &PayloadIntegrityError{Codec: codec, Reason: "decoded length exceeds limit"}
	}
	if int64(len(decoded)) != plaintextBytes {
		return nil, &PayloadIntegrityError{Codec: codec, Reason: "plaintext length mismatch"}
	}
	sum := sha256.Sum256(decoded)
	expected, err := hex.DecodeString(checksum)
	if err != nil || !bytes.Equal(sum[:], expected) {
		return nil, &PayloadIntegrityError{Codec: codec, Reason: "checksum mismatch"}
	}
	return decoded, nil
}

// storedBodyArg preserves the column affinity each codec is stored with:
// identity as TEXT, zstd as BLOB. Kept for v81 archive restore.
func storedBodyArg(payload encodedPayload) any {
	if payload.Codec == payloadCodecIdentity {
		return string(payload.Bytes)
	}
	return payload.Bytes
}
