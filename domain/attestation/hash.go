// Package attestation defines the v1 hash contract for the instruction and
// command-identity chain. It has no store or CLI dependency.
package attestation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/xerrors"
)

const (
	// KindPrompt is the attested kind for a human instruction.
	KindPrompt = "prompt"
	// KindCommand is the attested kind for command identity. It is not the
	// event kind string command_executed.
	KindCommand = "command"

	genesisPayload   = "traceary.attest.genesis.v1\n"
	contentVersionV1 = "traceary.attest.v1"
	linkVersionV1    = "traceary.attest.link.v1"
)

// PromptContent is the attested prompt field set. Body is the stored/trimmed
// plaintext, not a codec frame.
type PromptContent struct {
	EventID   string
	CreatedAt string
	Body      []byte
}

// CommandContent is the attested command-identity field set. There is no
// output field: output_text is outside the attested set.
type CommandContent struct {
	EventID     string
	CreatedAt   string
	CommandText []byte
	InputText   []byte
}

// GenesisSHA256 is the empty-store predecessor. The first link hashes this
// value as prev.
func GenesisSHA256() [sha256.Size]byte {
	return sha256.Sum256([]byte(genesisPayload))
}

// GenesisHex is the lowercase hex encoding of GenesisSHA256.
func GenesisHex() string {
	sum := GenesisSHA256()
	return hex.EncodeToString(sum[:])
}

// PromptContentSHA256 hashes one prompt under the v1 encoding.
func PromptContentSHA256(content PromptContent) ([sha256.Size]byte, error) {
	if err := validateIdentity(content.EventID, content.CreatedAt); err != nil {
		return [sha256.Size]byte{}, err
	}
	payload := encodePromptContent(content)
	return sha256.Sum256(payload), nil
}

// CommandContentSHA256 hashes one command identity under the v1 encoding.
// Output is not an input to this function.
func CommandContentSHA256(content CommandContent) ([sha256.Size]byte, error) {
	if err := validateIdentity(content.EventID, content.CreatedAt); err != nil {
		return [sha256.Size]byte{}, err
	}
	payload := encodeCommandContent(content)
	return sha256.Sum256(payload), nil
}

// ContentSHA256 dispatches on kind. Unknown kinds are an error.
func ContentSHA256(kind string, prompt PromptContent, command CommandContent) ([sha256.Size]byte, error) {
	switch kind {
	case KindPrompt:
		return PromptContentSHA256(prompt)
	case KindCommand:
		return CommandContentSHA256(command)
	default:
		return [sha256.Size]byte{}, xerrors.Errorf("unknown attestation kind %q", kind)
	}
}

// LinkSHA256 binds a predecessor digest to a content digest.
func LinkSHA256(prev, content [sha256.Size]byte) [sha256.Size]byte {
	payload := fmt.Sprintf(
		"%s\nprev %s\ncontent %s\n",
		linkVersionV1,
		hex.EncodeToString(prev[:]),
		hex.EncodeToString(content[:]),
	)
	return sha256.Sum256([]byte(payload))
}

// EncodeHex returns the lowercase hex form of a digest.
func EncodeHex(sum [sha256.Size]byte) string {
	return hex.EncodeToString(sum[:])
}

// ParseHex decodes a 64-character lowercase or mixed-case hex digest.
func ParseHex(value string) ([sha256.Size]byte, error) {
	trimmed := strings.TrimSpace(value)
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return [sha256.Size]byte{}, xerrors.Errorf("decode attestation digest: %w", err)
	}
	if len(raw) != sha256.Size {
		return [sha256.Size]byte{}, xerrors.Errorf("attestation digest must be %d bytes, got %d", sha256.Size, len(raw))
	}
	var out [sha256.Size]byte
	copy(out[:], raw)
	return out, nil
}

func validateIdentity(eventID, createdAt string) error {
	if strings.TrimSpace(eventID) == "" {
		return xerrors.Errorf("attestation event id must not be empty")
	}
	if strings.TrimSpace(createdAt) == "" {
		return xerrors.Errorf("attestation created_at must not be empty")
	}
	return nil
}

func encodePromptContent(content PromptContent) []byte {
	header := fmt.Sprintf(
		"%s\nkind %s\nevent_id %s\ncreated_at %s\nbody %d\n",
		contentVersionV1,
		KindPrompt,
		content.EventID,
		content.CreatedAt,
		len(content.Body),
	)
	out := make([]byte, 0, len(header)+len(content.Body))
	out = append(out, header...)
	out = append(out, content.Body...)
	return out
}

func encodeCommandContent(content CommandContent) []byte {
	header := fmt.Sprintf(
		"%s\nkind %s\nevent_id %s\ncreated_at %s\ncommand_text %d\n",
		contentVersionV1,
		KindCommand,
		content.EventID,
		content.CreatedAt,
		len(content.CommandText),
	)
	mid := fmt.Sprintf("\ninput_text %d\n", len(content.InputText))
	out := make([]byte, 0, len(header)+len(content.CommandText)+len(mid)+len(content.InputText))
	out = append(out, header...)
	out = append(out, content.CommandText...)
	out = append(out, mid...)
	out = append(out, content.InputText...)
	return out
}
