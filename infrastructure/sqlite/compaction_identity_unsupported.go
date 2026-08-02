//go:build !darwin && !linux

package sqlite

import (
	"errors"
	"os"

	"github.com/duck8823/traceary/domain"
)

func platformStoreFileIdentity(os.FileInfo) (domain.StoreFileIdentity, error) {
	return domain.StoreFileIdentity{}, errors.New("file identity unsupported on this operating system")
}
