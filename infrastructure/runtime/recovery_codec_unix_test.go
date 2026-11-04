//go:build darwin || linux

package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func recoveryCodecFixture(t *testing.T) (recoveryEnvelope, executionEnvelope) {
	t.Helper()
	project, err := domain.ProjectOf("/project", "/project/.git", "project-witness")
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := domain.WorktreeOf(project.ID(), "/project", "/project/.git", "/store.db", "worktree-witness")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "fixture", Host: "claude", Executable: "/agent", ExecutableWitness: "fixture-witness", Version: "synthetic 1"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := domain.TargetOf(worktree, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	id := domain.LaunchIdentity{Run: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32)}
	descriptor, err := domain.LaunchDescriptorOf(id, target, profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := descriptor.Started(321); err != nil {
		t.Fatal(err)
	}
	if err := descriptor.Exit(0); err != nil {
		t.Fatal(err)
	}
	binding, err := domain.RecoveryBindingOf(domain.RecoveryBindingState{Identity: id, Target: target, Profile: profile, PID: 321, NativeSessionID: "synthetic-session", NativeHistoryPath: "/native/session.jsonl", NativeNamespaceWitness: strings.Repeat("a", 64), Model: "synthetic-model", Provider: "synthetic-provider"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := record.ObserveOwnedExit(id, 0); err != nil {
		t.Fatal(err)
	}
	revision := uint64(1)
	doc := recoveryEnvelope{Version: 1, Revision: &revision, Records: []recoveryRecord{recordRecovery(record.Snapshot())}, Attempts: []resumeAttemptRecord{}}
	executionRevision := uint64(3)
	execution := executionEnvelope{Version: 1, Revision: &executionRevision, Profiles: []profileRecord{recordProfile(profile)}, Launches: []launchRecord{recordLaunch(descriptor.Snapshot())}}
	return doc, execution
}

func TestRecoveryCodecRoundTripAndCrossEnvelopeExitReceipt(t *testing.T) {
	doc, execution := recoveryCodecFixture(t)
	data, err := encodeRecovery(doc, execution)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := decodeRecovery(data, execution)
	if err != nil || !reflect.DeepEqual(loaded, doc) {
		t.Fatalf("changed metadata round trip: %v", err)
	}
	execution.Launches = nil
	if _, err := decodeRecovery(data, execution); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("dangling native record accepted: %v", err)
	}
}

func TestRecoveryCodecRejectsClosedSchemaAmbiguityAndMalformedLinks(t *testing.T) {
	doc, execution := recoveryCodecFixture(t)
	data, err := encodeRecovery(doc, execution)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		input []byte
	}{
		{"null revision", bytes.Replace(data, []byte(`"revision":1`), []byte(`"revision":null`), 1)},
		{"missing revision", bytes.Replace(data, []byte(`"revision":1,`), nil, 1)},
		{"duplicate revision", bytes.Replace(data, []byte(`"revision":1`), []byte(`"revision":1,"revision":1`), 1)},
		{"case alias", bytes.Replace(data, []byte(`"records"`), []byte(`"Records"`), 1)},
		{"unicode fold alias", bytes.Replace(data, []byte(`"native_session_id"`), []byte(`"native_ſession_id"`), 1)},
		{"null attempts", bytes.Replace(data, []byte(`"attempts":[]`), []byte(`"attempts":null`), 1)},
		{"unknown field", append(append([]byte{}, data[:len(data)-1]...), []byte(`,"payload":"forbidden"}`)...)},
		{"trailing document", append(append([]byte{}, data...), []byte(` {}`)...)},
		{"invalid utf8", append(append([]byte{}, data...), 0xff)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeRecovery(tc.input, execution); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("closed schema violation accepted: %v", err)
			}
		})
	}
	if _, err := decodeRecovery(bytes.Replace(data, []byte(`"version":1`), []byte(`"version":2`), 1), execution); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("unknown version not distinguished: %v", err)
	}
	doc.Records = append(doc.Records, doc.Records[0])
	if _, err := encodeRecovery(doc, execution); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("duplicate or under-revisioned records accepted: %v", err)
	}
}

