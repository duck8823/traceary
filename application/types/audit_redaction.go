package types

import "slices"

// AuditRedaction holds redaction and truncation settings for command audit recording.
type AuditRedaction struct {
	allowSecrets        bool
	maxInputBytes       int
	maxOutputBytes      int
	extraRedactPatterns []string
}

// AllowSecrets reports whether default secret redaction should be bypassed.
func (r AuditRedaction) AllowSecrets() bool { return r.allowSecrets }

// MaxInputBytes returns the maximum number of stored input bytes.
func (r AuditRedaction) MaxInputBytes() int { return r.maxInputBytes }

// MaxOutputBytes returns the maximum number of stored output bytes.
func (r AuditRedaction) MaxOutputBytes() int { return r.maxOutputBytes }

// ExtraRedactPatterns returns additional redaction regex patterns.
func (r AuditRedaction) ExtraRedactPatterns() []string {
	return slices.Clone(r.extraRedactPatterns)
}

// AuditRedactionBuilder builds an AuditRedaction value.
type AuditRedactionBuilder struct {
	redaction AuditRedaction
}

// NewAuditRedactionBuilder starts building an empty AuditRedaction.
func NewAuditRedactionBuilder() *AuditRedactionBuilder {
	return &AuditRedactionBuilder{}
}

// AllowSecrets toggles whether default secret redaction is bypassed.
func (b *AuditRedactionBuilder) AllowSecrets(allow bool) *AuditRedactionBuilder {
	b.redaction.allowSecrets = allow
	return b
}

// MaxInputBytes sets the maximum number of stored input bytes.
func (b *AuditRedactionBuilder) MaxInputBytes(n int) *AuditRedactionBuilder {
	b.redaction.maxInputBytes = n
	return b
}

// MaxOutputBytes sets the maximum number of stored output bytes.
func (b *AuditRedactionBuilder) MaxOutputBytes(n int) *AuditRedactionBuilder {
	b.redaction.maxOutputBytes = n
	return b
}

// ExtraRedactPatterns sets additional redaction regex patterns.
func (b *AuditRedactionBuilder) ExtraRedactPatterns(patterns []string) *AuditRedactionBuilder {
	b.redaction.extraRedactPatterns = slices.Clone(patterns)
	return b
}

// Build finalizes and returns the AuditRedaction.
func (b *AuditRedactionBuilder) Build() AuditRedaction {
	return b.redaction
}
