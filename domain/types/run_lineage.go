package types

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/xerrors"
)

const (
	maxRunIDBytes      = 512
	maxBatchIDBytes    = 512
	maxTicketRefBytes  = 512
	maxRepositoryBytes = 2048
)

// RunIdentity identifies one opaque execution inside a host namespace.
type RunIdentity struct {
	host  string
	runID string
}

// RunIdentityFrom validates a namespaced opaque run identity. The run ID is
// preserved byte-for-byte; only the host namespace is normalized.
func RunIdentityFrom(host, runID string) (RunIdentity, error) {
	host = strings.TrimSpace(host)
	if host == "" || !utf8.ValidString(host) {
		return RunIdentity{}, xerrors.Errorf("run identity host must be valid non-empty UTF-8")
	}
	if !utf8.ValidString(runID) || strings.TrimSpace(runID) == "" {
		return RunIdentity{}, xerrors.Errorf("run ID must be valid non-whitespace UTF-8")
	}
	if len([]byte(runID)) > maxRunIDBytes {
		return RunIdentity{}, xerrors.Errorf("run ID must not exceed %d bytes", maxRunIDBytes)
	}
	return RunIdentity{host: host, runID: runID}, nil
}

// Host returns the normalized host namespace.
func (i RunIdentity) Host() string { return i.host }

// RunID returns the opaque run identifier byte-for-byte.
func (i RunIdentity) RunID() string { return i.runID }

// RunWorkAttribution contains optional, body-free work grouping facts.
type RunWorkAttribution struct {
	batchID           Optional[string]
	ticketRef         Optional[string]
	repository        Optional[string]
	pullRequestNumber Optional[int64]
	headSHA           Optional[string]
}

// RunWorkAttributionFrom validates optional work grouping facts.
func RunWorkAttributionFrom(
	batchID, ticketRef, repository Optional[string],
	pullRequestNumber Optional[int64],
	headSHA Optional[string],
) (RunWorkAttribution, error) {
	normalizedBatch, err := normalizeOptionalLineageText(batchID, "batch ID", maxBatchIDBytes)
	if err != nil {
		return RunWorkAttribution{}, err
	}
	normalizedTicket, err := normalizeOptionalLineageText(ticketRef, "ticket ref", maxTicketRefBytes)
	if err != nil {
		return RunWorkAttribution{}, err
	}
	normalizedRepository, err := normalizeOptionalLineageText(repository, "repository", maxRepositoryBytes)
	if err != nil {
		return RunWorkAttribution{}, err
	}

	if value, present := pullRequestNumber.Value(); present && value < 1 {
		return RunWorkAttribution{}, xerrors.Errorf("pull request number must be positive")
	}
	normalizedHead := None[string]()
	if value, present := headSHA.Value(); present {
		value = strings.TrimSpace(value)
		if !utf8.ValidString(value) || (len(value) != 40 && len(value) != 64) || !isLowerHex(value) {
			return RunWorkAttribution{}, xerrors.Errorf("head SHA must be 40 or 64 lowercase hexadecimal bytes")
		}
		normalizedHead = Some(value)
	}
	_, hasRepository := normalizedRepository.Value()
	if _, present := pullRequestNumber.Value(); present && !hasRepository {
		return RunWorkAttribution{}, xerrors.Errorf("pull request number requires repository")
	}
	if _, present := normalizedHead.Value(); present && !hasRepository {
		return RunWorkAttribution{}, xerrors.Errorf("head SHA requires repository")
	}
	return RunWorkAttribution{
		batchID: normalizedBatch, ticketRef: normalizedTicket, repository: normalizedRepository,
		pullRequestNumber: pullRequestNumber, headSHA: normalizedHead,
	}, nil
}

// EmptyRunWorkAttribution returns work attribution with every fact unknown.
func EmptyRunWorkAttribution() RunWorkAttribution {
	return RunWorkAttribution{}
}

// BatchID returns the optional batch identity.
func (a RunWorkAttribution) BatchID() Optional[string] { return a.batchID }

// TicketRef returns the optional ticket reference.
func (a RunWorkAttribution) TicketRef() Optional[string] { return a.ticketRef }

// Repository returns the optional repository identity.
func (a RunWorkAttribution) Repository() Optional[string] { return a.repository }

// PullRequestNumber returns the optional positive pull request number.
func (a RunWorkAttribution) PullRequestNumber() Optional[int64] { return a.pullRequestNumber }

// HeadSHA returns the optional lowercase commit hash.
func (a RunWorkAttribution) HeadSHA() Optional[string] { return a.headSHA }

// PacketIdentity identifies the exact sanitized packet bytes handed to an engine.
type PacketIdentity struct {
	sha256 string
	bytes  int64
}

// PacketIdentityFrom validates an exact sanitized packet hash and byte size.
func PacketIdentityFrom(sha256 string, bytes int64) (PacketIdentity, error) {
	if !utf8.ValidString(sha256) || len(sha256) != 64 || !isLowerHex(sha256) {
		return PacketIdentity{}, xerrors.Errorf("packet SHA-256 must be 64 lowercase hexadecimal bytes")
	}
	if bytes < 0 {
		return PacketIdentity{}, xerrors.Errorf("packet bytes must not be negative")
	}
	return PacketIdentity{sha256: sha256, bytes: bytes}, nil
}

// SHA256 returns the lowercase packet digest.
func (p PacketIdentity) SHA256() string { return p.sha256 }

// Bytes returns the non-negative sanitized packet byte count.
func (p PacketIdentity) Bytes() int64 { return p.bytes }

func normalizeOptionalLineageText(value Optional[string], field string, limit int) (Optional[string], error) {
	raw, present := value.Value()
	if !present {
		return None[string](), nil
	}
	if !utf8.ValidString(raw) {
		return None[string](), xerrors.Errorf("%s must be valid UTF-8", field)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return None[string](), xerrors.Errorf("%s must not be empty", field)
	}
	if len([]byte(raw)) > limit {
		return None[string](), xerrors.Errorf("%s must not exceed %d bytes", field, limit)
	}
	return Some(raw), nil
}

func isLowerHex(value string) bool {
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
