//go:build !unix

package sqlite

import "os"

func physicalFileIdentity(info os.FileInfo) (string, string, bool) {
	return info.Name(), "portable-samefile", true
}

// os.SameFile remains the authoritative alias check on non-Unix hosts. The
// portable os.FileInfo contract does not expose a hard-link count.
func fileLinkCountOne(os.FileInfo) bool          { return true }
func filesystemFreeBytes(string) (uint64, error) { return 0, nil }
