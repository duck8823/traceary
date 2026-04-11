package sqlite

import (
	"io/fs"
	"strings"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
)

// Store combines all persistence interfaces provided by the SQLite layer.
type Store interface {
	model.EventRepository
	model.SessionRepository
	application.StoreManager
	queryservice.EventQueryService
	queryservice.SessionQueryService
}

// NewStore creates a new SQLite-backed Store bound to the given database path.
func NewStore(dbPath string, migrations fs.FS) Store {
	return &Datasource{dbPath: strings.TrimSpace(dbPath), migrations: migrations}
}
