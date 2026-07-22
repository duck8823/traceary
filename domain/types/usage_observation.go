package types

import (
	"strings"

	"golang.org/x/xerrors"
)

// UsageObservationID is the opaque, adapter-defined authoritative identity
// for one call, run, or snapshot revision.
type UsageObservationID string

// UsageObservationIDFrom validates an opaque authoritative identity.
func UsageObservationIDFrom(value string) (UsageObservationID, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", xerrors.Errorf("usage observation ID must not be empty")
	}
	if len(trimmed) > 512 {
		return "", xerrors.Errorf("usage observation ID must not exceed 512 bytes")
	}
	return UsageObservationID(trimmed), nil
}

func (id UsageObservationID) String() string { return string(id) }

// UsageScope is the accounting granularity selected by a host adapter.
type UsageScope string

const (
	// UsageScopeCall identifies one provider call.
	UsageScopeCall UsageScope = "call"
	// UsageScopeRun identifies one host run summary.
	UsageScopeRun UsageScope = "run"
	// UsageScopeSessionSnapshot identifies a cumulative session snapshot.
	UsageScopeSessionSnapshot UsageScope = "session_snapshot"
)

// UsageScopeFrom restores a validated usage scope.
func UsageScopeFrom(value string) (UsageScope, error) {
	scope := UsageScope(value)
	switch scope {
	case UsageScopeCall, UsageScopeRun, UsageScopeSessionSnapshot:
		return scope, nil
	default:
		return "", xerrors.Errorf("unsupported usage scope: %q", value)
	}
}

func (s UsageScope) String() string { return string(s) }

// UsageAccounting declares how a finalized observation may participate in an
// aggregate. Excluded is explicit rather than an overloaded boolean.
type UsageAccounting string

const (
	// UsageAccountingAdditive contributes a finalized observation once.
	UsageAccountingAdditive UsageAccounting = "additive"
	// UsageAccountingLatestSnapshot selects only the unsuperseded series head.
	UsageAccountingLatestSnapshot UsageAccounting = "latest_snapshot"
	// UsageAccountingExcluded retains evidence without contributing to totals.
	UsageAccountingExcluded UsageAccounting = "excluded"
)

// UsageAccountingFrom restores a validated accounting mode.
func UsageAccountingFrom(value string) (UsageAccounting, error) {
	accounting := UsageAccounting(value)
	switch accounting {
	case UsageAccountingAdditive, UsageAccountingLatestSnapshot, UsageAccountingExcluded:
		return accounting, nil
	default:
		return "", xerrors.Errorf("unsupported usage accounting mode: %q", value)
	}
}

func (a UsageAccounting) String() string { return string(a) }

// UsageObservationStatus is the durable lifecycle state.
type UsageObservationStatus string

const (
	// UsageObservationPending is open for one terminal completion.
	UsageObservationPending UsageObservationStatus = "pending"
	// UsageObservationFinalized is immutable terminal accounting.
	UsageObservationFinalized UsageObservationStatus = "finalized"
)

// UsageObservationStatusFrom restores a validated lifecycle status.
func UsageObservationStatusFrom(value string) (UsageObservationStatus, error) {
	status := UsageObservationStatus(value)
	switch status {
	case UsageObservationPending, UsageObservationFinalized:
		return status, nil
	default:
		return "", xerrors.Errorf("unsupported usage observation status: %q", value)
	}
}

func (s UsageObservationStatus) String() string { return string(s) }

// UsageSource identifies the versioned, body-free local surface selected by
// an adapter. Provider and model remain empty when the host did not report them.
type UsageSource struct {
	host     string
	name     string
	version  string
	provider string
	model    string
}

// UsageSourceOf creates a validated local host-source descriptor.
func UsageSourceOf(host, name, version, provider, model string) (UsageSource, error) {
	source := UsageSource{
		host: strings.TrimSpace(host), name: strings.TrimSpace(name), version: strings.TrimSpace(version),
		provider: strings.TrimSpace(provider), model: strings.TrimSpace(model),
	}
	if source.host == "" || source.name == "" || source.version == "" {
		return UsageSource{}, xerrors.Errorf("usage source host, name, and version must not be empty")
	}
	return source, nil
}

// Host returns the local AI host name.
func (s UsageSource) Host() string { return s.host }

// Name returns the selected host surface or capture mode.
func (s UsageSource) Name() string { return s.name }

// Version returns the verified host source version.
func (s UsageSource) Version() string { return s.version }

// Provider returns the reported provider, or empty when absent.
func (s UsageSource) Provider() string { return s.provider }

// Model returns the reported model, or empty when absent.
func (s UsageSource) Model() string { return s.model }
