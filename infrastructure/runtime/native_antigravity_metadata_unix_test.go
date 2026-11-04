//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	domain "github.com/duck8823/traceary/domain/runtime"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// These synthetic fixtures exercise private delivery, never native qualification.
func agyMetadataFixture(t *testing.T) (domain.RunSnapshot, map[string]string, []byte) {
	t.Helper()
	project, err := domain.ProjectOf("/fixture", "/fixture/.git", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := domain.WorktreeOf(project.ID(), "/fixture", "/fixture/.git", "/store.db", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "agy-fixture", Host: "antigravity", Executable: "/native/agy", ExecutableWitness: "fixture", Version: "1.5.0"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := domain.TargetOf(worktree, string(profile.ID()))
	if err != nil {
		t.Fatal(err)
	}
	id := domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}
	descriptor, err := domain.LaunchDescriptorOf(id, target, profile)
	if err != nil {
		t.Fatal(err)
	}
	hints := map[string]string{"TRACEARY_RUNTIME_RUN_ID": id.Run, "TRACEARY_RUNTIME_GENERATION": id.Generation, "TRACEARY_RUNTIME_EPOCH": id.Epoch, "TRACEARY_DB_PATH": "/store.db"}
	return descriptor.Snapshot(), hints, []byte(agyStatuslineJSON("Gemini 4 Argon (Medium)"))
}
func TestAgyMetadataSlotCountsEveryDeliveryAndRetainsABAAmbiguity(t *testing.T) {
	snapshot, hints, data := agyMetadataFixture(t)
	ctx := context.Background()
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slot.close(ctx) })
	sink := AntigravityStatuslineSink{hints: hints}
	if _, err = slot.read(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("absent delivery became evidence", err)
	}
	for i, payload := range [][]byte{data, data, bytes.ReplaceAll(data, []byte("(Medium)"), []byte("(High)")), data} {
		if err = sink.Observe(ctx, slot.metadata.root, bytes.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
		got, e := slot.read(ctx)
		if e != nil || got.Sequence != uint64(i+1) {
			t.Fatal(got, e)
		}
		if i < 2 {
			if got.Status != "observed" || got.Uncertain {
				t.Fatal("equal delivery invalidated", got)
			}
		} else if got.Status != "unavailable" || !got.ModelChanged || !got.Uncertain {
			t.Fatal("A-B-A rearmed", got)
		}
	}
	record, e := slot.read(ctx)
	if e != nil || record.Latest.Model != "gemini-4-argon-medium" || record.FirstModel != "gemini-4-argon-medium" {
		t.Fatal(record, e)
	}
	for _, name := range []string{"intent.json", "observation.json"} {
		raw, e := os.ReadFile(filepath.Join(slot.metadata.root, name))
		if e != nil || bytes.Contains(raw, []byte("PRIVATE_")) || bytes.Contains(raw, []byte("example.invalid")) {
			t.Fatal("private data retained", e)
		}
	}
}
func TestAgyMetadataInitialAndInvalidDeliveryNeverRearm(t *testing.T) {
	snapshot, hints, data := agyMetadataFixture(t)
	ctx := context.Background()
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slot.close(ctx) })
	sink := AntigravityStatuslineSink{hints: hints}
	initial := bytes.ReplaceAll(data, []byte(agyStatuslineSession), nil)
	if err = sink.Observe(ctx, slot.metadata.root, bytes.NewReader(initial)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	record, e := slot.read(ctx)
	if e != nil || record.Sequence != 1 || record.Status != "initializing" || record.Uncertain || record.Latest != (antigravityStatuslineSnapshot{}) {
		t.Fatal(record, e)
	}
	if err = sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err = sink.Observe(ctx, slot.metadata.root, strings.NewReader(`{"version":"PRIVATE_BODY"}`)); err == nil {
		t.Fatal("invalid accepted")
	}
	if err = sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	record, e = slot.read(ctx)
	if e != nil || record.Sequence != 4 || record.Status != "unavailable" || !record.Uncertain {
		t.Fatal("unknown rearmed", record, e)
	}
}

type agyUnreadInput struct{ reads int }

