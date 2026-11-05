//go:build darwin || linux

package runtime

import app "github.com/duck8823/traceary/application/runtime"

// PTYMasterForTest observes an actual fixture master; it grants no production API.
func PTYMasterForTest(child app.OwnedChild) (int, bool) {
	c, ok := child.(*ptyChild)
	if !ok {
		return -1, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fd, c.fd >= 0
}
