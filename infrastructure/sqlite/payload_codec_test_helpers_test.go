package sqlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/klauspost/compress/zstd"
)

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

func compressibleBody(tag string) string {
	return tag + " " + string(bytes.Repeat([]byte("redacted synthetic payload "), 128))
}
