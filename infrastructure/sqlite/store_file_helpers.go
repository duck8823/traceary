package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"
)

func exactFileClone(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open clone source: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open clone destination: %w", err)
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy clone: %w", err)
	}
	if err = out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync clone: %w", err)
	}
	if err = out.Close(); err != nil {
		return fmt.Errorf("close clone destination: %w", err)
	}
	return nil
}

func fileDigest(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("digest canceled: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open digest source: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err = ctx.Err(); err != nil {
			return "", fmt.Errorf("digest canceled: %w", err)
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, err = h.Write(buf[:n]); err != nil {
				return "", fmt.Errorf("hash digest chunk: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("read digest source: %w", readErr)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writableCandidateDSN(path string, lock time.Duration) string {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", max(1, lock.Milliseconds())))
	q.Set("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()
}
