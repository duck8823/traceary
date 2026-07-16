// Package hostcoverage is the machine-readable source of truth for the per-host
// lifecycle capture matrix used by doctor diagnostics and the bilingual
// host-coverage docs table.
package hostcoverage

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"golang.org/x/xerrors"
)

// Status is the matrix cell classification for one host × lifecycle event.
type Status string

const (
	// StatusWired means Traceary's packaged integration captures this event.
	StatusWired Status = "wired"
	// StatusAvailable means the host exposes a hook but Traceary does not wire it.
	StatusAvailable Status = "available"
	// StatusUnsupported means the host does not expose a usable signal.
	StatusUnsupported Status = "unsupported"
)

// Localized is a bilingual text pair.
type Localized struct {
	EN string `json:"en"`
	JA string `json:"ja"`
}

// LifecycleEvent is one row of the coverage matrix.
type LifecycleEvent struct {
	ID           string     `json:"id"`
	Verification Localized  `json:"verification"`
}

// EventCell is one host cell for a lifecycle event.
type EventCell struct {
	Status  Status    `json:"status"`
	Summary Localized `json:"summary"`
}

// Host is one column of the coverage matrix.
type Host struct {
	ID                        string               `json:"id"`
	Package                   string               `json:"package"`
	Label                     Localized            `json:"label"`
	DoctorClient              string               `json:"doctor_client"`
	ExpectsSessionEnrichment  bool                 `json:"expects_session_enrichment"`
	Events                    map[string]EventCell `json:"events"`
}

// Matrix is the full host × lifecycle coverage document.
type Matrix struct {
	SchemaVersion   int              `json:"schema_version"`
	LastVerified    string           `json:"last_verified"`
	Notes           Localized        `json:"notes"`
	LifecycleEvents []LifecycleEvent `json:"lifecycle_events"`
	Hosts           []Host           `json:"hosts"`
}

//go:embed matrix.json
var matrixJSON []byte

var (
	matrixOnce sync.Once
	matrixVal  Matrix
	matrixErr  error
)

// Load returns the embedded host coverage matrix.
func Load() (Matrix, error) {
	matrixOnce.Do(func() {
		if err := json.Unmarshal(matrixJSON, &matrixVal); err != nil {
			matrixErr = xerrors.Errorf("parse host coverage matrix: %w", err)
			return
		}
		if err := matrixVal.validate(); err != nil {
			matrixErr = err
			return
		}
	})
	return matrixVal, matrixErr
}

// MustLoad returns the matrix or panics. Reserved for init-time wiring where a
// missing matrix is a programming error.
func MustLoad() Matrix {
	m, err := Load()
	if err != nil {
		panic(err)
	}
	return m
}

// HostByDoctorClient returns the matrix host for a doctor --client value.
func (m Matrix) HostByDoctorClient(client string) (Host, bool) {
	for _, host := range m.Hosts {
		if host.DoctorClient == client || host.ID == client {
			return host, true
		}
	}
	return Host{}, false
}

// WiredLifecycleEvents returns the lifecycle event IDs marked status=wired for
// the given doctor client.
func (m Matrix) WiredLifecycleEvents(client string) []string {
	host, ok := m.HostByDoctorClient(client)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(host.Events))
	for id, cell := range host.Events {
		if cell.Status == StatusWired {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// ExpectsSessionEnrichment reports whether doctor should judge prompt/transcript
// coverage for the host once a minimum sample of sessions is observed.
func (m Matrix) ExpectsSessionEnrichment(client string) bool {
	host, ok := m.HostByDoctorClient(client)
	if !ok {
		return false
	}
	return host.ExpectsSessionEnrichment
}

func (m Matrix) validate() error {
	if m.SchemaVersion != 1 {
		return xerrors.Errorf("unsupported host coverage schema_version %d", m.SchemaVersion)
	}
	if m.LastVerified == "" {
		return xerrors.Errorf("host coverage matrix missing last_verified")
	}
	if len(m.LifecycleEvents) == 0 {
		return xerrors.Errorf("host coverage matrix has no lifecycle_events")
	}
	if len(m.Hosts) == 0 {
		return xerrors.Errorf("host coverage matrix has no hosts")
	}
	for _, event := range m.LifecycleEvents {
		if event.ID == "" {
			return xerrors.Errorf("lifecycle event missing id")
		}
	}
	for _, host := range m.Hosts {
		if host.ID == "" || host.DoctorClient == "" {
			return xerrors.Errorf("host missing id or doctor_client")
		}
		for _, event := range m.LifecycleEvents {
			cell, ok := host.Events[event.ID]
			if !ok {
				return xerrors.Errorf("host %s missing lifecycle event %s", host.ID, event.ID)
			}
			switch cell.Status {
			case StatusWired, StatusAvailable, StatusUnsupported:
			default:
				return xerrors.Errorf("host %s event %s has invalid status %q", host.ID, event.ID, cell.Status)
			}
			if cell.Summary.EN == "" || cell.Summary.JA == "" {
				return xerrors.Errorf("host %s event %s missing bilingual summary", host.ID, event.ID)
			}
		}
	}
	return nil
}

// RenderMatrixTable builds the Markdown table for language ("en" or "ja").
func (m Matrix) RenderMatrixTable(lang string) string {
	var b strings.Builder
	b.WriteString("| Traceary lifecycle event |")
	for _, host := range m.Hosts {
		b.WriteString(" ")
		b.WriteString(pick(lang, host.Label))
		b.WriteString(" |")
	}
	b.WriteString(" ")
	if lang == "ja" {
		b.WriteString("確認方法")
	} else {
		b.WriteString("Verification")
	}
	b.WriteString(" |\n|---|")
	for range m.Hosts {
		b.WriteString("---|")
	}
	b.WriteString("---|\n")
	for _, event := range m.LifecycleEvents {
		b.WriteString("| `")
		b.WriteString(event.ID)
		b.WriteString("` |")
		for _, host := range m.Hosts {
			cell := host.Events[event.ID]
			b.WriteString(" ")
			b.WriteString(pick(lang, cell.Summary))
			b.WriteString(" |")
		}
		b.WriteString(" ")
		b.WriteString(pick(lang, event.Verification))
		b.WriteString(" |\n")
	}
	return b.String()
}

func pick(lang string, loc Localized) string {
	if lang == "ja" {
		return loc.JA
	}
	return loc.EN
}

// StatusSymbol is a convenience for diagnostics (not used in docs cells, which
// already embed the symbol in the summary).
func StatusSymbol(status Status) string {
	switch status {
	case StatusWired:
		return "●"
	case StatusAvailable:
		return "○"
	case StatusUnsupported:
		return "✕"
	default:
		return fmt.Sprintf("?(%s)", status)
	}
}
