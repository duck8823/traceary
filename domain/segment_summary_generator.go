package domain

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/bits"
	"sort"
	"time"
)

// ErrSegmentSummaryGeneration rejects invalid input or a configured cap.
var ErrSegmentSummaryGeneration = errors.New("segment summary generation failed")

// SegmentSummaryHMACV1 identifies the fixed token derivation algorithm.
const SegmentSummaryHMACV1 uint32 = 1

const (
	segmentSummaryGeneratorMaxUnits          = 100_000
	segmentSummaryGeneratorMaxDistinct       = 100_000
	segmentSummaryGeneratorMaxSessions       = 100_000
	segmentSummaryReaderMaxRows              = 100_000
	segmentSummaryReaderMaxBytes       int64 = 64 << 20
)

// SegmentSummaryGeneratorConfig fixes all output-affecting bounds.
type SegmentSummaryGeneratorConfig struct {
	FilterKeyID         string
	HMACVersion         uint32
	MaxUnits            int
	MaxDistinctPerKind  int
	MaxSessions         int
	BloomBitCount       uint32
	BloomHashCount      uint8
	BloomMaxSetPermille uint16
}

func (c SegmentSummaryGeneratorConfig) valid() bool {
	return c.FilterKeyID != "" && len(c.FilterKeyID) <= 255 && c.HMACVersion == SegmentSummaryHMACV1 && c.MaxUnits > 0 && c.MaxUnits <= segmentSummaryGeneratorMaxUnits &&
		c.MaxDistinctPerKind > 0 && c.MaxDistinctPerKind <= segmentSummaryGeneratorMaxDistinct && c.MaxSessions > 0 && c.MaxSessions <= segmentSummaryGeneratorMaxSessions && c.BloomBitCount >= 8 && c.BloomBitCount <= SegmentSummaryBloomMaxBitsV1 && c.BloomBitCount%8 == 0 &&
		c.BloomHashCount > 0 && c.BloomHashCount <= 16 && c.BloomMaxSetPermille > 0 && c.BloomMaxSetPermille < 1000
}

// GenerateSegmentCatalogSummaryV1 reads only fixed event metadata fields. The
// caller-owned key is used transiently and is not representable in the result.
func GenerateSegmentCatalogSummaryV1(units []HistoryUnit, key []byte, cfg SegmentSummaryGeneratorConfig) (SegmentCatalogSummaryV1, error) {
	if !cfg.valid() || len(key) < 16 || len(units) == 0 || len(units) > cfg.MaxUnits {
		return SegmentCatalogSummaryV1{}, ErrSegmentSummaryGeneration
	}
	tokens := make(map[SummaryTokenKind]map[[sha256.Size]byte]struct{}, int(SummaryTokenEventKind))
	sessions := make(map[[sha256.Size]byte]SegmentSessionAggregateV1)
	completeTime := true
	for _, unit := range units {
		if unit.Sequence == 0 || unit.CreatedAt().IsZero() {
			completeTime = false
		}
		values := unit.Event.values
		metadata := [...]struct {
			kind  SummaryTokenKind
			index int
		}{{SummaryTokenWorkspace, 7}, {SummaryTokenSession, 3}, {SummaryTokenClient, 6}, {SummaryTokenAgent, 2}, {SummaryTokenEventKind, 1}}
		for _, field := range metadata {
			value := values[field.index]
			if value.Class == SQLiteNull || ((value.Class == SQLiteText || value.Class == SQLiteBlob) && len(value.Bytes) == 0) {
				continue
			}
			if value.Class != SQLiteText && value.Class != SQLiteBlob {
				return SegmentCatalogSummaryV1{}, ErrSegmentSummaryGeneration
			}
			token := summaryHMAC(key, field.kind, value.Bytes)
			if tokens[field.kind] == nil {
				tokens[field.kind] = make(map[[sha256.Size]byte]struct{})
			}
			tokens[field.kind][token] = struct{}{}
			if field.kind == SummaryTokenSession {
				agg := sessions[token]
				agg.SessionToken = token
				agg.UnitCount++
				if unit.Audit != nil {
					agg.AuditCount++
				}
				sessions[token] = agg
			}
		}
	}
	result := SegmentCatalogSummaryV1{FilterKeyID: cfg.FilterKeyID, TimeComplete: completeTime}
	for kind := SummaryTokenWorkspace; kind <= SummaryTokenEventKind; kind++ {
		set := tokens[kind]
		ordered := make([][sha256.Size]byte, 0, len(set))
		for token := range set {
			ordered = append(ordered, token)
		}
		sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i][:], ordered[j][:]) < 0 })
		if len(ordered) <= cfg.MaxDistinctPerKind {
			for _, token := range ordered {
				result.ExactTokens = append(result.ExactTokens, SegmentSummaryToken{Kind: kind, Value: token})
			}
		}
		bloom := SegmentBloomV1{Kind: kind, BitCount: cfg.BloomBitCount, HashCount: cfg.BloomHashCount, Bits: make([]byte, cfg.BloomBitCount/8)}
		for _, token := range ordered {
			bloomAdd(&bloom, token)
		}
		setBits := 0
		for _, value := range bloom.Bits {
			setBits += bits.OnesCount8(value)
		}
		// A saturated Bloom is omitted. Its absence is explicit unknown evidence.
		if uint64(setBits)*1000 <= uint64(cfg.BloomMaxSetPermille)*uint64(cfg.BloomBitCount) {
			result.Blooms = append(result.Blooms, bloom)
		}
	}
	if len(sessions) <= cfg.MaxSessions {
		for _, aggregate := range sessions {
			result.Sessions = append(result.Sessions, aggregate)
		}
	}
	return result, nil
}

