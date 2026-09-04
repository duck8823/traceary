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
