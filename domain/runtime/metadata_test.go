package runtime_test

import (
	"strings"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestMetadataForgetConfirmationRequiresCanonicalDigestAndBothAcknowledgements(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, acknowledgements := range [][2]bool{{false, false}, {true, false}, {false, true}} {
		if _, err := domain.MetadataForgetConfirmationOf(digest, acknowledgements[0], acknowledgements[1]); err == nil {
			t.Fatal("missing acknowledgement accepted")
		}
	}
	for _, invalid := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63), digest + "\n"} {
		if _, err := domain.MetadataForgetConfirmationOf(invalid, true, true); err == nil {
			t.Fatal("noncanonical digest accepted")
		}
	}
	c, err := domain.MetadataForgetConfirmationOf(digest, true, true)
	if err != nil || !c.Valid() || c.Digest() != digest {
		t.Fatal("valid confirmation rejected", err)
	}
	if (domain.MetadataForgetConfirmation{}).Valid() {
		t.Fatal("zero confirmation accepted")
	}
}
