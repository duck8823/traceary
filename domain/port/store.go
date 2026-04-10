// Package port defines repository and infrastructure interfaces
// that belong to the domain boundary.
package port

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// ErrSessionStartedEventNotFound indicates the target session has no start event.
var ErrSessionStartedEventNotFound = xerrors.New("session_started event was not found for the target session")

// StoreInitializer creates the store and applies migrations.
type StoreInitializer interface {
	Initialize(ctx context.Context, dbPath string) error
}

// EventSaver persists events.
type EventSaver interface {
	Save(ctx context.Context, dbPath string, event *model.Event) error
}

// CommandAuditSaver persists command audit events.
type CommandAuditSaver interface {
	SaveCommandAudit(ctx context.Context, dbPath string, event *model.Event, commandAudit *model.CommandAudit) error
}

// SessionSaver persists session metadata.
type SessionSaver interface {
	SaveSession(ctx context.Context, dbPath string, session *model.Session) error
}

// SessionStartedEventFinder looks up session_started events.
type SessionStartedEventFinder interface {
	FindSessionStartedEvent(ctx context.Context, dbPath string, sessionID types.SessionID) (*model.Event, error)
}

// SessionLabelUpdater updates a session label.
type SessionLabelUpdater interface {
	UpdateSessionLabel(ctx context.Context, dbPath string, sessionID types.SessionID, label string) error
}

// StoreBackupCreator creates a backup of the store.
type StoreBackupCreator interface {
	CreateBackup(ctx context.Context, dbPath string, outputPath string, overwrite bool) error
}

// StoreBackupRestorer restores a backup.
type StoreBackupRestorer interface {
	RestoreBackup(ctx context.Context, inputPath string, dbPath string, overwrite bool) error
}

// GarbageCollector removes old events.
type GarbageCollector interface {
	CollectGarbage(ctx context.Context, dbPath string, before time.Time, dryRun bool) (int, error)
}
