//go:build darwin || linux

package sqlite

import (
	"errors"
	"os"
	"syscall"

	"github.com/duck8823/traceary/domain"
)

func platformStoreFileIdentity(info os.FileInfo) (domain.StoreFileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return domain.StoreFileIdentity{}, errors.New("file identity unsupported")
	}
	return domain.StoreFileIdentity{
		Device:  uint64(stat.Dev),
		Inode:   uint64(stat.Ino),
		Size:    info.Size(),
		ModUnix: info.ModTime().UnixNano(),
		Mode:    uint32(info.Mode().Perm()),
		Links:   uint64(stat.Nlink),
	}, nil
}
