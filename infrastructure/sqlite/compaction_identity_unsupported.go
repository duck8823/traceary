//go:build !darwin && !linux

package sqlite

import (
	"os"

	"github.com/duck8823/traceary/domain"
)

func platformStoreFileIdentity(os.FileInfo) (domain.StoreFileIdentity, error) {
	return domain.StoreFileIdentity{}, ErrPreparedUpgradeUnsupported
}
