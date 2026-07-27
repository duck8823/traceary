package usecase

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	memoryHygieneCursorVersion        = 2
	maxMemoryHygieneCursorLength      = 4096
	memoryHygieneCursorAdditionalData = "traceary:memory-hygiene-cursor:v2"
)

type memoryHygieneCursorPayload struct {
	Version           int                                     `json:"v"`
	Revision          int64                                   `json:"r"`
	CriteriaDigest    string                                  `json:"c"`
	ScanAt            string                                  `json:"at"`
	Phase             apptypes.MemoryHygieneScanPhase         `json:"p"`
	Keyset            apptypes.MemoryHygieneScanKeyset        `json:"k"`
	Consistency       apptypes.MemoryHygieneScanConsistency   `json:"s"`
	ConsistencyReason apptypes.MemoryHygieneConsistencyReason `json:"sr,omitempty"`
}

var loadMemoryHygieneCursorAEAD = sync.OnceValues(func() (cipher.AEAD, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, xerrors.Errorf("failed to generate memory hygiene cursor key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, xerrors.Errorf("failed to initialize memory hygiene cursor cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, xerrors.Errorf("failed to initialize memory hygiene cursor authentication: %w", err)
	}
	return aead, nil
})

func encodeMemoryHygieneCursor(payload memoryHygieneCursorPayload) (string, error) {
	payload.Version = memoryHygieneCursorVersion
	aead, err := loadMemoryHygieneCursorAEAD()
	if err != nil {
		return "", err
	}
	return encodeMemoryHygieneCursorWithAEAD(payload, aead)
}

func encodeMemoryHygieneCursorWithAEAD(payload memoryHygieneCursorPayload, aead cipher.AEAD) (string, error) {
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", xerrors.Errorf("failed to encode memory hygiene cursor")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", xerrors.Errorf("failed to generate memory hygiene cursor nonce: %w", err)
	}
	encoded := aead.Seal(nonce, nonce, plaintext, []byte(memoryHygieneCursorAdditionalData))
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeMemoryHygieneCursor(encoded string) (memoryHygieneCursorPayload, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" || len(encoded) > maxMemoryHygieneCursorLength {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	aead, err := loadMemoryHygieneCursorAEAD()
	if err != nil {
		return memoryHygieneCursorPayload{}, err
	}
	return decodeMemoryHygieneCursorWithAEAD(raw, aead)
}

func decodeMemoryHygieneCursorWithAEAD(raw []byte, aead cipher.AEAD) (memoryHygieneCursorPayload, error) {
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	nonce := raw[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, raw[aead.NonceSize():], []byte(memoryHygieneCursorAdditionalData))
	if err != nil {
		return memoryHygieneCursorPayload{}, xerrors.Errorf(
			"%w: cursor cannot be authenticated; start a new scan because it may have been modified or issued before this server or CLI process restarted",
			queryservice.ErrMemoryHygieneRescanRequired,
		)
	}
	var payload memoryHygieneCursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(plaintext)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	if payload.Version != memoryHygieneCursorVersion ||
		payload.Revision < 0 ||
		!payload.Phase.IsKnown() ||
		!validMemoryHygieneCriteriaDigest(payload.CriteriaDigest) ||
		!validMemoryHygieneCursorKeyset(payload.Phase, payload.Keyset) ||
		!validMemoryHygieneCursorConsistency(payload.Consistency, payload.ConsistencyReason) {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("unsupported memory hygiene cursor")
	}
	scanAt, err := time.Parse(time.RFC3339Nano, payload.ScanAt)
	if err != nil || scanAt.IsZero() {
		return memoryHygieneCursorPayload{}, xerrors.Errorf("invalid memory hygiene cursor")
	}
	return payload, nil
}

func validMemoryHygieneCursorConsistency(
	consistency apptypes.MemoryHygieneScanConsistency,
	reason apptypes.MemoryHygieneConsistencyReason,
) bool {
	switch consistency {
	case apptypes.MemoryHygieneScanConsistencyConsistent:
		return reason == ""
	case apptypes.MemoryHygieneScanConsistencyBestEffort:
		return reason == apptypes.MemoryHygieneConsistencyReasonRevisionChanged
	default:
		return false
	}
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
