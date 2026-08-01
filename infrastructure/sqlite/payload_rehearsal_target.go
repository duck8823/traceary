package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

func (a *PayloadRehearsalAdapter) inspectTarget(target, claimedLive string) (rehearsalIdentity, error) {
	if a == nil || a.configuredLivePath == "" {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	configured, err := secureFileIdentity(a.configuredLivePath)
	if err != nil {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	claimed, err := secureFileIdentity(claimedLive)
	if err != nil || !os.SameFile(configured.info, claimed.info) {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	return inspectRehearsalTarget(target, a.configuredLivePath)
}

type rehearsalIdentity struct {
	canonical string
	info      os.FileInfo
	opaque    string
	device    string
	inode     string
}

func inspectRehearsalTarget(target, live string) (rehearsalIdentity, error) {
	t, err := secureFileIdentity(target)
	if err != nil {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	l, err := secureFileIdentity(live)
	if err != nil {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	if t.canonical == l.canonical || os.SameFile(t.info, l.info) {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if aliasesExistingFile(target+suffix, live+suffix) {
			return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
		}
	}
	return t, nil
}

//nolint:wrapcheck // callers deliberately collapse filesystem details into a safe typed error.
func secureFileIdentity(path string) (rehearsalIdentity, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return rehearsalIdentity{}, err
	}
	// Reject symlinks in every existing path component, not merely the leaf.
	cur := string(filepath.Separator)
	volume := filepath.VolumeName(abs)
	if volume != "" {
		cur = volume + string(filepath.Separator)
	}
	parts := strings.Split(strings.TrimPrefix(abs, cur), string(filepath.Separator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, e := os.Lstat(cur)
		if e != nil {
			return rehearsalIdentity{}, e
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
		}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return rehearsalIdentity{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return rehearsalIdentity{}, err
	}
	if !info.Mode().IsRegular() {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	if !fileLinkCountOne(info) {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	device, inode, ok := physicalFileIdentity(info)
	if !ok {
		return rehearsalIdentity{}, ErrUnsafeRehearsalTarget
	}
	sum := sha256.Sum256([]byte(resolved))
	return rehearsalIdentity{canonical: resolved, info: info, opaque: hex.EncodeToString(sum[:]), device: device, inode: inode}, nil
}

func aliasesExistingFile(a, b string) bool {
	ai, ae := os.Lstat(a)
	bi, be := os.Lstat(b)
	if ae != nil || be != nil {
		return false
	}
	if ai.Mode()&os.ModeSymlink != 0 || bi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return os.SameFile(ai, bi)
}

func componentSnapshots(path string) ([]apptypes.PayloadRehearsalFileState, error) {
	result := make([]apptypes.PayloadRehearsalFileState, 0, 3)
	for _, component := range []struct{ name, suffix string }{{"db", ""}, {"wal", "-wal"}, {"shm", "-shm"}} {
		fi, err := os.Lstat(path + component.suffix)
		if os.IsNotExist(err) {
			result = append(result, apptypes.PayloadRehearsalFileState{Component: component.name})
			continue
		}
		if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || !fileLinkCountOne(fi) {
			return nil, ErrUnsafeRehearsalTarget
		}
		device, inode, ok := physicalFileIdentity(fi)
		if !ok {
			return nil, ErrUnsafeRehearsalTarget
		}
		s := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d:%d:%d", component.name, device, inode, physicalLinkCount(fi), fi.Size(), fi.ModTime().UnixNano())))
		result = append(result, apptypes.PayloadRehearsalFileState{Component: component.name, Exists: true, SizeBytes: fi.Size(), ModUnixNS: fi.ModTime().UnixNano(), Identity: hex.EncodeToString(s[:])})
	}
	return result, nil
}

func immutableRehearsalDSN(path string) string {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	q.Add("_pragma", "query_only(1)")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}

// Preview proves a strictly immutable, zero-write inspection.
func ensurePhysicalBackup(source, dest string) (string, error) {
	sourceAbs, _ := filepath.Abs(filepath.Clean(source))
	destAbs, _ := filepath.Abs(filepath.Clean(dest))
	if sourceAbs == destAbs {
		return "", ErrUnsafeRehearsalTarget
	}
	if sourceInfo, sourceErr := os.Stat(source); sourceErr == nil {
		if destInfo, destErr := os.Stat(dest); destErr == nil && os.SameFile(sourceInfo, destInfo) {
			return "", ErrUnsafeRehearsalTarget
		}
	}
	sourceDigest, err := fileDigest(source)
	if err != nil {
		return "", err
	}
	if existing, existingErr := fileDigest(dest); existingErr == nil {
		if existing != sourceDigest {
			return "", errors.New("existing rollback artifact does not match the copied target")
		}
		return existing, nil
	}
	if err := copyFileAtomic(source, dest); err != nil {
		return "", xerrors.Errorf("create rollback artifact: %w", err)
	}
	return fileDigest(dest)
}

func validateBackupIndependence(backup string, forbidden ...string) error {
	if err := rejectSymlinkAncestors(backup); err != nil {
		return err
	}
	backupInfo, err := os.Stat(backup)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return ErrUnsafeRehearsalTarget
	}
	if linkInfo, linkErr := os.Lstat(backup); linkErr != nil || linkInfo.Mode()&os.ModeSymlink != 0 || !backupInfo.Mode().IsRegular() || !fileLinkCountOne(backupInfo) {
		return ErrUnsafeRehearsalTarget
	}
	for _, path := range forbidden {
		info, statErr := os.Stat(path)
		if statErr == nil && os.SameFile(backupInfo, info) {
			return ErrUnsafeRehearsalTarget
		}
	}
	return nil
}

func rejectSymlinkAncestors(path string) error {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return ErrUnsafeRehearsalTarget
	}
	current := filepath.Dir(abs)
	for {
		info, statErr := os.Lstat(current)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafeRehearsalTarget
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

//nolint:wrapcheck // caller provides the safe rollback operation context.
func copyFileAtomic(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, dest); err != nil {
		return err
	}
	ok = true
	return nil
}

//nolint:wrapcheck // caller converts path-sensitive failures to fixed messages.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
