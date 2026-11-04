package runtime

import (
	"encoding/hex"
	"strings"
)

// MetadataForgetConfirmation is an explicit historical-record loss decision,
// not proof of native process death or permission to resume a conversation.
type MetadataForgetConfirmation struct{ digest string }

// MetadataForgetConfirmationOf requires both acknowledgements independently.
// A digest only binds an exact preview; it is not an authentication credential.
func MetadataForgetConfirmationOf(digest string, historyLoss, recoveryLoss bool) (MetadataForgetConfirmation, error) {
	if !historyLoss || !recoveryLoss || !validMetadataDigest(digest) {
		return MetadataForgetConfirmation{}, ErrInvalid
	}
	return MetadataForgetConfirmation{digest: digest}, nil
}
func validMetadataDigest(digest string) bool {
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// Valid rejects zero or malformed confirmations before any metadata mutation.
func (c MetadataForgetConfirmation) Valid() bool { return validMetadataDigest(c.digest) }

// Digest returns the preview binding, never native ownership authority.
func (c MetadataForgetConfirmation) Digest() string { return c.digest }
