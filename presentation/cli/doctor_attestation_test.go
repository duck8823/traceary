package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/attestation"
	"github.com/duck8823/traceary/presentation/cli"
)

type stubAttestationAnchorInspector struct {
	calls int
	last  application.AttestationAnchorInspectOptions
	state application.AttestationAnchorState
	err   error
}

func (s *stubAttestationAnchorInspector) InspectAttestationAnchor(
	_ context.Context,
	opts application.AttestationAnchorInspectOptions,
) (application.AttestationAnchorState, error) {
	s.calls++
	s.last = opts
	return s.state, s.err
}

func TestRootCLI_DoctorAttestationAnchorRelations(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cli.SetUserHomeDirFunc(func() (string, error) { return homeDir, nil })
	t.Cleanup(cli.ResetUserHomeDirFunc)
	setTracearyPathToCurrentExecutable(t)

	tests := []struct {
		name     string
		state    application.AttestationAnchorState
		inspect  error
		wantStat string
		wantSub  string
	}{
		{
			name:     "matches",
			state:    application.AttestationAnchorState{Relation: string(attestation.AnchorMatches), StoreSeq: 2},
			wantStat: "pass",
			wantSub:  "matches the store head",
		},
		{
			name:     "published",
			state:    application.AttestationAnchorState{Relation: string(attestation.AnchorMatches), StoreSeq: 2, Published: true, Path: "/tmp/store.db.attest"},
			wantStat: "pass",
			wantSub:  "published",
		},
		{
			name:     "mismatch",
			state:    application.AttestationAnchorState{Relation: string(attestation.AnchorMismatch), FileSeq: 1, StoreSeq: 1},
			wantStat: "fail",
			wantSub:  "does not match",
		},
		{
			name:     "ahead",
			state:    application.AttestationAnchorState{Relation: string(attestation.AnchorAhead), FileSeq: 4, StoreSeq: 1},
			wantStat: "fail",
			wantSub:  "does not match",
		},
		{
			name:     "inspect error",
			inspect:  errAttestationInspect,
			wantStat: "fail",
			wantSub:  "attestation anchor check failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inspector := &stubAttestationAnchorInspector{state: tt.state, err: tt.inspect}
			rootCmd := newTestRootCLI(
				cli.WithStoreManagement(&storeManagementUsecaseStub{}),
				cli.WithAttestationAnchorInspector(inspector),
			).Command()
			stdout := &bytes.Buffer{}
			rootCmd.SetOut(stdout)
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs([]string{"doctor", "--client", "claude", "--project-dir", projectDir, "--json", "--warnings-ok"})
			executeDoctorAllowWarnings(t, rootCmd)
			if inspector.calls != 1 {
				t.Fatalf("Inspect calls = %d, want 1", inspector.calls)
			}
			if !inspector.last.OpenStore {
				t.Fatal("small-store doctor left OpenStore=false")
			}
			check := statusByName(decodeDoctorReport(t, stdout.Bytes()), "attestation-anchor")
			if check.Status != tt.wantStat || !strings.Contains(check.Message, tt.wantSub) {
				t.Fatalf("check = %#v, want status %q containing %q", check, tt.wantStat, tt.wantSub)
			}
		})
	}
}

func TestRootCLI_DoctorLargeStoreAttestationAnchorIsFileOnly(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	largeStore := filepath.Join(t.TempDir(), "large-anchor.db")
	file, err := os.OpenFile(largeStore, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Truncate(2 << 30); err != nil {
		_ = file.Close()
		t.Fatalf("Truncate() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store := &storeManagementUsecaseStub{}
	inspector := &stubAttestationAnchorInspector{
		state: application.AttestationAnchorState{
			Relation: "file_ok",
			FileSeq:  1,
			Path:     largeStore + ".attest",
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(store),
		cli.WithAttestationAnchorInspector(inspector),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", largeStore, "--json", "--warnings-ok"})

	started := time.Now()
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded metadata-only doctor took %s, want <= 1s", elapsed)
	}
	if store.initCalled {
		t.Fatal("large-store doctor initialized SQLite")
	}
	if inspector.calls != 1 {
		t.Fatalf("Inspect calls = %d, want 1", inspector.calls)
	}
	if inspector.last.OpenStore {
		t.Fatal("large-store doctor opened the store for attestation")
	}
	if inspector.last.StorePath != largeStore {
		t.Fatalf("StorePath = %q, want %q", inspector.last.StorePath, largeStore)
	}
	check := statusByName(decodeDoctorReport(t, stdout.Bytes()), "attestation-anchor")
	if check.Status != "pass" || !strings.Contains(check.Message, "readable") {
		t.Fatalf("large-store attestation check = %#v", check)
	}
}

func TestRootCLI_DoctorLargeStoreWarnsWhenAttestationAnchorIsMissing(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "en")
	setTracearyPathToCurrentExecutable(t)
	largeStore := filepath.Join(t.TempDir(), "large-missing-anchor.db")
	file, err := os.OpenFile(largeStore, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Truncate(2 << 30); err != nil {
		_ = file.Close()
		t.Fatalf("Truncate() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	inspector := &stubAttestationAnchorInspector{
		state: application.AttestationAnchorState{
			Relation: string(attestation.AnchorMissing),
			Path:     largeStore + ".attest",
		},
	}
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithAttestationAnchorInspector(inspector),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"doctor", "--db-path", largeStore, "--json", "--warnings-ok"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if inspector.last.OpenStore {
		t.Fatal("missing-file large-store doctor opened SQLite")
	}
	check := statusByName(decodeDoctorReport(t, stdout.Bytes()), "attestation-anchor")
	if check.Status != "warn" || !strings.Contains(check.Message, "does not publish") {
		t.Fatalf("missing large-store check = %#v", check)
	}
}

var errAttestationInspect = errAttestationInspectSentinel("chain broken")

type errAttestationInspectSentinel string

func (e errAttestationInspectSentinel) Error() string { return string(e) }
