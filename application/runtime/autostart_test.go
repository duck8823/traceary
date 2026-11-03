package runtime_test

import (
	"context"
	"errors"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

type autoStartFixture struct {
	enabled                bool
	reads, writes, renders int
	failure                error
}

func (f *autoStartFixture) ReadLifecycle(context.Context) (app.LifecycleSnapshot, error) {
	f.reads++
	return app.LifecycleSnapshot{AutoStartOptIn: f.enabled}, f.failure
}
func (f *autoStartFixture) SetAutoStartOptIn(_ context.Context, value bool) error {
	f.writes++
	if f.failure == nil {
		f.enabled = value
	}
	return f.failure
}
func (f *autoStartFixture) RenderAutoStartDescriptor(context.Context) (string, error) {
	f.renders++
	return "descriptor", f.failure
}
func TestAutoStartExplicitPreferenceAndDescriptorNeverImplyInstallation(t *testing.T) {
	f := &autoStartFixture{}
	s, err := app.NewAutoStart(f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	status, err := s.Status(ctx)
	if err != nil || status.OptIn || status.InstallationState != "unknown" || status.HostInspection != "not_performed" || status.StartsAgents {
		t.Fatal(status, err)
	}
	if _, err = s.Descriptor(ctx); !errors.Is(err, domain.ErrUnsafe) || f.renders != 0 || f.writes != 0 {
		t.Fatal("descriptor enabled itself", err)
	}
	for _, enabled := range []bool{true, false, true} {
		status, err = s.SetOptIn(ctx, enabled)
		if err != nil || status.OptIn != enabled || status.InstallationState != "unknown" || f.renders != 0 {
			t.Fatal(status, err)
		}
	}
	if value, err := s.Descriptor(ctx); err != nil || value != "descriptor" || f.renders != 1 || f.writes != 3 {
		t.Fatal(value, err)
	}
	f.failure = app.ErrLifecycleCommitUncertain
	if _, err = s.SetOptIn(ctx, false); !errors.Is(err, app.ErrLifecycleCommitUncertain) || f.writes != 4 {
		t.Fatal("ambiguous write retried/hidden", err)
	}
	if _, err = s.Descriptor(ctx); !errors.Is(err, app.ErrLifecycleCommitUncertain) || f.renders != 1 {
		t.Fatal("failed read rendered", err)
	}
}
func TestAutoStartRejectsMissingCapabilities(t *testing.T) {
	f := &autoStartFixture{}
	if _, err := app.NewAutoStart(nil, f, f); err == nil {
		t.Fatal("missing reader")
	}
	if _, err := app.NewAutoStart(f, nil, f); err == nil {
		t.Fatal("missing writer")
	}
	if _, err := app.NewAutoStart(f, f, nil); err == nil {
		t.Fatal("missing renderer")
	}
}
