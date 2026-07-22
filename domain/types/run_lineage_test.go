package types_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestRunIdentityFromPreservesOpaqueRunID(t *testing.T) {
	t.Parallel()
	identity, err := types.RunIdentityFrom(" codex ", "  Run/Ä  ")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Host() != "codex" || identity.RunID() != "  Run/Ä  " {
		t.Fatalf("identity = %q/%q", identity.Host(), identity.RunID())
	}
	if _, err := types.RunIdentityFrom("codex", strings.Repeat("é", 257)); err == nil {
		t.Fatal("multibyte run ID over 512 bytes accepted")
	}
}

func TestRunWorkAttributionValidatesRepositoryDependencies(t *testing.T) {
	t.Parallel()
	if _, err := types.RunWorkAttributionFrom(types.None[string](), types.None[string](), types.None[string](), types.Some[int64](1), types.None[string]()); err == nil {
		t.Fatal("pull request without repository accepted")
	}
	work, err := types.RunWorkAttributionFrom(types.Some(" batch "), types.Some("#1453"), types.Some("duck8823/traceary"), types.Some[int64](1453), types.Some(strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	batch, _ := work.BatchID().Value()
	if batch != "batch" {
		t.Fatalf("batch = %q", batch)
	}
}

func TestPacketIdentityPreservesKnownZero(t *testing.T) {
	t.Parallel()
	packet, err := types.PacketIdentityFrom(strings.Repeat("a", 64), 0)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Bytes() != 0 {
		t.Fatalf("bytes = %d", packet.Bytes())
	}
	if _, err := types.PacketIdentityFrom(strings.Repeat("A", 64), 0); err == nil {
		t.Fatal("uppercase hash accepted")
	}
}