// SegmentSummaryPredicateOperator is the fixed conservative predicate set.
type SegmentSummaryPredicateOperator uint8

const (
	// SegmentSummaryPredicateEqual is the only predicate that may exclude.
	SegmentSummaryPredicateEqual SegmentSummaryPredicateOperator = iota + 1
	// SegmentSummaryPredicateNotEqual always degrades to unknown.
	SegmentSummaryPredicateNotEqual
	// SegmentSummaryPredicateContains always degrades to unknown.
	SegmentSummaryPredicateContains
)

// SegmentSummaryPredicate is one metadata predicate without stored tokens.
type SegmentSummaryPredicate struct {
	Kind     SummaryTokenKind
	Operator SegmentSummaryPredicateOperator
	Value    []byte
}

// SegmentSummaryMayMatch returns false only for a complete Bloom miss. Exact
// hits are a fast positive; every unsupported or incomplete case fails open.
func SegmentSummaryMayMatch(summary SegmentCatalogSummaryV1, expectedKeyID string, key []byte, predicate SegmentSummaryPredicate) bool {
	if len(summary.ExactTokens)+len(summary.Blooms)+len(summary.Sessions) > segmentSummaryReaderMaxRows {
		return true
	}
	if _, err := summary.CanonicalBytes(segmentSummaryReaderMaxBytes); err != nil {
		return true
	}
	if summary.FilterKeyID == "" || summary.FilterKeyID != expectedKeyID || len(key) < 16 || predicate.Operator != SegmentSummaryPredicateEqual ||
		predicate.Kind < SummaryTokenWorkspace || predicate.Kind > SummaryTokenEventKind || len(predicate.Value) < 3 {
		return true
	}
	token := summaryHMAC(key, predicate.Kind, predicate.Value)
	for _, exact := range summary.ExactTokens {
		if exact.Kind == predicate.Kind && hmac.Equal(exact.Value[:], token[:]) {
			return true
		}
	}
	for _, bloom := range summary.Blooms {
		if bloom.Kind == predicate.Kind {
			if bloom.BitCount == 0 || bloom.HashCount == 0 || uint64(len(bloom.Bits))*8 != uint64(bloom.BitCount) {
				return true
			}
			return bloomMayContain(bloom, token)
		}
	}
	return true
}

// SegmentCatalogCandidateMayMatch applies the time envelope first and then
// supported positive metadata predicates. Invalid query bounds and all unknown
// metadata evidence fail open; a disjoint complete time envelope may exclude.
func SegmentCatalogCandidateMayMatch(summary SegmentCatalogSummaryV1, segmentMin, segmentMax, queryStart, queryEnd time.Time, expectedKeyID string, key []byte, predicates []SegmentSummaryPredicate) bool {
	if queryStart.IsZero() || queryEnd.IsZero() || queryEnd.Before(queryStart) {
		return true
	}
	if !summary.TimeFilterMayMatch(segmentMin, segmentMax, queryStart, queryEnd) {
		return false
	}
	for _, predicate := range predicates {
		if !SegmentSummaryMayMatch(summary, expectedKeyID, key, predicate) {
			return false
		}
	}
	return true
}

func summaryHMAC(key []byte, kind SummaryTokenKind, value []byte) [sha256.Size]byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte("traceary/segment-summary-token/v1\x00"))
	_, _ = h.Write([]byte{byte(kind)})
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(value)
	var result [sha256.Size]byte
	copy(result[:], h.Sum(nil))
	return result
}

func bloomLocations(token [sha256.Size]byte, bitCount uint32, hashCount uint8) []uint32 {
	a := binary.BigEndian.Uint64(token[:8])
	b := binary.BigEndian.Uint64(token[8:16]) | 1
	result := make([]uint32, hashCount)
	for i := range result {
		result[i] = uint32((a + uint64(i)*b) % uint64(bitCount))
	}
	return result
}
func bloomAdd(bloom *SegmentBloomV1, token [sha256.Size]byte) {
	for _, location := range bloomLocations(token, bloom.BitCount, bloom.HashCount) {
		bloom.Bits[location/8] |= byte(1 << (location % 8))
	}
}
func bloomMayContain(bloom SegmentBloomV1, token [sha256.Size]byte) bool {
	for _, location := range bloomLocations(token, bloom.BitCount, bloom.HashCount) {
		if bloom.Bits[location/8]&byte(1<<(location%8)) == 0 {
			return false
		}
	}
	return true
}
