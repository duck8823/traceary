//go:build linux

package runtime

import (
	app "github.com/duck8823/traceary/application/runtime"
	"golang.org/x/sys/unix"
	"net"
	"os"
)

const runtimeSocketPathLimit = 107

func verifyRuntimePeer(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return app.ErrUnsafeRuntime
	}
	var peer *unix.Ucred
	var peerErr error
	if err = raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || peerErr != nil || peer == nil || peer.Uid != uint32(os.Geteuid()) {
		return app.ErrUnsafeRuntime
	}
	return nil
}