func (r *agyUnreadInput) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }
func TestAgyMetadataForeignHintsCannotReadOrPoison(t *testing.T) {
	snapshot, hints, _ := agyMetadataFixture(t)
	ctx := context.Background()
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slot.close(ctx) })
	hints["TRACEARY_RUNTIME_GENERATION"] = strings.Repeat("9", 32)
	input := &agyUnreadInput{}
	if err = (AntigravityStatuslineSink{hints: hints}).Observe(ctx, slot.metadata.root, input); !errors.Is(err, domain.ErrChanged) || input.reads != 0 {
		t.Fatal("foreign input consumed", err, input.reads)
	}
	if _, err = slot.read(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign callback poisoned owner", err)
	}
}
func TestAgyMetadataConcurrentLocalSequenceAndPostWaitCleanup(t *testing.T) {
	snapshot, hints, data := agyMetadataFixture(t)
	ctx := context.Background()
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	root := slot.metadata.root
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	sink := AntigravityStatuslineSink{hints: hints}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if e := sink.Observe(ctx, root, bytes.NewReader(data)); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	record, err := slot.finish(ctx)
	if err != nil || record.Sequence != 16 || !record.Closed || record.Uncertain {
		t.Fatal(record, err)
	}
	if _, err = os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("known root retained", err)
	}
	input := &agyUnreadInput{}
	if err = sink.Observe(ctx, root, input); err == nil || input.reads != 0 {
		t.Fatal("late callback recreated/read", err, input.reads)
	}
	if _, err = os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late root recreated", err)
	}
}
func TestAgyMetadataUnknownFileCleanupIsNonDestructive(t *testing.T) {
	snapshot, _, _ := agyMetadataFixture(t)
	ctx := context.Background()
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	root := slot.metadata.root
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	other := filepath.Join(root, "foreign.txt")
	if err = os.WriteFile(other, []byte("retain owned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = slot.finish(ctx); err == nil {
		t.Fatal("unknown file deleted")
	}
	if data, e := os.ReadFile(other); e != nil || string(data) != "retain owned fixture" {
		t.Fatal("unknown content changed", e)
	}
}

func TestAgyMetadataIdentityProjectAndAccountABANeverClearUncertainty(t *testing.T) {
	for _, kind := range []string{"session", "project", "account"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, hints, data := agyMetadataFixture(t)
			ctx := context.Background()
			slot, err := newAgyMetadataSlot(ctx, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = slot.close(ctx) })
			sink := AntigravityStatuslineSink{hints: hints}
			other := append([]byte{}, data...)
			switch kind {
			case "session":
				other = bytes.ReplaceAll(data, []byte(agyStatuslineSession), []byte("11112233-4455-4677-8899-aabbccddeeff"))
			case "project":
				other = bytes.ReplaceAll(data, []byte(`"project_dir":"/project"`), []byte(`"project_dir":"/other"`))
			case "account":
				other = bytes.ReplaceAll(data, []byte(`"email":"PRIVATE_ACCOUNT@example.invalid"`), []byte(`"email":""`))
			}
			for _, input := range [][]byte{data, other, data} {
				if err = sink.Observe(ctx, slot.metadata.root, bytes.NewReader(input)); err != nil {
					t.Fatal(err)
				}
			}
			record, e := slot.read(ctx)
			if e != nil || record.Sequence != 3 || record.Status != "unavailable" || !record.Uncertain {
				t.Fatal("A-B-A lost", record, e)
			}
		})
	}
}

type agyErrorReader struct{}

