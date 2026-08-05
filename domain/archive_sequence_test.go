package domain_test

import (
	"testing"

	"github.com/duck8823/traceary/domain"
)

func TestNewArchiveSequenceRejectsNonPositiveValues(t *testing.T) {
	t.Parallel()
	for _, value := range []int64{-1, 0} {
		if _, err := domain.NewArchiveSequence(value); err == nil {
			t.Fatalf("NewArchiveSequence(%d) error = nil", value)
		}
	}
	got, err := domain.NewArchiveSequence(1)
	if err != nil || got.Int64() != 1 {
		t.Fatalf("NewArchiveSequence(1) = (%d, %v)", got, err)
	}
}
