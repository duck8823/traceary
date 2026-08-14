package attestation_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/attestation"
)

func TestContentSHA256_OutputIsNotInTheEncoding(t *testing.T) {
	t.Parallel()

	base := attestation.CommandContent{
		EventID:     "event-1",
		CreatedAt:   "2026-08-14T00:00:00Z",
		CommandText: []byte("git status"),
		InputText:   []byte("."),
	}
	left, err := attestation.CommandContentSHA256(base)
	if err != nil {
		t.Fatalf("CommandContentSHA256() error = %v", err)
	}
	// The function has no output parameter. Two calls with the same identity
	// fields are equal even when the caller would have different output_text.
	right, err := attestation.CommandContentSHA256(base)
	if err != nil {
		t.Fatalf("CommandContentSHA256() second call error = %v", err)
	}
	if left != right {
		t.Fatal("same command identity hashed differently")
	}

	changedInput := base
	changedInput.InputText = []byte("other")
	other, err := attestation.CommandContentSHA256(changedInput)
	if err != nil {
		t.Fatalf("CommandContentSHA256(changed input) error = %v", err)
	}
	if left == other {
		t.Fatal("input_text change must change the content digest")
	}

	changedCommand := base
	changedCommand.CommandText = []byte("git diff")
	other, err = attestation.CommandContentSHA256(changedCommand)
	if err != nil {
		t.Fatalf("CommandContentSHA256(changed command) error = %v", err)
	}
	if left == other {
		t.Fatal("command_text change must change the content digest")
	}
}

func TestContentSHA256_PromptAndCommandKindsDiffer(t *testing.T) {
	t.Parallel()

	id := "same-id"
	created := "2026-08-14T00:00:00.000000001Z"
	prompt, err := attestation.PromptContentSHA256(attestation.PromptContent{
		EventID:   id,
		CreatedAt: created,
		Body:      []byte("payload"),
	})
	if err != nil {
		t.Fatalf("PromptContentSHA256() error = %v", err)
	}
	command, err := attestation.CommandContentSHA256(attestation.CommandContent{
		EventID:     id,
		CreatedAt:   created,
		CommandText: []byte("payload"),
	})
	if err != nil {
		t.Fatalf("CommandContentSHA256() error = %v", err)
	}
	if prompt == command {
		t.Fatal("prompt and command encodings must not collide on the same id and bytes")
	}

	viaDispatch, err := attestation.ContentSHA256(attestation.KindPrompt, attestation.PromptContent{
		EventID: id, CreatedAt: created, Body: []byte("payload"),
	}, attestation.CommandContent{})
	if err != nil {
		t.Fatalf("ContentSHA256(prompt) error = %v", err)
	}
	if viaDispatch != prompt {
		t.Fatal("ContentSHA256(prompt) must match PromptContentSHA256")
	}
}

func TestContentSHA256_RejectsUnknownKind(t *testing.T) {
	t.Parallel()

	_, err := attestation.ContentSHA256("transcript", attestation.PromptContent{
		EventID: "e", CreatedAt: "t",
	}, attestation.CommandContent{})
	if err == nil {
		t.Fatal("ContentSHA256(transcript) error = nil, want unknown kind")
	}
}

func TestContentSHA256_RejectsEmptyIdentity(t *testing.T) {
	t.Parallel()

	if _, err := attestation.PromptContentSHA256(attestation.PromptContent{CreatedAt: "t"}); err == nil {
		t.Fatal("empty event id must fail")
	}
	if _, err := attestation.CommandContentSHA256(attestation.CommandContent{EventID: "e"}); err == nil {
		t.Fatal("empty created_at must fail")
	}
}

func TestGenesisSHA256_IsThePinnedV1Predecessor(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256([]byte("traceary.attest.genesis.v1\n"))
	want := hex.EncodeToString(sum[:])
	if diff := cmp.Diff(want, attestation.GenesisHex()); diff != "" {
		t.Fatalf("GenesisHex mismatch (-want +got):\n%s", diff)
	}
	if attestation.EncodeHex(attestation.GenesisSHA256()) != want {
		t.Fatal("EncodeHex(GenesisSHA256) must match GenesisHex")
	}
}

func TestLinkSHA256_ChangesWhenEitherInputChanges(t *testing.T) {
	t.Parallel()

	prev := attestation.GenesisSHA256()
	contentA := sha256.Sum256([]byte("a"))
	contentB := sha256.Sum256([]byte("b"))
	left := attestation.LinkSHA256(prev, contentA)
	if left == attestation.LinkSHA256(prev, contentB) {
		t.Fatal("link must include the content digest")
	}
	if left == attestation.LinkSHA256(contentA, contentA) {
		t.Fatal("link must include the predecessor digest")
	}
}

func TestParseHex_RoundTrip(t *testing.T) {
	t.Parallel()

	sum := attestation.GenesisSHA256()
	got, err := attestation.ParseHex(attestation.EncodeHex(sum))
	if err != nil {
		t.Fatalf("ParseHex() error = %v", err)
	}
	if got != sum {
		t.Fatal("ParseHex(EncodeHex) must round-trip")
	}
	if _, err := attestation.ParseHex("zz"); err == nil {
		t.Fatal("ParseHex(zz) error = nil")
	}
	if _, err := attestation.ParseHex("abcd"); err == nil {
		t.Fatal("ParseHex(short) error = nil")
	}
}
