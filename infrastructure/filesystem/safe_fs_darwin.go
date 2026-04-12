//go:build darwin

package filesystem

// systemSymlinkWhitelist lists the root-level symbolic links exposed by
// macOS that must be traversed without O_NOFOLLOW. /tmp, /var, and /etc
// are root-managed aliases to /private/tmp, /private/var, and
// /private/etc respectively and cannot be substituted by a non-
// privileged attacker.
var systemSymlinkWhitelist = map[string]bool{
	"/var": true,
	"/tmp": true,
	"/etc": true,
}
