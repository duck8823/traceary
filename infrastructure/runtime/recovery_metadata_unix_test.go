//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// These fixtures exercise the private on-disk protocol without creating native
// processes or touching any real user metadata.
func recoveryMetadataFixture(t *testing.T) (*MetadataStore, string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "metadata")
	if _, err := NewRegistryStore(root, MaxRegistryBytes); err != nil {
		t.Fatal(err)
	}
	doc, execution := recoveryCodecFixture(t)
	for name, value := range map[string]any{"execution.json": execution, "recovery.json": doc} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := OpenMetadataStore(root, MaxRegistryBytes)
	if err != nil {
		t.Fatal(err)
	}
	return store, root
}

func TestRecoveryMetadataExportAndConfirmedForget(t *testing.T) {
	store, root := recoveryMetadataFixture(t)
	ctx := context.Background()
	before, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.RecoveryRecords != 1 || before.ResumeAttempts != 0 || before.RecoveryRevision != "1" {
		t.Fatalf("missing recovery counts: %+v", before)
	}
	data, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"synthetic-session", "/native/session.jsonl", "synthetic-model", "synthetic-provider", "native_namespace_witness"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("export disclosed native data: %s", forbidden)
		}
	}
	confirmation, err := domain.MetadataForgetConfirmationOf(before.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.Forget(ctx, confirmation)
	if err != nil || result.Outcome != "forgotten" {
		t.Fatalf("forget: %+v %v", result, err)
	}
	after, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.RecoveryRecords != 0 || after.ResumeAttempts != 0 || after.RecoveryRevision != "2" || len(after.Runs) != 0 || after.Digest == before.Digest {
		t.Fatalf("incomplete forget: %+v", after)
	}
	if _, err := os.Stat(filepath.Join(root, "recovery.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryMetadataCorruptionAndChangedDigestFailClosed(t *testing.T) {
	store, root := recoveryMetadataFixture(t)
	ctx := context.Background()
	before, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := recoveryCodecFixture(t)
	*doc.Revision = 2
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "recovery.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	confirmation, err := domain.MetadataForgetConfirmationOf(before.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Forget(ctx, confirmation); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("stale preview accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "recovery.json"), []byte(`{"version":9}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Export(ctx); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("unknown recovery omitted: %v", err)
	}
	if _, err := store.Forget(ctx, confirmation); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("unknown recovery forgotten: %v", err)
	}
}

func TestRecoveryMetadataPartialForgetIsUncertainAndRemainsAuditable(t *testing.T) {
	store, _ := recoveryMetadataFixture(t)
	ctx := context.Background()
	before, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := domain.MetadataForgetConfirmationOf(before.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// Recovery must disappear before execution. Failure at the second replacement
	// must leave a valid audit, not dangling recovery references or false success.
	FailMetadataCommitForTest(store, 2, false)
	if _, err := store.Forget(ctx, confirmation); !errors.Is(err, app.ErrMetadataForgetUncertain) {
		t.Fatalf("partial success: %v", err)
	}
	after, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.RecoveryRecords != 0 || len(after.Runs) != 1 {
		t.Fatalf("unsafe removal order: %+v", after)
	}
}

func TestRecoveryMetadataForgetRevisionOverflowHasNoEffects(t *testing.T) {
	store, root := recoveryMetadataFixture(t)
	ctx := context.Background()
	doc, _ := recoveryCodecFixture(t)
	*doc.Revision = math.MaxUint64
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "recovery.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := domain.MetadataForgetConfirmationOf(before.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Forget(ctx, confirmation); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("overflow accepted: %v", err)
	}
	after, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest != after.Digest {
		t.Fatal("overflow partially changed metadata")
	}
}

func TestRecoveryMetadataRejectsUnsafeFileIdentity(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public mode"} {
		t.Run(kind, func(t *testing.T) {
			store, root := recoveryMetadataFixture(t)
			path := filepath.Join(root, "recovery.json")
			other := filepath.Join(filepath.Dir(root), "fixture-copy")
			switch kind {
			case "symlink":
				if err := os.Rename(path, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, other); err != nil {
					t.Fatal(err)
				}
			case "public mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Export(context.Background()); !errors.Is(err, domain.ErrUnsafe) {
				t.Fatalf("unsafe recovery file accepted: %v", err)
			}
		})
	}
}
