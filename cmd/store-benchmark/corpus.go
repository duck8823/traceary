package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const calibrateSeed int64 = 1811

// corpusKind isolates one dimension of the #1620 storage-gate figures.
type corpusKind string

const (
	corpusTiny       corpusKind = "tiny"
	corpusEnormous   corpusKind = "enormous"
	corpusCJK        corpusKind = "cjk"
	corpusEntropy    corpusKind = "entropy"
	corpusRepetitive corpusKind = "repetitive"
)

func allCorpusKinds() []corpusKind {
	return []corpusKind{corpusTiny, corpusEnormous, corpusCJK, corpusEntropy, corpusRepetitive}
}

func corpusBody(kind corpusKind, seed int64, index int, enormousBytes int) string {
	switch kind {
	case corpusTiny:
		return fmt.Sprintf("t%08d", index)
	case corpusEnormous:
		if enormousBytes < 1 {
			enormousBytes = 1 << 20
		}
		return strings.Repeat("E", enormousBytes)
	case corpusCJK:
		return strings.Repeat("合成コーパス用の反復テキスト。", 8)
	case corpusEntropy:
		sum := sha256.Sum256([]byte(strconv.FormatInt(seed, 10) + ":" + strconv.Itoa(index)))
		return hex.EncodeToString(sum[:]) + hex.EncodeToString(sum[:])
	case corpusRepetitive:
		return strings.Repeat("redacted synthetic payload ", 32)
	default:
		return ""
	}
}
