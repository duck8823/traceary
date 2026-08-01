package sqlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const payloadEnvelopePrefix = "traceary-payload-v1:"

const (
	payloadFormatVersion         = 1
	payloadCodecIdentity         = "identity"
	payloadCodecZstd             = "zstd"
	maxDecodedPayloadBytes int64 = 16 << 20
)

// PayloadIntegrityError identifies corruption or an unsupported row without
// leaking payload contents. Callers may continue serving body-free metadata.
type PayloadIntegrityError struct {
	Codec  string
	Reason string
}

func (e *PayloadIntegrityError) Error() string {
	return fmt.Sprintf("payload integrity error (codec=%s): %s", e.Codec, e.Reason)
}

type encodedPayload struct {
	Bytes          []byte
	Codec          string
	FormatVersion  int
	PlaintextBytes int64
	StoredBytes    int64
	SHA256         string
}

type payloadEnvelope struct {
	Codec          string `json:"codec"`
	FormatVersion  int    `json:"format_version"`
	PlaintextBytes int64  `json:"plaintext_bytes"`
	SHA256         string `json:"sha256"`
	Stored         []byte `json:"stored"`
}

// marshalPayloadEnvelope is reserved for zstd shadow rows in v0.34. Identity
// canonical rows deliberately remain plain text for downgrade-safe rollout.
func marshalPayloadEnvelope(payload encodedPayload) (string, error) {
	data, err := json.Marshal(payloadEnvelope{Codec: payload.Codec, FormatVersion: payload.FormatVersion, PlaintextBytes: payload.PlaintextBytes, SHA256: payload.SHA256, Stored: payload.Bytes})
	if err != nil {
		return "", fmt.Errorf("marshal payload envelope: %w", err)
	}
	return payloadEnvelopePrefix + string(data), nil
}

// decodeStoredPayload accepts both legacy/plain identity text and the
// self-describing shadow envelope, allowing existing query shapes to hydrate
// mixed rows through one bounded adapter.
func decodeStoredPayload(stored string, limit int64) (string, error) {
	if !strings.HasPrefix(stored, payloadEnvelopePrefix) {
		return stored, nil
	}
	var envelope payloadEnvelope
	if err := json.Unmarshal([]byte(strings.TrimPrefix(stored, payloadEnvelopePrefix)), &envelope); err != nil {
		return "", &PayloadIntegrityError{Codec: "unknown", Reason: "invalid envelope"}
	}
	decoded, err := decodePayload(envelope.Stored, envelope.Codec, envelope.FormatVersion, envelope.PlaintextBytes, envelope.SHA256, limit)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func encodePayload(plaintext []byte, codec string) (encodedPayload, error) {
	result := encodedPayload{Codec: codec, FormatVersion: payloadFormatVersion, PlaintextBytes: int64(len(plaintext))}
	sum := sha256.Sum256(plaintext)
	result.SHA256 = hex.EncodeToString(sum[:])
	switch codec {
	case payloadCodecIdentity:
		result.Bytes = bytes.Clone(plaintext)
	case payloadCodecZstd:
		encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		if err != nil {
			return encodedPayload{}, fmt.Errorf("create zstd encoder: %w", err)
		}
		result.Bytes = encoder.EncodeAll(plaintext, nil)
		if err := encoder.Close(); err != nil {
			return encodedPayload{}, fmt.Errorf("close zstd encoder: %w", err)
		}
	default:
		return encodedPayload{}, &PayloadIntegrityError{Codec: codec, Reason: "unsupported codec"}
	}
	result.StoredBytes = int64(len(result.Bytes))
	return result, nil
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

func isPayloadIntegrityError(err error) bool {
	var target *PayloadIntegrityError
	return errors.As(err, &target)
}
