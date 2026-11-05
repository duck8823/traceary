package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

func (d localAutoStartDescriptor) RenderAutoStartDescriptor(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", xerrors.Errorf("render auto-start descriptor budget: %w", err)
	}
	if !validRuntimeRoot(d.root) || !printableAutoStartPath(d.root) {
		return "", domain.ErrUnsafe
	}
	canonical, err := filepath.EvalSymlinks(d.root)
	if err != nil || canonical != d.root {
		return "", domain.ErrUnsafe
	}
	fd, err := privateRuntimeDir(d.root, false)
	if err != nil {
		return "", err
	}
	defer func() { _ = unix.Close(fd) }()
	if err = verifyRuntimeRoot(d.root, d.witness, fd); err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", domain.ErrUnsafe
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil || !printableAutoStartPath(executable) {
		return "", domain.ErrUnsafe
	}
	witness, err := executableWitness(ctx, executable)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(d.root))
	label := "io.traceary.daemon." + hex.EncodeToString(digest[:])[:32]
	var arguments strings.Builder
	for _, arg := range []string{executable, "daemon", "serve", "--runtime-dir", d.root} {
		var escaped bytes.Buffer
		if err = xml.EscapeText(&escaped, []byte(arg)); err != nil {
			return "", domain.ErrInvalid
		}
		arguments.WriteString("<string>" + escaped.String() + "</string>\n")
	}
	result := xml.Header + `<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n" +
		`<plist version="1.0"><dict><key>Label</key><string>` + label + "</string>\n" +
		"<key>ProgramArguments</key><array>\n" + arguments.String() + "</array>\n" +
		"<key>RunAtLoad</key><true/></dict></plist>\n"
	// Re-open the named executable/root after rendering. A rename or altered
	// object during inspection must not yield a descriptor for an unverified path.
	fresh, err := executableWitness(ctx, executable)
	if err != nil {
		return "", err
	}
	if fresh != witness {
		return "", domain.ErrChanged
	}
	if err = verifyRuntimeRoot(d.root, d.witness, fd); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", xerrors.Errorf("render auto-start descriptor budget: %w", err)
	}
	return result, nil
}
