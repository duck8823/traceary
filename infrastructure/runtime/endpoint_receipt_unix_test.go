//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

func receiptRoot(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if goruntime.GOOS == "darwin" {
		base = "/private/tmp"
	}
	root, err := os.MkdirTemp(base, "tep-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
func prepareReceiptFixture(t *testing.T, root string) *daemonServer {
	t.Helper()
	d, err := NewLocalDaemon(nil, DaemonOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := prepareDaemon(context.Background(), d, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s.rootFD >= 0 {
			s.shutdown()
		}
	})
	return s
}
func crashReceiptFixture(s *daemonServer, keepListeners bool) {
	if !keepListeners {
		for _, endpoint := range s.endpoints {
			_ = endpoint.listener.Close()
		}
	}
	s.cancel()
	s.release()
	s.rootFD = -1
	s.leaseFD = -1
}
func TestEndpointReceiptOwnedStalePairRestartsAndRefusesForeignPairBeforeMutation(t *testing.T) {
	for _, mode := range []string{"owned-stale", "foreign-second", "active", "missing-receipt", "changed-ctime"} {
		t.Run(mode, func(t *testing.T) {
			root := receiptRoot(t)
			old := prepareReceiptFixture(t, root)
			var before unix.Stat_t
			if err := unix.Lstat(filepath.Join(root, "daemon.sock"), &before); err != nil {
				t.Fatal(err)
			}
			receiptPath := filepath.Join(root, "endpoints.json")
			receipt, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal("no durable ownership receipt", err)
			}
			crashReceiptFixture(old, mode == "active")
			defer func() {
				for _, e := range old.endpoints {
					_ = e.listener.Close()
				}
			}()
			switch mode {
			case "foreign-second":
				if err = os.Remove(filepath.Join(root, "observations.sock")); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(root, "observations.sock"), []byte("foreign test-owned data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-receipt":
				if err = os.Remove(receiptPath); err != nil {
					t.Fatal(err)
				}
			case "changed-ctime":
				if err = os.Chmod(filepath.Join(root, "daemon.sock"), 0640); err != nil {
					t.Fatal(err)
				}
				if err = os.Chmod(filepath.Join(root, "daemon.sock"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			d, err := NewLocalDaemon(nil, DaemonOptions{RuntimeFactory: func(context.Context, string, string) (*app.Runtime, error) {
				called = true
				return nil, app.ErrRuntimeUnsupported
			}})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "owned-stale" {
				d, err = NewLocalDaemon(nil, DaemonOptions{})
				if err != nil {
					t.Fatal(err)
				}
			}
			next, err := prepareDaemon(context.Background(), d, root)
			if mode == "owned-stale" {
				if err != nil {
					t.Fatal("proven owned stale pair refused", err)
				}
				if next.epoch == old.epoch {
					t.Fatal("restart reused epoch")
				}
				next.shutdown()
				return
			}
			if err == nil {
				next.shutdown()
				t.Fatal("unknown/foreign/active endpoint accepted")
			}
			if called {
				t.Fatal("unsafe endpoint mutated lifecycle through RuntimeFactory")
			}
			var after unix.Stat_t
			if err = unix.Lstat(filepath.Join(root, "daemon.sock"), &after); err != nil || after.Dev != before.Dev || after.Ino != before.Ino {
				t.Fatal("pairwise refusal removed first socket")
			}
			if mode != "missing-receipt" {
				afterReceipt, e := os.ReadFile(receiptPath)
				if e != nil || !bytes.Equal(receipt, afterReceipt) {
					t.Fatal("refusal changed endpoint receipt")
				}
			}
		})
	}
}

func TestEndpointReceiptStrictCodecRejectsMalformedAndUnsafeEvidence(t *testing.T) {
	root := receiptRoot(t)
	s := prepareReceiptFixture(t, root)
	data, err := os.ReadFile(filepath.Join(root, "endpoints.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeEndpointReceipt(data, s.witness, strings.Repeat("f", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err = decodeEndpointReceipt(data, s.witness, s.epoch); err == nil {
		t.Fatal("same-epoch receipt accepted for reclaim")
	}
	if _, err = decodeEndpointReceipt(data, "other-root", strings.Repeat("f", 32)); err == nil {
		t.Fatal("foreign-root receipt accepted")
	}
	for _, body := range [][]byte{[]byte(`{"version":2,"future":true}`), []byte(`{"version":1,"version":1}`), []byte(`{"version":1,"revision":null}`)} {
		if _, err = decodeEndpointReceipt(body, s.witness, strings.Repeat("f", 32)); err == nil {
			t.Fatal("invalid receipt accepted")
		}
	}
	var original endpointReceipt
	if err = json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*endpointReceipt){
		func(d *endpointReceipt) { d.Endpoints = d.Endpoints[:1] },
		func(d *endpointReceipt) { d.Endpoints[0].UID = nil },
		func(d *endpointReceipt) { d.Endpoints[0].Dev = "00" },
		func(d *endpointReceipt) { d.Endpoints[0].Name = "../foreign.sock" },
		func(d *endpointReceipt) { d.Endpoints[0].Role = "observations" },
		func(d *endpointReceipt) {
			d.Endpoints[1].Dev = d.Endpoints[0].Dev
			d.Endpoints[1].Ino = d.Endpoints[0].Ino
		},
	} {
		var invalid endpointReceipt
		if err = json.Unmarshal(data, &invalid); err != nil {
			t.Fatal(err)
		}
		mutate(&invalid)
		body, e := json.Marshal(invalid)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = decodeEndpointReceipt(body, s.witness, strings.Repeat("f", 32)); e == nil {
			t.Fatal("malformed ownership evidence accepted")
		}
	}
}
func TestEndpointReceiptVersionDiscrimination(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want error
	}{
		{"future", `{"version":2,"future":true}`, domain.ErrUnsupported},
		{"future_null", `{"version":2,"future":null}`, domain.ErrUnsupported},
		{"future_nested_null", `{"version":2,"future":{"value":null}}`, domain.ErrUnsupported},
		{"future_duplicate", `{"version":2,"future":null,"future":true}`, domain.ErrInvalid},
		{"future_trailing", `{"version":2} {}`, domain.ErrInvalid},
		{"missing_version", `{"future":null}`, domain.ErrInvalid},
		{"null_version", `{"version":null}`, domain.ErrInvalid},
		{"v1_null", `{"version":1,"revision":null}`, domain.ErrInvalid},
		{"v1_unknown", `{"version":1,"future":true}`, domain.ErrInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeEndpointReceipt([]byte(tt.body), "root", strings.Repeat("f", 32))
			if !errors.Is(err, tt.want) {
				t.Fatalf("decode error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestEndpointReceiptFailureDoesNotPublishEpochOrIgnoreSharedBudget(t *testing.T) {
	root := receiptRoot(t)
	// A private sparse sibling consumes the envelope without supplying any socket
	// ownership. Publication must fail before RuntimeFactory/epoch mutation.
	file, err := os.OpenFile(filepath.Join(root, "history.json"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(MaxRegistryBytes - 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	called := false
	d, err := NewLocalDaemon(nil, DaemonOptions{RuntimeFactory: func(context.Context, string, string) (*app.Runtime, error) {
		called = true
		return nil, app.ErrRuntimeUnsupported
	}})
	if err != nil {
		t.Fatal(err)
	}
	if s, e := prepareDaemon(context.Background(), d, root); e == nil {
		s.shutdown()
		t.Fatal("over-budget endpoint publication allowed")
	} else if !errors.Is(e, domain.ErrLimit) {
		t.Fatal("wrong budget failure", e)
	}
	if called {
		t.Fatal("factory published epoch before ownership receipt")
	}
	for _, name := range []string{"daemon.sock", "observations.sock", "lifecycle.json", "endpoints.json", "registry.json"} {
		if _, err = os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed publication left ready/metadata state", name, err)
		}
	}
	second := receiptRoot(t)
	store, err := NewExecutionStore(second, 3000)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := os.OpenFile(filepath.Join(second, "endpoints.json"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = sibling.Truncate(2990); err != nil {
		t.Fatal(err)
	}
	_ = sibling.Close()
	if err = store.SetAutoStartOptIn(context.Background(), true); !errors.Is(err, domain.ErrLimit) {
		t.Fatal("lifecycle writer ignored endpoint receipt budget", err)
	}
}
