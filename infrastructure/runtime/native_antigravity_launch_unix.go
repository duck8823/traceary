//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
)

const agyMetadataNativeDigest = "e61679b8ba54abae9a56534208e21f65e1dede6e4eb0144dbd64535f42d07724"

// This static guard qualifies only metadata instrumentation, NOT auth/history,
// model routing, live state or native recovery. Never rewrite the environment.
func inspectAntigravityMetadataSource(ctx context.Context, profile domain.HostProfile, environment []string) error {
	p := profile.Snapshot()
	if p.Host != "antigravity" || p.Version != "1.5.0" {
		return domain.ErrUnsupported
	}
	seen := make(map[string]struct{}, len(environment))
	temp := "/tmp"
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return domain.ErrUnsupported
		}
		if _, duplicate := seen[key]; duplicate {
			return domain.ErrUnsupported
		}
		seen[key] = struct{}{}
		if strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") {
			return domain.ErrUnsupported
		}
		if key == "TMPDIR" && value != "" {
			temp = value
		}
	}
	parent, err := nativeStartTempParent()
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(temp)
	if err != nil || canonical != parent {
		return domain.ErrUnsupported
	}
	source, err := inspectNativeSourceFile(ctx, p.Executable, true)
	if err != nil {
		return err
	}
	if source.Witness != p.ExecutableWitness {
		return domain.ErrChanged
	}
	if source.Digest != agyMetadataNativeDigest {
		return domain.ErrUnsupported
	}
	return nil
}

type agyMetadataLaunchPlan struct {
	slot     *agyMetadataSlot
	settings string
}

func (p *agyMetadataLaunchPlan) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.slot.close(ctx)
}
func (p *agyMetadataLaunchPlan) args() []string { return []string{"--settings", p.settings} }
func (s *agyMetadataSlot) writeSettings(ctx context.Context, executable string) (string, error) {
	if !canonicalNativePath(executable) {
		return "", domain.ErrInvalid
	}
	var settings struct {
		StatusLine struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Stack   bool   `json:"stack_with_default"`
			Enabled bool   `json:"enabled"`
		} `json:"statusLine"`
	}
	settings.StatusLine.Type = "command"
	settings.StatusLine.Stack = true
	settings.StatusLine.Enabled = true
	settings.StatusLine.Command = nativeShellLiteral(executable) + " runtime native-antigravity-statusline --slot " + nativeShellLiteral(s.metadata.root) + " >/dev/null 2>&1 || :"
	data, err := json.Marshal(settings)
	if err != nil {
		return "", domain.ErrInvalid
	}
	if int64(len(data)) > maxNativeMarkerBytes {
		return "", domain.ErrLimit
	}
	err = s.metadata.locked(ctx, func(fd int) error {
		intent, e := readAgyMetadataIntent(s.metadata, fd)
		if e != nil {
			return e
		}
		if intent != s.intent {
			return domain.ErrChanged
		}
		old, stamp, e := s.metadata.readMetadata(fd, "settings.json", maxNativeMarkerBytes)
		if e != nil {
			return e
		}
		if stamp != nil {
			if !bytes.Equal(old, data) {
				return domain.ErrChanged
			}
			return s.metadata.verifyRoot(fd)
		}
		return s.metadata.writeMetadata(ctx, fd, "settings.json", data, nil)
	})
	if err != nil {
		return "", err
	}
	s.settings = append([]byte{}, data...)
	return filepath.Join(s.metadata.root, "settings.json"), nil
}
func (l *PTYLauncher) prepareAntigravityMetadata(ctx context.Context, snapshot domain.RunSnapshot) (*agyMetadataLaunchPlan, error) {
	if err := inspectAntigravityMetadataSource(ctx, snapshot.Profile, l.environment); err != nil {
		return nil, err
	}
	slot, err := newAgyMetadataSlot(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	plan := &agyMetadataLaunchPlan{slot: slot}
	ready := false
	defer func() {
		if !ready {
			plan.close()
		}
	}()
	executable, err := os.Executable()
	if err != nil {
		return nil, domain.ErrUnsafe
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, domain.ErrUnsafe
	}
	plan.settings, err = slot.writeSettings(ctx, executable)
	if err != nil {
		return nil, err
	}
	ready = true
	return plan, nil
}
func (p *agyMetadataLaunchPlan) revalidate(ctx context.Context, snapshot domain.RunSnapshot, environment []string) error {
	if err := inspectAntigravityMetadataSource(ctx, snapshot.Profile, environment); err != nil {
		return err
	}
	actual := recordLaunch(snapshot)
	want := agyMetadataPlan{actual.Identity, actual.Worktree, actual.Profile}
	if want != p.slot.intent.Plan {
		return domain.ErrChanged
	}
	return p.slot.metadata.locked(ctx, func(fd int) error {
		intent, err := readAgyMetadataIntent(p.slot.metadata, fd)
		if err != nil {
			return err
		}
		if intent != p.slot.intent {
			return domain.ErrChanged
		}
		settings, stamp, err := p.slot.metadata.readMetadata(fd, "settings.json", maxNativeMarkerBytes)
		if err != nil {
			return err
		}
		if stamp == nil || !bytes.Equal(settings, p.slot.settings) {
			return domain.ErrChanged
		}
		return p.slot.metadata.verifyRoot(fd)
	})
}
