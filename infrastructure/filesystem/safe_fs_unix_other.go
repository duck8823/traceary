//go:build unix && !darwin

package filesystem

// systemSymlinkWhitelist is empty on non-Darwin Unix systems. Linux and
// other Unix platforms do not expose root-level aliases equivalent to
// macOS's /tmp → /private/tmp, so the traversal applies O_NOFOLLOW at
// every component.
var systemSymlinkWhitelist = map[string]bool{}