func (agyErrorReader) Read([]byte) (int, error) { return 0, errors.New("PRIVATE_BODY") }
func TestAgyMetadataPartialReadAndSequenceOverflowStayUnavailable(t *testing.T) {
	snapshot, hints, data := agyMetadataFixture(t)
	ctx := context.Background()
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slot.close(ctx) })
	sink := AntigravityStatuslineSink{hints: hints}
	if err = sink.Observe(ctx, slot.metadata.root, io.MultiReader(bytes.NewReader(data), agyErrorReader{})); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
	record, err := slot.read(ctx)
	if err != nil || record.Status != "unavailable" || record.Latest != (antigravityStatuslineSnapshot{}) {
		t.Fatal(record, err)
	}
	err = slot.metadata.locked(ctx, func(fd int) error {
		r, stamp, e := readAgyMetadataObservation(slot.metadata, fd, slot.intent)
		if e != nil {
			return e
		}
		r.Sequence = math.MaxUint64
		encoded, e := json.Marshal(r)
		if e != nil {
			return domain.ErrInvalid
		}
		return slot.metadata.writeMetadata(ctx, fd, "observation.json", encoded, stamp)
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = sink.Observe(ctx, slot.metadata.root, bytes.NewReader(data)); !errors.Is(err, domain.ErrLimit) {
			t.Fatal("overflow wrapped", err)
		}
	}
	record, err = slot.read(ctx)
	if err != nil || record.Sequence != math.MaxUint64 || record.Status != "unavailable" || !record.Uncertain {
		t.Fatal(record, err)
	}
}
func TestAgyMetadataSettingsPreserveOnlyApprovedPerProcessDisplayOverride(t *testing.T) {
	snapshot, _, _ := agyMetadataFixture(t)
	ctx := context.Background()
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slot.close(ctx) })
	path, err := slot.writeSettings(ctx, "/native/Traceary's CLI")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(raw, &settings) != nil || len(settings) != 1 || settings["statusLine"] == nil {
		t.Fatal("unexpected settings", string(raw))
	}
	var status struct {
		Command string `json:"command"`
		Stack   bool   `json:"stack_with_default"`
		Enabled bool   `json:"enabled"`
	}
	if json.Unmarshal(settings["statusLine"], &status) != nil || !status.Stack || !status.Enabled || !strings.Contains(status.Command, nativeShellLiteral("/native/Traceary's CLI")) || !strings.Contains(status.Command, " runtime native-antigravity-statusline --slot "+nativeShellLiteral(slot.metadata.root)) {
		t.Fatal(status)
	}
	plan := agyMetadataLaunchPlan{slot: slot, settings: path}
	if got := plan.args(); len(got) != 2 || got[0] != "--settings" || got[1] != path {
		t.Fatal("model/auth/native argv changed", got)
	}
	if err = os.WriteFile(path, []byte("changed generated settings"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = slot.close(ctx); !errors.Is(err, domain.ErrChanged) {
		t.Fatal("changed settings deleted", err)
	}
	if got, e := os.ReadFile(path); e != nil || string(got) != "changed generated settings" {
		t.Fatal(got, e)
	}
	// The retained directory contains only this test's files.
	t.Cleanup(func() { _ = os.RemoveAll(slot.metadata.root) })
}
func TestAgyMetadataPrivateFileGuardsBeforeInputAndNoRecoveryAuthority(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, hints, _ := agyMetadataFixture(t)
			ctx := context.Background()
			slot, err := newAgyMetadataSlot(ctx, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(slot.metadata.root) })
			path := filepath.Join(slot.metadata.root, "intent.json")
			other := filepath.Join(t.TempDir(), "other")
			switch kind {
			case "symlink":
				if err = os.Rename(path, other); err == nil {
					err = os.Symlink(other, path)
				}
			case "hardlink":
				err = os.Link(path, other)
			case "public":
				err = os.Chmod(path, 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			input := &agyUnreadInput{}
			if err = (AntigravityStatuslineSink{hints: hints}).Observe(ctx, slot.metadata.root, input); err == nil || input.reads != 0 {
				t.Fatal("unsafe intent read", err, input.reads)
			}
		})
	}
	snapshot, _, _ := agyMetadataFixture(t)
	ctx := context.Background()
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	owner := &ownedAntigravityMetadata{slot: slot}
	if _, e := owner.last(ctx); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("missing local delivery became binding", e)
	}
	child := &ptyChild{agyMetadata: owner, done: make(chan struct{})}
	owner.finish()
	if _, err = child.RecoveryBinding(ctx); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal("metadata granted recovery", err)
	}
	if child.exitObserved.Load() {
		t.Fatal("metadata close granted process exit")
	}
	select {
	case <-child.done:
		t.Fatal("metadata finished child Wait")
	default:
	}
}

func FuzzAgyMetadataTransition(f *testing.F) {
	f.Add([]byte(agyStatuslineJSON("Gemini 4 Argon (Medium)")))
	f.Add([]byte(`{"version":"1.5.0"}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		snapshot, _, _ := agyMetadataFixture(t)
		r := recordLaunch(snapshot)
		intent := agyMetadataIntent{Version: 1, Witness: strings.Repeat("a", 64), Plan: agyMetadataPlan{r.Identity, r.Worktree, r.Profile}}
		native, decodeErr := decodeAntigravityStatusline(input)
		first, _ := (agyMetadataObservationRecord{}).next(intent, native, decodeErr)
		if !first.valid(intent) || first.Sequence != 1 {
			t.Fatal("invalid local transition")
		}
		second, _ := first.next(intent, native, decodeErr)
		if !second.valid(intent) || second.Sequence != 2 || first.Uncertain && !second.Uncertain {
			t.Fatal("unknown rearmed or sequence collapsed")
		}
	})
}
