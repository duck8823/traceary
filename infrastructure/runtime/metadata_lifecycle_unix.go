//go:build darwin || linux

package runtime

import (
	"encoding/hex"
	"os"
	"strings"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
)

// These closed v1 projections belong to metadata audit, not to the lifecycle
// owner. They cannot start epochs, reclaim endpoints, or grant native authority.
// Keep parity with the lifecycle writer through combined-source qualification.
const maxMetadataLifecycleBytes int64 = 16 * 1024 * 1024
const maxMetadataEndpointBytes int64 = 4096

type metadataLifecycleEnvelope struct {
	Version        int                           `json:"version"`
	Revision       *uint64                       `json:"revision"`
	Epoch          string                        `json:"epoch"`
	Clean          bool                          `json:"clean"`
	PreviousEpoch  string                        `json:"previous_epoch"`
	PreviousClean  bool                          `json:"previous_clean"`
	Interrupted    []metadataInterruptionRecord  `json:"interrupted"`
	Shutdowns      []metadataShutdownRecord      `json:"shutdowns"`
	Update         *metadataUpdateDeferralRecord `json:"update,omitempty"`
	AutoStartOptIn bool                          `json:"autostart_opt_in"`
}
type metadataInterruptionRecord struct {
	Selection       outputSelectionRecord `json:"selection"`
	ProcessUnknown  bool                  `json:"process_unknown"`
	PendingControls int                   `json:"pending_controls"`
}
type metadataShutdownBindingRecord struct {
	Epoch                  string `json:"epoch"`
	PreviewID              string `json:"preview_id"`
	Revision               string `json:"revision"`
	RosterDigest           string `json:"roster_digest"`
	TotalRuns              int    `json:"total_runs"`
	PendingLaunches        int    `json:"pending_launches"`
	PendingControls        int    `json:"pending_controls"`
	PendingReconciliations int    `json:"pending_reconciliations"`
}
type metadataShutdownRecord struct {
	Binding         metadataShutdownBindingRecord `json:"binding"`
	Outcome         string                        `json:"outcome"`
	ClosedTerminals int                           `json:"closed_terminals"`
}
type metadataUpdateDeferralRecord struct {
	Version     string `json:"version"`
	Protocol    int    `json:"protocol"`
	Started     string `json:"started"`
	Notified24h bool   `json:"notified_24h"`
}
type metadataEndpointRecord struct {
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	UID       *uint32 `json:"uid"`
	Dev       string  `json:"dev"`
	Ino       string  `json:"ino"`
	CtimeSec  *int64  `json:"ctime_sec"`
	CtimeNsec *int64  `json:"ctime_nsec"`
}
type metadataEndpointEnvelope struct {
	Version     int                      `json:"version"`
	Revision    *uint64                  `json:"revision"`
	Epoch       string                   `json:"epoch"`
	RootWitness string                   `json:"root_witness"`
	Endpoints   []metadataEndpointRecord `json:"endpoints"`
}

