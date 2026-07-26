package usecase

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	memoryHygieneCursorVersion   = 1
	maxMemoryHygieneCursorLength = 4096
)

type memoryHygieneCursorPayload struct {
	Version        int                              `json:"v"`
	Revision       int64                            `json:"r"`
	CriteriaDigest string                           `json:"c"`
	ScanAt         string                           `json:"at"`
	Phase          apptypes.MemoryHygieneScanPhase  `json:"p"`
	Keyset         apptypes.MemoryHygieneScanKeyset `json:"k"`
}

type memoryHygieneCursorEnvelope struct {
	Payload  memoryHygieneCursorPayload `json:"payload"`
	Checksum string                     `json:"checksum"`
}

func encodeMemoryHygieneCursor(payload memoryHygieneCursorPayload) (string, error) {
	payload.Version = memoryHygieneCursorVersion
	checksum, err := memoryHygieneCursorChecksum(payload)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(memoryHygieneCursorEnvelope{Payload: payload, Checksum: checksum})
	if err != nil {
		return "", xerrors.Errorf("failed to encode memory hygiene cursor")
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeMemoryHygieneCursor(encoded string) (memoryHygieneCursorPayload, error) {
	if encoded == "" || len(encoded) > maxMemoryHygieneCursorLength {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	var envelope memoryHygieneCursorEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	payload := envelope.Payload
	if payload.Version != memoryHygieneCursorVersion ||
		payload.Revision < 0 ||
		!payload.Phase.IsKnown() ||
		!validMemoryHygieneCriteriaDigest(payload.CriteriaDigest) ||
		!validMemoryHygieneCursorKeyset(payload.Phase, payload.Keyset) {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("unsupported memory hygiene cursor")
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.ScanAt); err != nil {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	wantChecksum, err := memoryHygieneCursorChecksum(payload)
	if err != nil || subtle.ConstantTimeCompare([]byte(envelope.Checksum), []byte(wantChecksum)) != 1 {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor checksum")
	}
	return payload, nil
}

func validMemoryHygieneCriteriaDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validMemoryHygieneCursorKeyset(
	phase apptypes.MemoryHygieneScanPhase,
	keyset apptypes.MemoryHygieneScanKeyset,
) bool {
	for _, value := range []string{keyset.AfterMemoryID, keyset.AnchorMemoryID, keyset.AfterPartnerID} {
		if value == "" {
			continue
		}
		if _, err := domtypes.MemoryIDFrom(value); err != nil {
			return false
		}
	}
	switch phase {
	case apptypes.MemoryHygieneScanPhaseAcceptedRows,
		apptypes.MemoryHygieneScanPhaseExactDuplicates,
		apptypes.MemoryHygieneScanPhaseCandidateRows:
		return keyset.AnchorMemoryID == "" && keyset.AfterPartnerID == ""
	case apptypes.MemoryHygieneScanPhaseSimilarityPairs:
		if keyset.AnchorMemoryID == "" {
			return keyset.AfterPartnerID == ""
		}
		if keyset.AfterPartnerID == "" || keyset.AnchorMemoryID >= keyset.AfterPartnerID {
			return false
		}
		return keyset.AfterMemoryID == "" || keyset.AfterMemoryID < keyset.AnchorMemoryID
	default:
		return false
	}
}

func memoryHygieneCursorChecksum(payload memoryHygieneCursorPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", xerrors.Errorf("failed to checksum memory hygiene cursor")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16]), nil
}

func memoryHygieneCriteriaDigest(
	scopes []domtypes.MemoryScope,
	staleness time.Duration,
	similarity float64,
	includeHidden bool,
) string {
	normalizedScopes := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope == nil {
			continue
		}
		normalizedScopes = append(normalizedScopes, scope.Kind().String()+"\x00"+scope.Key())
	}
	sort.Strings(normalizedScopes)
	payload := struct {
		Scopes        []string `json:"scopes"`
		Staleness     int64    `json:"staleness_ns"`
		Similarity    string   `json:"similarity"`
		IncludeHidden bool     `json:"include_hidden"`
	}{
		Scopes:        normalizedScopes,
		Staleness:     int64(staleness),
		Similarity:    strconv.FormatFloat(similarity, 'g', -1, 64),
		IncludeHidden: includeHidden,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
