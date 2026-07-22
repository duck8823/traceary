//go:build !unix

package filesystem

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// FileRetentionDatasource rejects apply on platforms without fd-relative primitives.
type FileRetentionDatasource struct{}

// NewFileRetentionDatasource creates the unsupported-platform adapter.
func NewFileRetentionDatasource() *FileRetentionDatasource { return &FileRetentionDatasource{} }

func (datasource *FileRetentionDatasource) InspectFileRetention(context.Context, apptypes.FileRetentionInventoryRequest) (apptypes.FileRetentionInventorySnapshot, error) {
	return apptypes.FileRetentionInventorySnapshot{}, xerrors.New("file retention requires Unix directory-relative filesystem primitives")
}

func (datasource *FileRetentionDatasource) ApplyFileRetention(context.Context, apptypes.FileRetentionPlan, string, time.Time) (apptypes.FileRetentionApplyResult, error) {
	return apptypes.FileRetentionApplyResult{}, xerrors.New("file retention apply requires Unix directory-relative filesystem primitives")
}
