package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectStaleTracearyProcesses(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	origExecutable := osExecutableFunc
	t.Cleanup(func() {
		osExecutableFunc = origExecutable
		ResetListTracearyProcessSnapshotsForTest()
	})

	t.Run("passes when no Traceary processes are listed", func(t *testing.T) {
		SetEmptyTracearyProcessSnapshotsForTest()
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Name != doctorStaleProcessesCheckName {
			t.Fatalf("name = %q", got.Name)
		}
		if got.Status != doctorStatusPass {
			t.Fatalf("status = %q, want pass; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "no stale Traceary binaries") {
			t.Fatalf("message = %q", got.Message)
		}
	})

	t.Run("warns with pid and version for a stale Homebrew binary", func(t *testing.T) {
		osExecutableFunc = func() (string, error) {
			return "/opt/homebrew/opt/traceary/bin/traceary", nil
		}
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return []tracearyProcessSnapshot{{
				PID:        7766,
				Executable: "/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary",
				Args:       []string{"/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary", "mcp-server"},
				StartedAt:  now.Add(-13 * 24 * time.Hour),
			}}, nil
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusWarn {
			t.Fatalf("status = %q, want warn; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "pid=7766") || !strings.Contains(got.Message, "version=0.33.0") {
			t.Fatalf("message should name pid and version, got %q", got.Message)
		}
		if !strings.Contains(got.Message, "age=13d") {
			t.Fatalf("message should name age, got %q", got.Message)
		}
		if !strings.Contains(got.Message, "retired-mcp-server") {
			t.Fatalf("message should name retired mcp reason, got %q", got.Message)
		}
		if got.FixCommand != "ps -p 7766" {
			t.Fatalf("FixCommand = %q", got.FixCommand)
		}
		if !strings.Contains(got.Hint, "kill") {
			t.Fatalf("hint should include reap guidance, got %q", got.Hint)
		}
	})

	t.Run("warns on retired mcp-server even when the version matches", func(t *testing.T) {
		current := writeExecutable(t, t.TempDir(), "traceary")
		osExecutableFunc = func() (string, error) { return current, nil }
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return []tracearyProcessSnapshot{{
				PID:        4242,
				Executable: current,
				Args:       []string{current, "mcp-server"},
				StartedAt:  now.Add(-8 * 24 * time.Hour),
			}}, nil
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusWarn {
			t.Fatalf("status = %q, want warn; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "pid=4242") || !strings.Contains(got.Message, "retired-mcp-server") {
			t.Fatalf("message = %q", got.Message)
		}
		if !strings.Contains(got.Message, "version=0.44.1") {
			t.Fatalf("message should use the running version for the same binary, got %q", got.Message)
		}
	})

	t.Run("passes for the current binary when it is not mcp-server", func(t *testing.T) {
		current := writeExecutable(t, t.TempDir(), "traceary")
		osExecutableFunc = func() (string, error) { return current, nil }
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return []tracearyProcessSnapshot{{
				PID:        99999,
				Executable: current,
				Args:       []string{current, "doctor", "--json"},
				StartedAt:  now.Add(-time.Minute),
			}}, nil
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusPass {
			t.Fatalf("status = %q, want pass; msg=%q", got.Status, got.Message)
		}
	})

	t.Run("warns when Linux reports the executable inode as deleted after a same-path replace", func(t *testing.T) {
		current := writeExecutable(t, t.TempDir(), "traceary")
		osExecutableFunc = func() (string, error) { return current, nil }
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return []tracearyProcessSnapshot{{
				PID:        8800,
				Executable: current,
				Args:       []string{current, "hook", "session", "claude", "start"},
				StartedAt:  now.Add(-2 * 24 * time.Hour),
				Unlinked:   true,
			}}, nil
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusWarn {
			t.Fatalf("status = %q, want warn; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "pid=8800") || !strings.Contains(got.Message, "stale-binary") {
			t.Fatalf("message = %q", got.Message)
		}
		if !strings.Contains(got.Message, "version=unknown") {
			t.Fatalf("unlinked inode must not inherit the replacement binary version, got %q", got.Message)
		}
	})

	t.Run("passes for a hook-duration child with unknown version below the age floor", func(t *testing.T) {
		current := writeExecutable(t, t.TempDir(), "traceary")
		osExecutableFunc = func() (string, error) { return current, nil }
		// A different-inode binary with no readable build info inspects as
		// version=unknown; at 4s of age it is a hook-duration child, not a
		// stale long-runner.
		other := filepath.Join(t.TempDir(), "traceary")
		if err := os.WriteFile(other, []byte("not a go binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return []tracearyProcessSnapshot{{
				PID:        9100,
				Executable: other,
				Args:       []string{other, "hook", "session", "claude", "start"},
				StartedAt:  now.Add(-4 * time.Second),
			}}, nil
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusPass {
			t.Fatalf("status = %q, want pass; msg=%q", got.Status, got.Message)
		}
	})

	t.Run("passes for a young unlinked inode below the age floor", func(t *testing.T) {
		current := writeExecutable(t, t.TempDir(), "traceary")
		osExecutableFunc = func() (string, error) { return current, nil }
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return []tracearyProcessSnapshot{{
				PID:        9200,
				Executable: current,
				Args:       []string{current, "hook", "session", "claude", "start"},
				StartedAt:  now.Add(-4 * time.Second),
				Unlinked:   true,
			}}, nil
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusPass {
			t.Fatalf("status = %q, want pass; msg=%q", got.Status, got.Message)
		}
	})

	t.Run("warns for an old-binary long-runner above the age floor", func(t *testing.T) {
		osExecutableFunc = func() (string, error) {
			return "/opt/homebrew/opt/traceary/bin/traceary", nil
		}
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return []tracearyProcessSnapshot{{
				PID:        7767,
				Executable: "/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary",
				Args:       []string{"/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary", "serve"},
				StartedAt:  now.Add(-2 * time.Hour),
			}}, nil
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusWarn {
			t.Fatalf("status = %q, want warn; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "pid=7767") || !strings.Contains(got.Message, "version=0.33.0") {
			t.Fatalf("message should name pid and version, got %q", got.Message)
		}
		if !strings.Contains(got.Message, "age=2h") || !strings.Contains(got.Message, "stale-binary") {
			t.Fatalf("message should name age and stale-binary reason, got %q", got.Message)
		}
	})

	t.Run("warns for a young retired mcp-server with no age floor", func(t *testing.T) {
		osExecutableFunc = func() (string, error) {
			return "/opt/homebrew/opt/traceary/bin/traceary", nil
		}
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return []tracearyProcessSnapshot{{
				PID:        9300,
				Executable: "/opt/homebrew/Cellar/traceary/0.44.1/bin/traceary",
				Args:       []string{"/opt/homebrew/Cellar/traceary/0.44.1/bin/traceary", "mcp-server"},
				StartedAt:  now.Add(-4 * time.Second),
			}}, nil
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusWarn {
			t.Fatalf("status = %q, want warn; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "pid=9300") || !strings.Contains(got.Message, "retired-mcp-server") {
			t.Fatalf("message = %q", got.Message)
		}
	})

	t.Run("warns when process listing fails", func(t *testing.T) {
		SetListTracearyProcessSnapshotsForTest(func() ([]tracearyProcessSnapshot, error) {
			return nil, errors.New("ps: unavailable")
		})
		got := inspectStaleTracearyProcesses("0.44.1", now)
		if got.Status != doctorStatusWarn {
			t.Fatalf("status = %q, want warn; msg=%q", got.Status, got.Message)
		}
		if !strings.Contains(got.Message, "ps: unavailable") {
			t.Fatalf("message = %q", got.Message)
		}
	})
}

func TestParsePSElapsed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{name: "seconds only", in: "09", want: 9 * time.Second},
		{name: "minutes and seconds", in: "03:01", want: 3*time.Minute + time.Second},
		{name: "hours", in: "01:02:03", want: time.Hour + 2*time.Minute + 3*time.Second},
		{name: "days", in: "13-04:00:00", want: 13*24*time.Hour + 4*time.Hour},
		{name: "empty", in: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parsePSElapsed(tt.in); got != tt.want {
				t.Fatalf("parsePSElapsed(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParsePSTracearyProcessSnapshots(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	output := strings.Join([]string{
		" 7766 13-00:00:00 /opt/homebrew/Cellar/traceary/0.33.0/bin/traceary mcp-server",
		"  120 00:00:01 /usr/bin/ssh -N",
		" 4242    08:00:00 /opt/homebrew/Cellar/traceary/0.32.1/bin/traceary hook session claude start",
	}, "\n")
	got := parsePSTracearyProcessSnapshots(output, now)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].PID != 7766 || got[0].Args[1] != "mcp-server" {
		t.Fatalf("first snapshot = %+v", got[0])
	}
	if got[1].PID != 4242 {
		t.Fatalf("second snapshot = %+v", got[1])
	}
}

func TestCellarTracearyVersion(t *testing.T) {
	t.Parallel()
	got := cellarTracearyVersion("/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary")
	if got != "0.33.0" {
		t.Fatalf("got %q", got)
	}
	if cellarTracearyVersion("/usr/local/bin/traceary") != "" {
		t.Fatalf("non-cellar path should be empty")
	}
}

func TestDoctorSectionAndScopeForStaleProcesses(t *testing.T) {
	t.Parallel()
	if got := doctorSectionNameForCheck(doctorStaleProcessesCheckName); got != "Environment" {
		t.Fatalf("section = %q", got)
	}
	if !doctorCheckIsStoreIndependent(doctorStaleProcessesCheckName) {
		t.Fatal("stale-processes should be store-independent")
	}
}

func TestClassifyStaleTracearyProcessesSkipsSelfPID(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	origExecutable := osExecutableFunc
	t.Cleanup(func() { osExecutableFunc = origExecutable })
	osExecutableFunc = func() (string, error) {
		return "/opt/homebrew/opt/traceary/bin/traceary", nil
	}
	findings := classifyStaleTracearyProcesses([]tracearyProcessSnapshot{{
		PID:        os.Getpid(),
		Executable: "/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary",
		Args:       []string{"/opt/homebrew/Cellar/traceary/0.33.0/bin/traceary", "mcp-server"},
		StartedAt:  now.Add(-time.Hour),
	}}, "0.44.1", now)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none for self PID", findings)
	}
}

func TestLooksLikeTracearyExecutable(t *testing.T) {
	t.Parallel()
	if !looksLikeTracearyExecutable("/opt/homebrew/bin/traceary") {
		t.Fatal("expected traceary path to match")
	}
	if !looksLikeTracearyExecutable("traceary.exe") {
		t.Fatal("expected windows executable name to match")
	}
	if looksLikeTracearyExecutable("/usr/bin/grep") {
		t.Fatal("grep must not match")
	}
}
