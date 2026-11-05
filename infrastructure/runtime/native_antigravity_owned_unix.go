//go:build darwin || linux

package runtime

import (
	"context"
	"sync"
	"time"
)

// This lifetime tracks last local metadata delivery only; it deliberately does
// not implement recovery binding or native-current/process authority.
type ownedAntigravityMetadata struct {
	slot     *agyMetadataSlot
	mu       sync.Mutex
	finished bool
	final    agyMetadataObservation
	finalErr error
}

func (s *ownedAntigravityMetadata) finish() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	s.final, s.finalErr = s.slot.finish(ctx)
	s.finished = true
}
func (s *ownedAntigravityMetadata) last(ctx context.Context) (agyMetadataObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return s.final, s.finalErr
	}
	return s.slot.read(ctx)
}
