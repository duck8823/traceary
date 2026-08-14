package attestation

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"golang.org/x/xerrors"
)

const (
	// AnchorFormatVersion is the JSONL record version.
	AnchorFormatVersion = 1
	// AnchorFileSuffix is appended to the store path.
	AnchorFileSuffix = ".attest"
)

// AnchorRecord is one published head. The file is a log of these records.
type AnchorRecord struct {
	Version     int    `json:"v"`
	Seq         int64  `json:"seq"`
	Head        string `json:"head"`
	PublishedAt string `json:"at"`
}

// AnchorRelation is how a file's last record compares to the store head.
type AnchorRelation string

const (
	// AnchorMatches means the last file record is the store head.
	AnchorMatches AnchorRelation = "matches"
	// AnchorMissing means the file is absent or empty.
	AnchorMissing AnchorRelation = "missing"
	// AnchorBehind means the store has advanced past the file.
	AnchorBehind AnchorRelation = "behind"
	// AnchorMismatch means the same seq has a different head.
	AnchorMismatch AnchorRelation = "mismatch"
	// AnchorAhead means the file names a seq the store does not have.
	AnchorAhead AnchorRelation = "ahead"
)

// FormatAnchorLine encodes one record as a single JSON line without a newline.
func FormatAnchorLine(record AnchorRecord) ([]byte, error) {
	if record.Version == 0 {
		record.Version = AnchorFormatVersion
	}
	if record.Version != AnchorFormatVersion {
		return nil, xerrors.Errorf("unsupported attestation anchor version %d", record.Version)
	}
	if err := validateAnchorRecord(record); err != nil {
		return nil, err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return nil, xerrors.Errorf("encode attestation anchor: %w", err)
	}
	return body, nil
}

// ParseAnchorLine decodes one JSON line.
func ParseAnchorLine(line []byte) (AnchorRecord, error) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return AnchorRecord{}, xerrors.Errorf("attestation anchor line is empty")
	}
	var record AnchorRecord
	if err := json.Unmarshal(trimmed, &record); err != nil {
		return AnchorRecord{}, xerrors.Errorf("decode attestation anchor: %w", err)
	}
	if record.Version != AnchorFormatVersion {
		return AnchorRecord{}, xerrors.Errorf("unsupported attestation anchor version %d", record.Version)
	}
	if err := validateAnchorRecord(record); err != nil {
		return AnchorRecord{}, err
	}
	return record, nil
}

// ParseAnchorFile decodes a whole JSONL buffer.
func ParseAnchorFile(body []byte) ([]AnchorRecord, error) {
	lines := bytes.Split(body, []byte("\n"))
	records := make([]AnchorRecord, 0, len(lines))
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		record, err := ParseAnchorLine(line)
		if err != nil {
			return nil, xerrors.Errorf("attestation anchor line %d: %w", i+1, err)
		}
		records = append(records, record)
	}
	if err := CheckAnchorHistory(records); err != nil {
		return nil, err
	}
	return records, nil
}

// CheckAnchorHistory requires non-decreasing seq and identical heads for a
// repeated seq. Gaps are allowed: a missed publish can skip intermediate seqs.
func CheckAnchorHistory(records []AnchorRecord) error {
	for i := 1; i < len(records); i++ {
		prev, rec := records[i-1], records[i]
		if rec.Seq < prev.Seq {
			return xerrors.Errorf("attestation anchor seq moved backward from %d to %d", prev.Seq, rec.Seq)
		}
		if rec.Seq == prev.Seq && rec.Head != prev.Head {
			return xerrors.Errorf("attestation anchor seq %d has conflicting heads", rec.Seq)
		}
	}
	return nil
}

// RelateAnchor compares the last published record to the store head.
func RelateAnchor(storeSeq int64, storeHead string, last AnchorRecord, present bool) AnchorRelation {
	if !present {
		return AnchorMissing
	}
	if last.Seq < storeSeq {
		return AnchorBehind
	}
	if last.Seq > storeSeq {
		return AnchorAhead
	}
	if !strings.EqualFold(last.Head, storeHead) {
		return AnchorMismatch
	}
	return AnchorMatches
}

// DecideAnchorAppend says whether next should be appended after last.
// Same seq and same head is a no-op. A backward seq or a same-seq
// conflicting head is corrupt and must not be written.
func DecideAnchorAppend(last AnchorRecord, present bool, next AnchorRecord) (bool, error) {
	if err := validateAnchorRecord(next); err != nil {
		return false, err
	}
	if !present {
		return true, nil
	}
	if next.Seq < last.Seq {
		return false, xerrors.Errorf("attestation anchor file is ahead of store (file_seq=%d store_seq=%d)", last.Seq, next.Seq)
	}
	if next.Seq == last.Seq {
		if !strings.EqualFold(last.Head, next.Head) {
			return false, xerrors.Errorf("attestation anchor seq %d has conflicting heads", next.Seq)
		}
		return false, nil
	}
	return true, nil
}

func validateAnchorRecord(record AnchorRecord) error {
	if record.Seq < 0 {
		return xerrors.Errorf("attestation anchor seq must not be negative")
	}
	if _, err := ParseHex(record.Head); err != nil {
		return xerrors.Errorf("attestation anchor head: %w", err)
	}
	if strings.TrimSpace(record.PublishedAt) == "" {
		return xerrors.Errorf("attestation anchor timestamp must not be empty")
	}
	return nil
}

// FormatSeq is a stable decimal form for messages.
func FormatSeq(seq int64) string {
	return strconv.FormatInt(seq, 10)
}
