//go:build darwin

package runtime

import (
	app "github.com/duck8823/traceary/application/runtime"
	"golang.org/x/sys/unix"
	"net"
	"os"
)

const (
	runtimeSocketPathLimit = 103 // sockaddr_un.sun_path includes a terminating NUL.
	darwinXucredVersion    = 0   // Native sys/ucred.h ABI; reject unknown layouts.
)

func verifyRuntimePeer(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return app.ErrUnsafeRuntime
	}
	var peer *unix.Xucred
	var peerErr error
	if err = raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) }); err != nil || peerErr != nil || peer == nil || peer.Version != darwinXucredVersion || peer.Uid != uint32(os.Geteuid()) {
		return app.ErrUnsafeRuntime
	}
	return nil
}
