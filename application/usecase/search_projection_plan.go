package usecase

import (
	apptypes "github.com/duck8823/traceary/application/types"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const projectionIntegerBytes int64 = 8

// PlanProjectionBatch is pure: the same snapshot and budget produce the same plan.
func PlanProjectionBatch(s apptypes.ProjectionSnapshot, b apptypes.SearchProjectionBudget) (apptypes.ProjectionBatchPlan, error) {
	p := apptypes.ProjectionBatchPlan{GenerationID: s.Generation.GenerationID, Phase: s.Phase, ExpectedRevision: s.Generation.SourceRevision, ExpectedCheckpoint: s.Generation.Checkpoint, NextCheckpoint: s.Generation.Checkpoint, AllowRevisionDrift: s.CleanupAll, ContinueState: "rebuilding"}
	if s.CleanupAll {
		p.ContinueState = "drifted"
	}
	p.Ledger.LogicalWriteBytes = projectionCheckpointLogicalBytes(p)
	if p.Ledger.LogicalWriteBytes > b.WriteBytes {
		return p, &apptypes.SearchProjectionOversizeError{Class: "write_bytes", Bytes: p.Ledger.LogicalWriteBytes, Limit: b.WriteBytes}
	}
	if s.Phase != "source" {
		retentionBudget := b
		retentionBudget.WriteBytes -= p.Ledger.LogicalWriteBytes
		rp, err := PlanProjectionRetention(s.Cleanup, retentionBudget)
		if err != nil {
			return p, err
		}
		p.Cleanup = rp.Candidates
		p.Ledger.Rows = rp.Ledger.Rows
		p.Ledger.LogicalWriteBytes += rp.Ledger.LogicalWriteBytes
		if p.Ledger.LogicalWriteBytes > b.WriteBytes {
			return p, &apptypes.SearchProjectionOversizeError{Class: "cleanup_bytes", Bytes: p.Ledger.LogicalWriteBytes, Limit: b.WriteBytes}
		}
		if s.CleanupDone && len(s.Cleanup) == len(p.Cleanup) {
			if s.Phase == "eviction" {
				p.NextPhase = "cleanup"
			} else {
				p.NextPhase = "complete"
				p.Completed = true
				if s.CleanupAll {
					p.FinalState = "drifted"
				} else {
					p.FinalState = "complete"
				}
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
		w.LogicalBytes = projectionWriteLogicalBytes(p.GenerationID, w)
		if w.LogicalBytes > b.WriteBytes {
			return p, &apptypes.SearchProjectionOversizeError{Class: "write_bytes", Bytes: w.LogicalBytes, Limit: b.WriteBytes}
		}
		if p.Ledger.LogicalWriteBytes+w.LogicalBytes > b.WriteBytes {
			if len(p.Writes) == 0 {
				return p, &apptypes.SearchProjectionOversizeError{Class: "write_bytes", Bytes: p.Ledger.LogicalWriteBytes + w.LogicalBytes, Limit: b.WriteBytes}
			}
			break
		}
		p.Writes = append(p.Writes, w)
		p.NextCheckpoint = d.Sequence
		p.Ledger.Rows++
		p.Ledger.StoredBytes += d.StoredBytes
		p.Ledger.DecodedBytes += d.DecodedBytes
		p.Ledger.LogicalWriteBytes += w.LogicalBytes
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
		if out.Ledger.Rows >= b.Rows || out.Ledger.LogicalWriteBytes+c.LogicalBytes > b.WriteBytes {
			break
		}
		out.Candidates = append(out.Candidates, c)
		out.Ledger.Rows++
		out.Ledger.LogicalWriteBytes += c.LogicalBytes
	}
	if len(in) > 0 && len(out.Candidates) == 0 {
		return out, &apptypes.SearchProjectionOversizeError{Class: "cleanup_bytes", Bytes: in[0].LogicalBytes, Limit: b.WriteBytes}
	}
	return out, nil
}

// projectionWriteLogicalBytes counts every logical column mutation. Recent
// text is counted once for its source row and once for the external FTS index.
func projectionWriteLogicalBytes(generation string, w apptypes.ProjectionWrite) int64 {
	d := w.Document
	// Summary mutates generation/session, event_count, summary, and two
	// versions. Aggregate mutates generation/session and two counters.
	n := int64(len(generation)+len(d.SessionID))*2 + projectionIntegerBytes*5 + int64(len(w.Summary))
	if w.RetainRecent {
		n += int64(len(generation)+len(d.EventID)+len(d.CreatedAt)+len(d.Text)*2) + projectionIntegerBytes*4
	}
	for keyword := range w.Keywords {
		n += int64(len(generation)+len(d.SessionID)+len(keyword)) + projectionIntegerBytes*2
	}
	return n
}

// projectionCheckpointLogicalBytes counts checkpoint, phase, state, elapsed
// milliseconds, and the normalized timestamp updated by every batch.
func projectionCheckpointLogicalBytes(p apptypes.ProjectionBatchPlan) int64 {
	return projectionIntegerBytes*2 + int64(len(p.GenerationID)+len(p.Phase)+len(p.ContinueState)+30)
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
