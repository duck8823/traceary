package runtime_test

import (
	"bytes"
	"errors"
	"math"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestByteLogExactWrapGapAndDefensiveCopies(t *testing.T) {
	log, err := domain.NewByteLog(8)
	if err != nil {
		t.Fatal(err)
	}
	source := []byte{0, 1, 2, 3, 4}
	if _, err = log.Append(source); err != nil {
		t.Fatal(err)
	}
	source[0] = 99
	if _, err = log.Append([]byte{5, 6, 7, 8, 9}); err != nil {
		t.Fatal(err)
	}
	state := log.Snapshot()
	if state.Base != 2 || state.Head != 10 || state.Retained != 8 {
		t.Fatal("ring bound or offsets incorrect")
	}
	gap, err := log.Read(0, 4)
	if err != nil || !gap.Gap || gap.Start != 0 || gap.End != 2 || len(gap.Data) != 0 {
		t.Fatalf("missing exact eviction gap: %v", err)
	}
	first, err := log.Read(2, 4)
	if err != nil || !bytes.Equal(first.Data, []byte{2, 3, 4, 5}) || first.End != 6 {
		t.Fatal("wrapped byte order incorrect")
	}
	first.Data[0] = 99
	again, _ := log.Read(2, 4)
	if again.Data[0] != 2 {
		t.Fatal("reader mutated retention")
	}
	last, err := log.Read(6, 8)
	if err != nil || !bytes.Equal(last.Data, []byte{6, 7, 8, 9}) || last.End != 10 {
		t.Fatal("tail byte sequence incorrect")
	}
	if _, err = log.Read(11, 4); !errors.Is(err, domain.ErrOutputCursor) {
		t.Fatalf("future cursor guessed compatible: %v", err)
	}
}
func TestByteLogUnicodeEscapesAndLargeChunksRemainRawBytes(t *testing.T) {
	log, err := domain.NewByteLog(64)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("日本語🙂\x1b[31m\x1b]52;c;ZmFrZQ==\a\x1b]8;;https://example.invalid\a")
	if _, err = log.Append(data); err != nil {
		t.Fatal(err)
	}
	var got []byte
	cursor := domain.ByteOffset(0)
	for cursor < domain.ByteOffset(len(data)) {
		part, e := log.Read(cursor, 3)
		if e != nil || part.Gap {
			t.Fatal("unexpected raw byte gap")
		}
		got = append(got, part.Data...)
		cursor = part.End
	}
	if !bytes.Equal(got, data) {
		t.Fatal("Unicode/escape bytes interpreted or altered")
	}
	if _, err = log.Append(make([]byte, domain.MaxOutputChunk+1)); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("oversize native append allowed: %v", err)
	}
	if _, err = domain.NewByteLog(domain.MaxOutputBytes + 1); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("expanded output bound: %v", err)
	}
}
func TestByteOffsetCanonicalDecimalAndOverflow(t *testing.T) {
	for _, value := range []string{"", "00", "+1", "-1", "1.0", " 1", "18446744073709551616"} {
		if _, err := domain.ParseByteOffset(value); err == nil {
			t.Fatalf("noncanonical cursor accepted: %q", value)
		}
	}
	offset, err := domain.ParseByteOffset("18446744073709551615")
	if err != nil || offset != domain.ByteOffset(math.MaxUint64) || offset.String() != "18446744073709551615" {
		t.Fatal("lossy uint64 cursor")
	}
	if _, err = offset.Advance(1); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("byte counter wrapped: %v", err)
	}
}

func TestByteLogReferenceModelAcrossSmallCapacities(t *testing.T) {
	for _, capacity := range []int{1, 3, 8, 127, 1025} {
		log, err := domain.NewByteLog(capacity)
		if err != nil {
			t.Fatal(err)
		}
		var history []byte
		for step := 0; step < 160; step++ {
			chunk := bytes.Repeat([]byte{byte(step)}, 1+(step*37)%200)
			if _, err = log.Append(chunk); err != nil {
				t.Fatal(err)
			}
			history = append(history, chunk...)
			base := max(0, len(history)-capacity)
			state := log.Snapshot()
			if state.Base != domain.ByteOffset(base) || state.Head != domain.ByteOffset(len(history)) || state.Retained != len(history)-base {
				t.Fatal("ring metadata differs from reference")
			}
			part, err := log.Read(state.Base, domain.MaxOutputChunk)
			if err != nil || !bytes.Equal(part.Data, history[base:]) {
				t.Fatal("ring wrap differs from reference")
			}
		}
	}
}
