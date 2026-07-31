//go:build !unix

package filesystem

import "os"

// kimiUsageOpenFlags is a plain read-only open: the Unix-only O_NONBLOCK and
// O_CLOEXEC flags do not exist on this platform.
const kimiUsageOpenFlags = os.O_RDONLY
