//go:build linux

package runtime

import (
	"context"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestPTYKernelReapPolicyRefusesUnsafeDisposition(t *testing.T) {
	for _, mode := range []string{"ignored", "no_cld_wait"} {
		t.Run(mode, func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "-test.run=^TestPTYKernelReapPolicyHelper$")
			command.Env = []string{"TRACEARY_REAP_POLICY_HELPER=" + mode, "GORACE=atexit_sleep_ms=0"}
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("isolated negative policy fixture: %v %.500s", err, out)
			}
		})
	}
}
func TestPTYKernelReapPolicyHelper(t *testing.T) {
	mode := os.Getenv("TRACEARY_REAP_POLICY_HELPER")
	if mode == "" {
		t.Skip("isolated helper only")
	}
	if mode == "ignored" {
		signal.Ignore(syscall.SIGCHLD)
	} else {
		// Test-only mutation in an isolated helper: kernel NOCLDWAIT without Go Ignore.
		// No children are spawned after this point; the parent process is untouched.
		switch runtime.GOARCH {
		case "amd64", "arm64", "386", "arm", "ppc64", "ppc64le", "s390x", "riscv64", "loong64":
		default:
			t.Skip("unverified signal ABI")
		}
		var action [8]uintptr
		action[1] = 2
		_, _, err := unix.RawSyscall6(unix.SYS_RT_SIGACTION, uintptr(syscall.SIGCHLD), uintptr(unsafe.Pointer(&action)), 0, 8, 0, 0)
		if err != 0 {
			t.Fatal("unable to establish isolated kernel policy fixture")
		}
		if signal.Ignored(syscall.SIGCHLD) {
			t.Fatal("fixture must exercise actual kernel flags rather than Go Ignore")
		}
	}
	if err := checkReapPolicy(context.Background()); !errors.Is(err, domain.ErrUnsafe) {
		t.Fatal("unsafe kernel reaping policy admitted")
	}
}
