//go:build unix

package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPayloadRehearsalResumeRejectsHardLinkedSidecarsWithoutMutatingAlias(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			adapter, config, _ := newSwapRehearsalFixture(t)
			victim := filepath.Join(t.TempDir(), "victim")
			contents := []byte("unrelated-data")
			if err := os.WriteFile(victim, contents, 0600); err != nil {
				t.Fatal(err)
			}
			before := sha256.Sum256(contents)
			if err := os.Link(victim, config.TargetPath+suffix); err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Run(context.Background(), config, true); !errors.Is(err, ErrUnsafeRehearsalTarget) {
				t.Fatalf("error=%v", err)
			}
			afterBytes, err := os.ReadFile(victim)
			if err != nil {
				t.Fatal(err)
			}
			if after := sha256.Sum256(afterBytes); after != before {
				t.Fatal("hard-link alias was mutated")
			}
		})
	}
}
