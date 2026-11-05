package runtime

import (
	"encoding/base64"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"strings"
)

type outputSelectionRecord struct {
	Epoch      string `json:"epoch"`
	Run        string `json:"run"`
	Generation string `json:"generation"`
	Worktree   string `json:"worktree"`
	Profile    string `json:"profile"`
}
type outputAttachRecord struct {
	Selection outputSelectionRecord `json:"selection"`
	Cursor    string                `json:"cursor"`
}
type outputResizeRecord struct {
	Sequence string `json:"sequence"`
	Rows     uint16 `json:"rows"`
	Cols     uint16 `json:"cols"`
}
type outputInfoRecord struct {
	Selection outputSelectionRecord `json:"selection"`
	Base      string                `json:"base"`
	Head      string                `json:"head"`
	Ended     bool                  `json:"ended"`
	Reason    string                `json:"reason,omitempty"`
	Resize    outputResizeRecord    `json:"resize"`
}
type outputGapRecord struct {
	From            string `json:"from"`
	To              string `json:"to"`
	Reason          string `json:"reason"`
	DeliveryUnknown bool   `json:"delivery_unknown"`
}
type outputRecord struct {
	Kind  string           `json:"kind"`
	Info  outputInfoRecord `json:"info"`
	Start string           `json:"start,omitempty"`
	End   string           `json:"end,omitempty"`
	Data  string           `json:"data,omitempty"`
	Gap   *outputGapRecord `json:"gap,omitempty"`
}

func recordSelection(s app.OutputSelection) outputSelectionRecord {
	return outputSelectionRecord{s.Identity.Epoch, s.Identity.Run, s.Identity.Generation, string(s.Worktree), string(s.Profile)}
}
func (r outputSelectionRecord) selection() (app.OutputSelection, error) {
	s := app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: r.Epoch, Run: r.Run, Generation: r.Generation}, Worktree: domain.WorktreeID(r.Worktree), Profile: domain.ProfileID(r.Profile)}
	if !s.Identity.Valid() || len(r.Worktree) != 66 || !strings.HasPrefix(r.Worktree, "w_") {
		return app.OutputSelection{}, app.ErrRuntimeProtocol
	}
	for _, c := range r.Worktree[2:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return app.OutputSelection{}, app.ErrRuntimeProtocol
		}
	}
	if _, err := domain.ProfileIDFrom(r.Profile); err != nil {
		return app.OutputSelection{}, app.ErrRuntimeProtocol
	}
	return s, nil
}
func recordOutput(f app.OutputFrame) *outputRecord {
	r := &outputRecord{Kind: f.Kind, Info: outputInfoRecord{Selection: recordSelection(f.Info.Selection), Base: f.Info.Base.String(), Head: f.Info.Head.String(), Ended: f.Info.Ended, Reason: string(f.Info.Reason), Resize: outputResizeRecord{domain.ByteOffset(f.Info.Resize.Sequence).String(), f.Info.Resize.Rows, f.Info.Resize.Cols}}}
	if f.Kind == "data" {
		r.Start = f.Start.String()
		r.End = f.End.String()
		r.Data = base64.StdEncoding.EncodeToString(f.Data)
	}
	if f.Gap != nil {
		r.Gap = &outputGapRecord{f.Gap.From.String(), f.Gap.To.String(), f.Gap.Reason, f.Gap.DeliveryUnknown}
	}
	return r
}
func (r outputRecord) frame() (app.OutputFrame, error) {
	invalid := app.ErrRuntimeProtocol
	selection, err := r.Info.Selection.selection()
	if err != nil {
		return app.OutputFrame{}, invalid
	}
	base, e1 := domain.ParseByteOffset(r.Info.Base)
	head, e2 := domain.ParseByteOffset(r.Info.Head)
	sequence, e3 := domain.ParseByteOffset(r.Info.Resize.Sequence)
	if e1 != nil || e2 != nil || e3 != nil || base > head || head-base > domain.MaxOutputBytes || r.Info.Resize.Rows == 0 || r.Info.Resize.Cols == 0 {
		return app.OutputFrame{}, invalid
	}
	if r.Info.Ended {
		switch app.CaptureReason(r.Info.Reason) {
		case app.CaptureClosed, app.CaptureReadError, app.CaptureTailUnknown, app.CaptureTerminalClosed:
		default:
			return app.OutputFrame{}, invalid
		}
	} else if r.Info.Reason != "" {
		return app.OutputFrame{}, invalid
	}
	f := app.OutputFrame{Kind: r.Kind, Info: app.OutputInfo{Selection: selection, Base: base, Head: head, Ended: r.Info.Ended, Reason: app.CaptureReason(r.Info.Reason), Resize: app.ResizeEvidence{Sequence: uint64(sequence), Rows: r.Info.Resize.Rows, Cols: r.Info.Resize.Cols}}}
	if r.Kind != "data" && (r.Start != "" || r.End != "" || r.Data != "") {
		return app.OutputFrame{}, invalid
	}
	if r.Kind != "gap" && r.Gap != nil {
		return app.OutputFrame{}, invalid
	}
	switch r.Kind {
	case "snapshot":
	case "end":
		if !f.Info.Ended {
			return app.OutputFrame{}, invalid
		}
	case "data":
		start, e1 := domain.ParseByteOffset(r.Start)
		end, e2 := domain.ParseByteOffset(r.End)
		data, e3 := base64.StdEncoding.Strict().DecodeString(r.Data)
		if e1 != nil || e2 != nil || e3 != nil || len(data) == 0 || len(data) > domain.MaxOutputChunk || start < base || end > head || start >= end || uint64(end-start) != uint64(len(data)) || base64.StdEncoding.EncodeToString(data) != r.Data {
			return app.OutputFrame{}, invalid
		}
		f.Start = start
		f.End = end
		f.Data = data
	case "gap":
		if r.Gap == nil {
			return app.OutputFrame{}, invalid
		}
		g := r.Gap
		from, e1 := domain.ParseByteOffset(g.From)
		to, e2 := domain.ParseByteOffset(g.To)
		if e1 != nil || e2 != nil || from > to || to > head {
			return app.OutputFrame{}, invalid
		}
		switch g.Reason {
		case "evicted":
			if g.DeliveryUnknown || from == to || to > base {
				return app.OutputFrame{}, invalid
			}
		case string(app.CaptureTerminalClosed):
			// This is a zero-width unknowable tail at the ended capture head,
			// not a claimed byte loss interval or proof of native process exit.
			if !g.DeliveryUnknown || !r.Info.Ended || f.Info.Reason != app.CaptureTerminalClosed || from != to || to != head {
				return app.OutputFrame{}, invalid
			}
		case "slow_consumer", "tail_unknown", "read_error":
			if !g.DeliveryUnknown {
				return app.OutputFrame{}, invalid
			}
		default:
			return app.OutputFrame{}, invalid
		}
		f.Gap = &app.OutputGap{From: from, To: to, Reason: g.Reason, DeliveryUnknown: g.DeliveryUnknown}
	default:
		return app.OutputFrame{}, invalid
	}
	return f, nil
}
