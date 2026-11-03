package runtime

import (
	"context"
	"errors"

	domain "github.com/duck8823/traceary/domain/runtime"
)

// ErrMetadataForgetUncertain requires a fresh preview, never automatic replay.
// One or more envelope replacements may already have committed.
var ErrMetadataForgetUncertain = errors.New("runtime metadata forget outcome uncertain; export and preview again before a new decision")

// MetadataProfileSummary omits executable paths, environment and credentials.
type MetadataProfileSummary struct {
	ID      domain.ProfileID `json:"id"`
	Host    string           `json:"host"`
	Version string           `json:"version"`
}

// MetadataRunSummary exports references/counts, not a second target registry,
// PID ownership, terminal contents, input payload or native conversation data.
type MetadataRunSummary struct {
	Run               string            `json:"run"`
	Generation        string            `json:"generation"`
	Epoch             string            `json:"epoch"`
	Worktree          domain.WorktreeID `json:"worktree"`
	Profile           domain.ProfileID  `json:"profile"`
	State             domain.RunState   `json:"recorded_state"`
	Uncertain         bool              `json:"uncertain"`
	Receipts          int               `json:"receipt_count"`
	UncertainReceipts int               `json:"uncertain_receipt_count"`
}

// MetadataExport is a versioned bounded audit summary, not a restore backup.
// Revision strings preserve exact counters beyond JSON's numeric precision.
type MetadataExport struct {
	LifecycleRevision        string                   `json:"lifecycle_revision"`
	EndpointRevision         string                   `json:"endpoint_revision"`
	InterruptedRuns          int                      `json:"interrupted_runs"`
	RetainedShutdowns        int                      `json:"retained_shutdowns"`
	RetainedUpdateDeferral   bool                     `json:"retained_update_deferral"`
	RetainedAutoStartOptIn   bool                     `json:"retained_autostart_opt_in"`
	RetainedEndpointEvidence bool                     `json:"retained_endpoint_evidence"`
	Version                  int                      `json:"version"`
	Authority                string                   `json:"authority"`
	RootWitness              string                   `json:"root_witness"`
	Digest                   string                   `json:"digest"`
	RegistryRevision         string                   `json:"registry_revision"`
	ExecutionRevision        string                   `json:"execution_revision"`
	ReceiptRevision          string                   `json:"receipt_revision"`
	Profiles                 []MetadataProfileSummary `json:"profiles"`
	Runs                     []MetadataRunSummary     `json:"runs"`
	TotalReceipts            int                      `json:"total_receipts"`
	UncertainRuns            int                      `json:"uncertain_runs"`
	UncertainReceipts        int                      `json:"uncertain_receipts"`
}

// MetadataForgetPreview describes exact historical scope and unavoidable loss.
type MetadataForgetPreview struct {
	Snapshot           MetadataExport `json:"snapshot"`
	RecoveryRecordLoss bool           `json:"recovery_record_loss"`
	OfflineRequired    bool           `json:"offline_required"`
}

// MetadataForgetResult exists only for a confirmed successful/no-op operation.
type MetadataForgetResult struct {
	InterruptedRuns int    `json:"interrupted_runs"`
	Outcome         string `json:"outcome"`
	Runs            int    `json:"runs"`
	Receipts        int    `json:"receipts"`
}

// MetadataAudit keeps explicit read/preview/mutation operations separate.
// No operation may start/stop/adopt a daemon or native child.
type MetadataAudit interface {
	Export(context.Context) (MetadataExport, error)
	PreviewForget(context.Context) (MetadataForgetPreview, error)
	Forget(context.Context, domain.MetadataForgetConfirmation) (MetadataForgetResult, error)
}

// MetadataAuditFactory opens only an explicitly selected existing root.
type MetadataAuditFactory interface {
	OpenMetadata(context.Context, string) (MetadataAudit, error)
}