func TestRecoveryCodecPreservesReservedUnknownAndConfirmedAttempts(t *testing.T) {
	doc, execution := recoveryCodecFixture(t)
	record, err := doc.Records[0].domainRecord()
	if err != nil {
		t.Fatal(err)
	}
	old := record.Snapshot().Binding.Snapshot()
	next := domain.LaunchIdentity{Run: strings.Repeat("4", 32), Generation: strings.Repeat("5", 32), Epoch: old.Identity.Epoch}
	attempt, err := domain.NewResumeAttempt("request-1", record, next)
	if err != nil {
		t.Fatal(err)
	}
	revision := uint64(2)
	doc.Revision = &revision
	doc.Attempts = []resumeAttemptRecord{recordResume(attempt.Snapshot())}
	encoded, err := encodeRecovery(doc, execution)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := decodeRecovery(encoded, execution)
	if err != nil || loaded.Attempts[0].Phase != string(domain.ResumeReserved) {
		t.Fatal("loaded reservation was promoted to an execution outcome", err)
	}
	newRun, err := domain.LaunchDescriptorOf(next, old.Target, old.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := newRun.Started(322); err != nil {
		t.Fatal(err)
	}
	execution.Launches = append(execution.Launches, recordLaunch(newRun.Snapshot()))
	if err := attempt.MarkStarted(); err != nil {
		t.Fatal(err)
	}
	if err := attempt.MarkUnknown(); err != nil {
		t.Fatal(err)
	}
	doc.Attempts[0] = recordResume(attempt.Snapshot())
	encoded, err = encodeRecovery(doc, execution)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = decodeRecovery(encoded, execution)
	if err != nil || loaded.Attempts[0].Phase != string(domain.ResumeUnknown) {
		t.Fatal("unknown outcome was cleared on reopen", err)
	}
	state := old
	state.Identity, state.PID = next, 322
	successor, err := domain.RecoveryBindingOf(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.Confirm(successor); err != nil {
		t.Fatal(err)
	}
	doc.Attempts[0] = recordResume(attempt.Snapshot())
	encoded, err = encodeRecovery(doc, execution)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = decodeRecovery(encoded, execution)
	if err != nil || loaded.Attempts[0].Phase != string(domain.ResumeConfirmed) {
		t.Fatal("confirmed identity/linkage lost", err)
	}
	// A missing successor is inconsistent, not permission to repeat Start.
	execution.Launches = execution.Launches[:1]
	if _, err := decodeRecovery(encoded, execution); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("confirmed attempt without its actual launch accepted: %v", err)
	}
}

func TestRecoveryCodecSeparatesObservedExitFromPublicationUncertainty(t *testing.T) {
	doc, execution := recoveryCodecFixture(t)
	uncertain := true
	execution.Launches[0].Uncertain = &uncertain
	if _, err := encodeRecovery(doc, execution); err != nil {
		t.Fatalf("publication uncertainty erased the positive exact exit receipt: %v", err)
	}
	execution.Launches[0].State = string(domain.RunLive)
	if _, err := encodeRecovery(doc, execution); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("positive receipt for a live original run accepted: %v", err)
	}
}

func FuzzRecoveryCodecClosedMetadata(f *testing.F) {
	f.Add([]byte(`{"version":1,"revision":0,"records":[],"attempts":[]}`))
	f.Add([]byte(`{"version":1,"revision":null,"records":[],"attempts":[]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		zero := uint64(0)
		execution := executionEnvelope{Version: 1, Revision: &zero, Profiles: []profileRecord{}, Launches: []launchRecord{}}
		doc, err := decodeRecovery(data, execution)
		if err != nil {
			return
		}
		encoded, err := encodeRecovery(doc, execution)
		if err != nil || !json.Valid(encoded) {
			t.Fatalf("accepted metadata cannot be encoded: %v", err)
		}
	})
}

func TestRecoveryCodecRejectsConcurrentConversationAndGenerationCollisions(t *testing.T) {
	doc, execution := recoveryCodecFixture(t)
	first, err := doc.Records[0].domainRecord()
	if err != nil {
		t.Fatal(err)
	}
	secondState := first.Snapshot().Binding.Snapshot()
	secondState.Identity.Run, secondState.Identity.Generation = strings.Repeat("4", 32), strings.Repeat("5", 32)
	secondState.PID = 323
	binding, err := domain.RecoveryBindingOf(secondState)
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.NewRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.ObserveOwnedExit(secondState.Identity, 0); err != nil {
		t.Fatal(err)
	}
	descriptor, err := domain.LaunchDescriptorOf(secondState.Identity, secondState.Target, secondState.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := descriptor.Started(323); err != nil {
		t.Fatal(err)
	}
	if err := descriptor.Exit(0); err != nil {
		t.Fatal(err)
	}
	execution.Launches = append(execution.Launches, recordLaunch(descriptor.Snapshot()))
	doc.Records = append(doc.Records, recordRecovery(second.Snapshot()))
	*doc.Revision = 4
	firstNext := domain.LaunchIdentity{Run: strings.Repeat("6", 32), Generation: strings.Repeat("7", 32), Epoch: secondState.Identity.Epoch}
	secondNext := domain.LaunchIdentity{Run: strings.Repeat("8", 32), Generation: strings.Repeat("9", 32), Epoch: secondState.Identity.Epoch}
	a, err := domain.NewResumeAttempt("first-request", first, firstNext)
	if err != nil {
		t.Fatal(err)
	}
	b, err := domain.NewResumeAttempt("second-request", second, secondNext)
	if err != nil {
		t.Fatal(err)
	}
	doc.Attempts = []resumeAttemptRecord{recordResume(a.Snapshot()), recordResume(b.Snapshot())}
	if _, err := encodeRecovery(doc, execution); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("two active managed resumes for same conversation accepted: %v", err)
	}
	doc.Attempts = doc.Attempts[:1]
	if _, err := encodeRecovery(doc, execution); err != nil {
		t.Fatalf("one reservation rejected: %v", err)
	}
	doc.Attempts[0].Next.Generation = secondState.Identity.Generation
	if _, err := encodeRecovery(doc, execution); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("existing generation reused: %v", err)
	}
}
