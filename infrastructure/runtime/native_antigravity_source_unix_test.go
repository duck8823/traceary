//go:build darwin || linux

package runtime

import (
	"context"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"os"
	"path/filepath"
	"testing"
)

func TestAgyMetadataSourceDoesNotExecuteUnknownArtifactOrRewritePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agy")
	marker := filepath.Join(t.TempDir(), "must-not-execute")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch "+nativeShellLiteral(marker)+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	path = canonical
	ctx := context.Background()
	witness, err := executableWitness(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "agy", Host: "antigravity", Executable: path, ExecutableWitness: witness, Version: "1.5.0"})
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{"TMPDIR=" + os.TempDir()}
	if err = inspectAntigravityMetadataSource(ctx, profile, environment); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal("unknown source enabled", err)
	}
	if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source probe executed", err)
	}
	for _, env := range [][]string{{"LD_PRELOAD=not-forwarded"}, {"X=a", "X=b"}, {"TMPDIR=/does-not-exist"}} {
		if err = inspectAntigravityMetadataSource(ctx, profile, env); !errors.Is(err, domain.ErrUnsupported) {
			t.Fatal("unsafe instrumentation env admitted", err)
		}
	}
}
