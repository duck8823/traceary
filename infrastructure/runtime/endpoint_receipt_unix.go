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
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"unicode/utf8"
)

const maxEndpointReceiptBytes int64 = 4096

var endpointNames = [2]string{"daemon.sock", "observations.sock"}
var endpointRoles = [2]string{"management", "observations"}

type endpointOwnedRecord struct {
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	UID       *uint32 `json:"uid"`
	Dev       string  `json:"dev"`
	Ino       string  `json:"ino"`
	CtimeSec  *int64  `json:"ctime_sec"`
	CtimeNsec *int64  `json:"ctime_nsec"`
}
type endpointReceipt struct {
	Version     int                   `json:"version"`
	Revision    *uint64               `json:"revision"`
	Epoch       string                `json:"epoch"`
	RootWitness string                `json:"root_witness"`
	Endpoints   []endpointOwnedRecord `json:"endpoints"`
}

func endpointRecord(name, role string, stamp unix.Stat_t) endpointOwnedRecord {
	uid := stamp.Uid
	sec, nsec := stamp.Ctim.Unix()
	return endpointOwnedRecord{Name: name, Role: role, UID: &uid, Dev: domain.ByteOffset(uint64(stamp.Dev)).String(), Ino: domain.ByteOffset(stamp.Ino).String(), CtimeSec: &sec, CtimeNsec: &nsec}
}
func (r endpointOwnedRecord) valid(index int) bool {
	if r.Name != endpointNames[index] || r.Role != endpointRoles[index] || r.UID == nil || *r.UID != uint32(os.Geteuid()) || r.CtimeSec == nil || r.CtimeNsec == nil || *r.CtimeNsec < 0 || *r.CtimeNsec >= 1e9 {
		return false
	}
	_, e1 := domain.ParseByteOffset(r.Dev)
	_, e2 := domain.ParseByteOffset(r.Ino)
	return e1 == nil && e2 == nil
}
func (r endpointOwnedRecord) matches(s unix.Stat_t) bool {
	sec, nsec := s.Ctim.Unix()
	return runtimeSocketStat(s) && r.UID != nil && r.CtimeSec != nil && r.CtimeNsec != nil && *r.UID == s.Uid && r.Dev == domain.ByteOffset(uint64(s.Dev)).String() && r.Ino == domain.ByteOffset(s.Ino).String() && *r.CtimeSec == sec && *r.CtimeNsec == nsec
}
func sameSocketIdentity(a, b unix.Stat_t) bool {
	as, an := a.Ctim.Unix()
	bs, bn := b.Ctim.Unix()
	return a.Uid == b.Uid && a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Nlink == b.Nlink && as == bs && an == bn
}
func decodeEndpointReceipt(data []byte, witness, currentEpoch string) (endpointReceipt, error) {
	invalid := domain.ErrInvalid
	if !utf8.Valid(data) {
		return endpointReceipt{}, invalid
	}
	if err := uniqueJSONValue(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return endpointReceipt{}, invalid
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	for {
		token, err := tokens.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || token == nil {
			return endpointReceipt{}, invalid
		}
	}
	var header struct {
		Version *int `json:"version"`
	}
	if json.Unmarshal(data, &header) != nil || header.Version == nil {
		return endpointReceipt{}, invalid
	}
	if *header.Version != 1 {
		return endpointReceipt{}, domain.ErrUnsupported
	}
	var doc endpointReceipt
	if decodeControlMetadata(data, &doc) != nil || doc.Revision == nil || *doc.Revision < 1 || !validLifecycleID(doc.Epoch) || doc.Epoch == currentEpoch || doc.RootWitness != witness || len(doc.Endpoints) != 2 {
		return endpointReceipt{}, invalid
	}
	for i, r := range doc.Endpoints {
		if !r.valid(i) {
			return endpointReceipt{}, invalid
		}
	}
	if doc.Endpoints[0].Dev == doc.Endpoints[1].Dev && doc.Endpoints[0].Ino == doc.Endpoints[1].Ino {
		return endpointReceipt{}, invalid
	}
	return doc, nil
}
func (s *daemonServer) readEndpointReceipt(fd int) (*endpointReceipt, *unix.Stat_t, error) {
	data, stamp, err := s.endpointMetadata.readMetadata(fd, "endpoints.json", maxEndpointReceiptBytes)
	if err != nil {
		return nil, nil, err
	}
	if stamp == nil {
		return nil, nil, nil
	}
	doc, err := decodeEndpointReceipt(data, s.witness, s.epoch)
	if err != nil {
		return nil, nil, err
	}
	if err = s.endpointMetadata.metadataBudget(fd, "endpoints.json", int64(len(data))); err != nil {
		return nil, nil, err
	}
	return &doc, stamp, nil
}

// preflightEndpoints performs pairwise READ-ONLY checks before any unlink,
// RuntimeFactory/epoch mutation or binding. Receipt absence is never cleanup permission.
func (s *daemonServer) preflightEndpoints(ctx context.Context) error {
	metadata, err := OpenRegistryStore(s.root, MaxRegistryBytes)
	if err != nil {
		return err
	}
	s.endpointMetadata = metadata
	s.staleEndpoints = make(map[string]unix.Stat_t, 2)
	err = metadata.locked(ctx, func(fd int) error {
		doc, _, e := s.readEndpointReceipt(fd)
		if e != nil {
			return e
		}
		s.previousEndpoints = doc
		if doc != nil && *doc.Revision == math.MaxUint64 {
			return domain.ErrLimit
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i, name := range endpointNames {
		var current unix.Stat_t
		err = unix.Fstatat(s.rootFD, name, &current, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil || !runtimeSocketStat(current) {
			return app.ErrUnsafeRuntime
		}
		if s.previousEndpoints == nil {
			return app.ErrRuntimeBusy
		}
		if !s.previousEndpoints.Endpoints[i].matches(current) {
			return app.ErrUnsafeRuntime
		}
		if err = s.proveRefusedEndpoint(ctx, name, current); err != nil {
			return err
		}
		s.staleEndpoints[name] = current
	}
	return nil
}
func (s *daemonServer) proveRefusedEndpoint(parent context.Context, name string, expected unix.Stat_t) error {
	if !s.leaseCurrent() || verifyRuntimeRoot(s.root, s.witness, s.rootFD) != nil {
		return app.ErrUnsafeRuntime
	}
	ctx, cancel := context.WithTimeout(parent, min(OptionalRuntimeBudget, s.daemon.options.IOTimeout))
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(s.root, name))
	if err == nil {
		_ = connection.Close()
		return app.ErrRuntimeBusy
	}
	if !errors.Is(err, unix.ECONNREFUSED) {
		return app.ErrUnsafeRuntime
	}
	var current unix.Stat_t
	if !s.leaseCurrent() || verifyRuntimeRoot(s.root, s.witness, s.rootFD) != nil || unix.Fstatat(s.rootFD, name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameSocket(current, expected) {
		return app.ErrUnsafeRuntime
	}
	return nil
}
func (s *daemonServer) reclaimEndpoint(ctx context.Context, name string, current unix.Stat_t) error {
	expected, ok := s.staleEndpoints[name]
	if !ok || !sameSocket(current, expected) {
		return app.ErrRuntimeBusy
	}
	// Reprobe and revalidate immediately before unlink; no saved PID is consulted.
	if err := s.proveRefusedEndpoint(ctx, name, expected); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return app.ErrUnsafeRuntime
	}
	if unix.Unlinkat(s.rootFD, name, 0) != nil {
		return app.ErrUnsafeRuntime
	}
	return nil
}
func (s *daemonServer) publishEndpointReceipt(ctx context.Context) error {
	if len(s.endpoints) != 2 || !s.leaseCurrent() {
		return app.ErrUnsafeRuntime
	}
	revision := uint64(1)
	if s.previousEndpoints != nil {
		revision = *s.previousEndpoints.Revision + 1
	}
	doc := endpointReceipt{Version: 1, Revision: &revision, Epoch: s.epoch, RootWitness: s.witness, Endpoints: []endpointOwnedRecord{}}
	for _, endpoint := range s.endpoints {
		if !s.endpointCurrent(endpoint) {
			return app.ErrUnsafeRuntime
		}
		doc.Endpoints = append(doc.Endpoints, endpointRecord(endpoint.name, endpoint.role, endpoint.stamp))
	}
	data, err := json.Marshal(doc)
	if err != nil || int64(len(data)) > maxEndpointReceiptBytes {
		return domain.ErrLimit
	}
	return s.endpointMetadata.locked(ctx, func(fd int) error {
		prior, stamp, e := s.readEndpointReceipt(fd)
		if e != nil {
			return e
		}
		// Canonical comparison prevents overwriting a changed receipt between preflight
		// and publication. Only this actual daemon lifetime lease may publish a successor.
		expected, _ := json.Marshal(s.previousEndpoints)
		actual, _ := json.Marshal(prior)
		if !bytes.Equal(expected, actual) || !s.leaseCurrent() {
			return domain.ErrChanged
		}
		for _, endpoint := range s.endpoints {
			if !s.endpointCurrent(endpoint) {
				return app.ErrUnsafeRuntime
			}
		}
		return s.endpointMetadata.writeMetadata(ctx, fd, "endpoints.json", data, stamp)
	})
}
