package usecase

import (
	apptypes "github.com/duck8823/traceary/application/types"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const projectionWALFactor int64 = 2

// PlanProjectionBatch is pure: the same snapshot and budget produce the same plan.
func PlanProjectionBatch(s apptypes.ProjectionSnapshot, b apptypes.SearchProjectionBudget) (apptypes.ProjectionBatchPlan, error) {
	p := apptypes.ProjectionBatchPlan{GenerationID: s.Generation.GenerationID, Phase: s.Phase, ExpectedRevision: s.Generation.SourceRevision, ExpectedCheckpoint: s.Generation.Checkpoint, NextCheckpoint: s.Generation.Checkpoint}
	if s.Phase != "source" {
		rp, err := PlanProjectionRetention(s.Cleanup, b)
		if err != nil {
			return p, err
		}
		p.Cleanup, p.Ledger = rp.Candidates, rp.Ledger
		if s.CleanupDone && len(s.Cleanup) == len(p.Cleanup) {
			if s.Phase == "eviction" {
				p.NextPhase = "cleanup"
			} else {
				p.NextPhase = "complete"
				p.Completed = true
			}
		}
		return p, nil
	}
	summaries := map[string]string{}
	for _, d := range s.Documents {
		if p.Ledger.Rows >= b.Rows || p.Ledger.StoredBytes+d.StoredBytes > b.StoredBytes || p.Ledger.DecodedBytes+d.DecodedBytes > b.DecodedBytes {
			break
		}
		if d.StoredBytes > b.StoredBytes {
			return p, &apptypes.SearchProjectionOversizeError{Class: "stored_bytes", Bytes: d.StoredBytes, Limit: b.StoredBytes}
		}
		if d.DecodedBytes > b.DecodedBytes {
			return p, &apptypes.SearchProjectionOversizeError{Class: "decoded_bytes", Bytes: d.DecodedBytes, Limit: b.DecodedBytes}
		}
		w := apptypes.ProjectionWrite{Document: d, Keywords: map[string]int{}}
		if created, err := time.Parse(time.RFC3339Nano, d.CreatedAt); err == nil {
			w.RetainRecent = !created.Before(s.Now.Add(-b.RecentAge))
		}
		base, ok := summaries[d.SessionID]
		if !ok {
			base = d.PreviousSummary
		}
		w.Summary = truncateProjection(strings.TrimSpace(base+"\n"+d.Text), 4096)
		summaries[d.SessionID] = w.Summary
		for _, k := range projectionTokens(strings.ToLower(d.Text)) {
			w.Keywords[k]++
		}
		w.LogicalBytes = int64(len(d.Text)*2 + len(w.Summary) + len(d.SessionID) + 32)
		for k := range w.Keywords {
			w.LogicalBytes += int64(len(k) + 24)
		}
		wal := w.LogicalBytes * projectionWALFactor
		if w.LogicalBytes > b.WriteBytes {
			return p, &apptypes.SearchProjectionOversizeError{Class: "write_bytes", Bytes: w.LogicalBytes, Limit: b.WriteBytes}
		}
		if p.Ledger.LogicalWriteBytes+w.LogicalBytes+p.Ledger.WALReservationBytes+wal > b.WriteBytes {
			break
		}
		p.Writes = append(p.Writes, w)
		p.NextCheckpoint = d.Sequence
		p.Ledger.Rows++
		p.Ledger.StoredBytes += d.StoredBytes
		p.Ledger.DecodedBytes += d.DecodedBytes
		p.Ledger.LogicalWriteBytes += w.LogicalBytes
		p.Ledger.WALReservationBytes += wal
	}
	if len(s.Documents) > 0 && len(p.Writes) == 0 {
		return p, &apptypes.SearchProjectionNoProgressError{Reason: "resource budget prevented the first row"}
	}
	if s.SourceDone && len(p.Writes) == len(s.Documents) {
		p.NextPhase = "eviction"
	}
	return p, nil
}

// PlanProjectionRetention creates a bounded, deterministic cleanup decision.
func PlanProjectionRetention(in []apptypes.ProjectionCleanupCandidate, b apptypes.SearchProjectionBudget) (apptypes.ProjectionRetentionPlan, error) {
	out := apptypes.ProjectionRetentionPlan{}
	for _, c := range in {
		wal := c.LogicalBytes * projectionWALFactor
		if out.Ledger.Rows >= b.Rows || out.Ledger.LogicalWriteBytes+c.LogicalBytes+out.Ledger.WALReservationBytes+wal > b.WriteBytes {
			break
		}
		out.Candidates = append(out.Candidates, c)
		out.Ledger.Rows++
		out.Ledger.LogicalWriteBytes += c.LogicalBytes
		out.Ledger.WALReservationBytes += wal
	}
	if len(in) > 0 && len(out.Candidates) == 0 {
		return out, &apptypes.SearchProjectionOversizeError{Class: "cleanup_bytes", Bytes: in[0].LogicalBytes * (1 + projectionWALFactor), Limit: b.WriteBytes}
	}
	return out, nil
}
func truncateProjection(v string, n int) string {
	if len(v) <= n {
		return v
	}
	v = v[:n]
	for !utf8.ValidString(v) {
		v = v[:len(v)-1]
	}
	return v
}
func projectionTokens(v string) []string {
	f := strings.FieldsFunc(v, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("_./:@+-", r)
	})
	out := f[:0]
	for _, s := range f {
		if utf8.RuneCountInString(s) >= 12 {
			var a, d, y bool
			for _, r := range s {
				if r >= 'a' && r <= 'z' {
					a = true
				} else if r >= '0' && r <= '9' {
					d = true
				} else {
					y = true
				}
			}
			if a && (d || y) {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}
