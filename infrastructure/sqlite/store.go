package sqlite

import (
	"context"
	"io/fs"
	"strings"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// Store provides all persistence capabilities for the SQLite layer.
type Store struct {
	EventRepository model.EventRepository
	SessionRepository model.SessionRepository
	StoreManager application.StoreManager
	EventQueryService queryservice.EventQueryService
	SessionQueryService queryservice.SessionQueryService
}

// sessionRepositoryAdapter adapts Datasource to model.SessionRepository,
// mapping Save to the underlying SaveSession to avoid a method name
// collision with EventRepository.Save.
type sessionRepositoryAdapter struct{ ds *Datasource }

func (a *sessionRepositoryAdapter) Save(ctx context.Context, session *model.Session) error {
	return a.ds.SaveSession(ctx, session)
}

func (a *sessionRepositoryAdapter) FindByID(ctx context.Context, sessionID types.SessionID) (types.Optional[*model.Session], error) {
	return a.ds.FindByID(ctx, sessionID)
}

// NewStore creates a new SQLite-backed Store bound to the given database path.
func NewStore(dbPath string, migrations fs.FS) *Store {
	ds := &Datasource{dbPath: strings.TrimSpace(dbPath), migrations: migrations}
	return &Store{
		EventRepository:     ds,
		SessionRepository:   &sessionRepositoryAdapter{ds: ds},
		StoreManager:        ds,
		EventQueryService:   ds,
		SessionQueryService: ds,
	}
}
