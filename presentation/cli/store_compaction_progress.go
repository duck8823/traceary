package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/duck8823/traceary/domain"
)

var compactBuildProgressInterval = 10 * time.Second

type cliCompactionProgress struct {
	w   io.Writer
	now func() time.Time
}

func newCLICompactionProgress(w io.Writer) cliCompactionProgress {
	return cliCompactionProgress{w: w, now: time.Now}
}

func (p cliCompactionProgress) OnPhase(phase domain.CompactionPhase, destinationBytes uint64) {
	if p.w == nil {
		return
	}
	if phase == domain.CompactionCandidatePrepared && destinationBytes > 0 {
		_, _ = fmt.Fprintf(p.w, "%s\n", localizef(
			"compact: %s (replica build starting, ~%s)",
			"compact: %s (replica 構築開始, 約 %s)",
			phase, formatCompactBytes(destinationBytes),
		))
		return
	}
	_, _ = fmt.Fprintf(p.w, "compact: %s\n", phase)
}

func (p cliCompactionProgress) WatchBuild(ctx context.Context, candidatePath string, destinationBytes uint64) func() {
	if p.w == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(compactBuildProgressInterval)
		defer ticker.Stop()
		p.emitBuildProgress(candidatePath, destinationBytes)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				p.emitBuildProgress(candidatePath, destinationBytes)
			}
		}
	}()
	return func() { close(done) }
}

func (p cliCompactionProgress) emitBuildProgress(candidatePath string, destinationBytes uint64) {
	info, err := os.Stat(candidatePath)
	if err != nil {
		return
	}
	written := uint64(info.Size())
	if destinationBytes == 0 {
		_, _ = fmt.Fprintf(p.w, "%s\n", localizef(
			"compact: build %s written",
			"compact: 構築 %s 書き込み済み",
			formatCompactBytes(written),
		))
		return
	}
	_, _ = fmt.Fprintf(p.w, "%s\n", localizef(
		"compact: build %s / %s (%s)",
		"compact: 構築 %s / %s (%s)",
		formatCompactBytes(written),
		formatCompactBytes(destinationBytes),
		formatCompactPercent(written, destinationBytes),
	))
}

func formatCompactPercent(written, destination uint64) string {
	if destination == 0 {
		return "0%"
	}
	if written >= destination {
		return ">100%"
	}
	return fmt.Sprintf("%d%%", written*100/destination)
}

func formatCompactBytes(n uint64) string {
	const giB = 1024 * 1024 * 1024
	const miB = 1024 * 1024
	switch {
	case n >= giB:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(giB))
	case n >= miB:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(miB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
