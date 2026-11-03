package runtime

import (
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

type shutdownPreviewRequestRecord struct {
	Epoch string `json:"epoch"`
}
type shutdownPageRequestRecord struct {
	Binding shutdownBindingRecord `json:"binding"`
	Cursor  string                `json:"cursor"`
	Offset  int                   `json:"offset"`
}
type shutdownConfirmationRecord struct {
	Binding              shutdownBindingRecord `json:"binding"`
	WorkLossAcknowledged bool                  `json:"work_loss_acknowledged"`
}
type lifecycleRequestRecord struct {
	Preview *shutdownPreviewRequestRecord `json:"preview,omitempty"`
	Page    *shutdownPageRequestRecord    `json:"page,omitempty"`
	Cancel  *shutdownBindingRecord        `json:"cancel,omitempty"`
	Confirm *shutdownConfirmationRecord   `json:"confirm,omitempty"`
	Receipt *shutdownBindingRecord        `json:"receipt,omitempty"`
}

func validLifecycleID(value string) bool {
	return (domain.LaunchIdentity{Epoch: value, Run: value, Generation: value}).Valid()
}
func (r lifecycleRequestRecord) validOperation(operation string) bool {
	count := 0
	for _, present := range []bool{r.Preview != nil, r.Page != nil, r.Cancel != nil, r.Confirm != nil, r.Receipt != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return false
	}
	var record *shutdownBindingRecord
	switch operation {
	case "shutdown_preview":
		return r.Preview != nil && validLifecycleID(r.Preview.Epoch)
	case "shutdown_page":
		if r.Page == nil || !validLifecycleID(r.Page.Cursor) || r.Page.Offset < 1 {
			return false
		}
		record = &r.Page.Binding
	case "shutdown_cancel":
		record = r.Cancel
	case "shutdown_confirm":
		if r.Confirm == nil || !r.Confirm.WorkLossAcknowledged {
			return false
		}
		record = &r.Confirm.Binding
	case "shutdown_receipt":
		record = r.Receipt
	default:
		return false
	}
	if record == nil {
		return false
	}
	binding, err := record.binding()
	if err != nil {
		return false
	}
	return r.Page == nil || r.Page.Offset <= binding.Snapshot().TotalRuns
}

type shutdownPreviewRunRecord struct {
	Selection outputSelectionRecord `json:"selection"`
	State     string                `json:"state"`
	Uncertain bool                  `json:"publication_uncertain"`
}
type shutdownPreviewPageRecord struct {
	Binding shutdownBindingRecord      `json:"binding"`
	Offset  int                        `json:"offset"`
	Runs    []shutdownPreviewRunRecord `json:"runs"`
	Next    string                     `json:"next,omitempty"`
}
type lifecycleResponseRecord struct {
	Preview   *shutdownPreviewPageRecord `json:"preview,omitempty"`
	Decision  *shutdownRecord            `json:"decision,omitempty"`
	Cancelled *shutdownBindingRecord     `json:"cancelled,omitempty"`
}

func recordShutdownPage(p app.ShutdownPreviewPage) *shutdownPreviewPageRecord {
	r := &shutdownPreviewPageRecord{Binding: recordShutdownBinding(p.Binding), Offset: p.Offset, Next: p.Next, Runs: []shutdownPreviewRunRecord{}}
	for _, row := range p.Runs {
		r.Runs = append(r.Runs, shutdownPreviewRunRecord{recordSelection(row.Selection), string(row.State), row.PublicationUncertain})
	}
	return r
}
func (r shutdownPreviewPageRecord) page() (app.ShutdownPreviewPage, error) {
	binding, err := r.Binding.binding()
	if err != nil || r.Runs == nil || len(r.Runs) > 2 {
		return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
	}
	p := app.ShutdownPreviewPage{Binding: binding.Snapshot(), Offset: r.Offset, Next: r.Next, Runs: []app.ShutdownPreviewRun{}}
	for _, row := range r.Runs {
		selection, e := row.Selection.selection()
		if e != nil {
			return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
		}
		p.Runs = append(p.Runs, app.ShutdownPreviewRun{Selection: selection, State: domain.RunState(row.State), PublicationUncertain: row.Uncertain})
	}
	if !p.Valid() {
		return app.ShutdownPreviewPage{}, app.ErrRuntimeProtocol
	}
	return p, nil
}

// A rejected wire decision is safe caller evidence, not a durable effect intent.
// Storage deliberately continues to reject it in shutdownRecord.result().
func (r shutdownRecord) decision() (app.ShutdownResult, error) {
	if domain.ShutdownOutcome(r.Outcome) == domain.ShutdownRejected {
		binding, err := r.Binding.binding()
		if err != nil || r.ClosedTerminals != 0 {
			return app.ShutdownResult{}, app.ErrRuntimeProtocol
		}
		return app.ShutdownResult{Binding: binding.Snapshot(), Outcome: domain.ShutdownRejected}, nil
	}
	result, err := r.result()
	if err != nil {
		return app.ShutdownResult{}, app.ErrRuntimeProtocol
	}
	return result, nil
}