func metadataEpochValid(epoch string) bool {
	return (domain.LaunchIdentity{Epoch: epoch, Run: epoch, Generation: epoch}).Valid()
}
func metadataCanonicalHex(value string, size int) bool {
	if len(value) != size || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func (r metadataUpdateDeferralRecord) valid() bool {
	if len(r.Version) == 0 || len(r.Version) > 128 || r.Protocol < 1 || r.Protocol > 65535 {
		return false
	}
	for _, c := range r.Version {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' || c == '+'
		if !valid {
			return false
		}
	}
	started, err := time.Parse(time.RFC3339Nano, r.Started)
	return err == nil && !started.IsZero() && len(r.Started) <= 40 && started.UTC().Format(time.RFC3339Nano) == r.Started
}
func (r metadataShutdownRecord) valid() bool {
	b := r.Binding
	_, err := domain.ParseByteOffset(b.Revision)
	if err != nil || !metadataEpochValid(b.Epoch) || !metadataEpochValid(b.PreviewID) || !metadataCanonicalHex(b.RosterDigest, 64) || b.TotalRuns < 0 || b.TotalRuns > domain.MaxLaunchRequests || b.PendingLaunches != 0 || b.PendingControls != 0 || b.PendingReconciliations != 0 || r.ClosedTerminals < 0 || r.ClosedTerminals > b.TotalRuns {
		return false
	}
	switch r.Outcome {
	case "pending":
		return r.ClosedTerminals == 0
	case "draining":
		return r.ClosedTerminals == b.TotalRuns
	case "unknown":
		return true
	default:
		return false
	}
}
func (s *MetadataStore) readAuditLifecycle(fd int, execution executionEnvelope) (metadataLifecycleEnvelope, *unix.Stat_t, error) {
	m := s.execution.metadata
	data, stamp, err := m.readMetadata(fd, "lifecycle.json", min(maxMetadataLifecycleBytes, m.budget))
	if err != nil {
		return metadataLifecycleEnvelope{}, nil, err
	}
	revision := uint64(0)
	doc := metadataLifecycleEnvelope{Version: 1, Revision: &revision, Interrupted: []metadataInterruptionRecord{}, Shutdowns: []metadataShutdownRecord{}}
	if stamp == nil {
		return doc, nil, nil
	}
	doc = metadataLifecycleEnvelope{}
	if err = decodeControlMetadata(data, &doc); err != nil {
		return metadataLifecycleEnvelope{}, nil, err
	}
	if doc.Revision == nil || doc.Interrupted == nil || doc.Shutdowns == nil || len(doc.Interrupted) > domain.MaxLaunchRequests || len(doc.Shutdowns) > domain.MaxLaunchRequests {
		return metadataLifecycleEnvelope{}, nil, domain.ErrInvalid
	}
	if doc.Epoch != "" && !metadataEpochValid(doc.Epoch) || doc.PreviousEpoch != "" && (!metadataEpochValid(doc.PreviousEpoch) || doc.PreviousEpoch == doc.Epoch) || doc.PreviousClean && doc.PreviousEpoch == "" || doc.Clean && doc.Epoch == "" {
		return metadataLifecycleEnvelope{}, nil, domain.ErrInvalid
	}
	if doc.Epoch == "" && (doc.PreviousEpoch != "" || len(doc.Interrupted) != 0 || len(doc.Shutdowns) != 0 || doc.Update != nil) || doc.Update != nil && !doc.Update.valid() {
		return metadataLifecycleEnvelope{}, nil, domain.ErrInvalid
	}
	bindings := make(map[string]app.OutputSelection, len(execution.Launches))
	for _, record := range execution.Launches {
		snapshot, e := record.domainSnapshot()
		if e != nil {
			return metadataLifecycleEnvelope{}, nil, e
		}
		bindings[snapshot.Identity.Run] = app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	}
	seen := make(map[string]bool, len(doc.Interrupted))
	for _, r := range doc.Interrupted {
		selection, e := r.Selection.selection()
		if e != nil || bindings[r.Selection.Run] != selection || selection.Identity.Epoch == doc.Epoch || seen[r.Selection.Run] || r.PendingControls < 0 || r.PendingControls > domain.MaxControlReceipts || !r.ProcessUnknown && r.PendingControls == 0 {
			return metadataLifecycleEnvelope{}, nil, domain.ErrInvalid
		}
		seen[r.Selection.Run] = true
	}
	plans := make(map[string]bool, len(doc.Shutdowns))
	for _, r := range doc.Shutdowns {
		key := r.Binding.Epoch + r.Binding.PreviewID
		if !r.valid() || plans[key] {
			return metadataLifecycleEnvelope{}, nil, domain.ErrInvalid
		}
		plans[key] = true
	}
	if err = m.metadataBudget(fd, "lifecycle.json", int64(len(data))); err != nil {
		return metadataLifecycleEnvelope{}, nil, err
	}
	return doc, stamp, nil
}
func (s *MetadataStore) readAuditEndpoints(fd int) (metadataEndpointEnvelope, *unix.Stat_t, error) {
	m := s.execution.metadata
	data, stamp, err := m.readMetadata(fd, "endpoints.json", min(maxMetadataEndpointBytes, m.budget))
	if err != nil {
		return metadataEndpointEnvelope{}, nil, err
	}
	if stamp == nil {
		return metadataEndpointEnvelope{}, nil, nil
	}
	var doc metadataEndpointEnvelope
	if err = decodeControlMetadata(data, &doc); err != nil {
		return metadataEndpointEnvelope{}, nil, err
	}
	// Offline audit intentionally has no NEW startup epoch to exclude. Equality
	// with lifecycle.Epoch is normal and conveys no endpoint/process ownership.
	if doc.Revision == nil || *doc.Revision < 1 || !metadataEpochValid(doc.Epoch) || doc.RootWitness != m.witness || len(doc.Endpoints) != 2 {
		return metadataEndpointEnvelope{}, nil, domain.ErrInvalid
	}
	names := [2]string{"daemon.sock", "observations.sock"}
	roles := [2]string{"management", "observations"}
	for i, r := range doc.Endpoints {
		_, d := domain.ParseByteOffset(r.Dev)
		_, n := domain.ParseByteOffset(r.Ino)
		if r.Name != names[i] || r.Role != roles[i] || r.UID == nil || *r.UID != uint32(os.Geteuid()) || r.CtimeSec == nil || r.CtimeNsec == nil || *r.CtimeNsec < 0 || *r.CtimeNsec >= 1e9 || d != nil || n != nil {
			return metadataEndpointEnvelope{}, nil, domain.ErrInvalid
		}
	}
	if doc.Endpoints[0].Dev == doc.Endpoints[1].Dev && doc.Endpoints[0].Ino == doc.Endpoints[1].Ino {
		return metadataEndpointEnvelope{}, nil, domain.ErrInvalid
	}
	if err = m.metadataBudget(fd, "endpoints.json", int64(len(data))); err != nil {
		return metadataEndpointEnvelope{}, nil, err
	}
	return doc, stamp, nil
}
