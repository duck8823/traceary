//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
	"net"
	"os"
	"path/filepath"
)

// Generated fixture wrapper only: these tests call the actual compiled
// lifecycle/endpoint producers and readers; no reconstructed reader model.
func WriteLifecycleEndpointsForTest(root, epoch string) error {
	store, err := OpenExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		return err
	}
	revision := uint64(1)
	doc := endpointReceipt{Version: 1, Revision: &revision, Epoch: epoch, RootWitness: store.metadata.witness, Endpoints: []endpointOwnedRecord{}}
	for i, name := range endpointNames {
		path := filepath.Join(root, name)
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			return xerrors.Errorf("listen fixture endpoint: %w", err)
		}
		listener.SetUnlinkOnClose(false)
		if err = os.Chmod(path, 0600); err != nil {
			_ = listener.Close()
			return xerrors.Errorf("inspect fixture endpoint: %w", err)
		}
		var stamp unix.Stat_t
		if err = unix.Lstat(path, &stamp); err != nil {
			_ = listener.Close()
			return xerrors.Errorf("inspect fixture endpoint: %w", err)
		}
		doc.Endpoints = append(doc.Endpoints, endpointRecord(name, endpointRoles[i], stamp))
		if err = listener.Close(); err != nil {
			return xerrors.Errorf("close fixture listener: %w", err)
		}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return xerrors.Errorf("encode fixture receipt: %w", err)
	}
	if err = os.WriteFile(filepath.Join(root, "endpoints.json"), data, 0600); err != nil {
		return xerrors.Errorf("write fixture receipt: %w", err)
	}
	return nil
}
func ValidateLifecycleHistoryReadersForTest(ctx context.Context, root string) error {
	store, err := OpenExecutionStore(root, MaxRegistryBytes)
	if err != nil {
		return err
	}
	return store.metadata.locked(ctx, func(fd int) error {
		if _, _, err = store.read(fd); err != nil {
			return err
		}
		if _, _, _, err = store.readControls(fd); err != nil {
			return err
		}
		if _, _, err = store.readLifecycle(fd); err != nil {
			return err
		}
		data, stamp, e := store.metadata.readMetadata(fd, "endpoints.json", maxEndpointReceiptBytes)
		if e != nil {
			return e
		}
		if stamp != nil {
			_, e = decodeEndpointReceipt(data, store.metadata.witness, "")
		}
		return e
	})
}
