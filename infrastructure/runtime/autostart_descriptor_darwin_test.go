package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestAutoStartDescriptorVerifiedExecutableExactArgumentsAndEscapedXML(t *testing.T) {
	root := filepath.Join(daemonRoot(t), `a&<'"`)
	ctx := context.Background()
	service, err := (infra.LocalOperatorFactory{}).CreateAutoStart(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetOptIn(ctx, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Descriptor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(strings.NewReader(value))
	var keys, values []string
	var trueCount int
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("invalid XML", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key", "string":
			var text string
			if err = decoder.DecodeElement(&text, &start); err != nil {
				t.Fatal(err)
			}
			if start.Name.Local == "key" {
				keys = append(keys, text)
			} else {
				values = append(values, text)
			}
		case "true":
			trueCount++
		case "plist", "dict", "array":
		default:
			t.Fatal("unexpected descriptor element", start.Name.Local)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(root))
	want := []string{"io.traceary.daemon." + hex.EncodeToString(digest[:])[:32], executable, "daemon", "serve", "--runtime-dir", root}
	if strings.Join(keys, "|") != "Label|ProgramArguments|RunAtLoad" || strings.Join(values, "|") != strings.Join(want, "|") || trueCount != 1 {
		t.Fatal("descriptor changed command/host/environment policy", keys, values)
	}
	after, err := os.ReadFile(filepath.Join(root, "lifecycle.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("render modified preference", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "lifecycle.json" && entry.Name() != "registry.lock" {
			t.Fatal("render created unrelated files", entry.Name())
		}
	}
}

func TestAutoStartDescriptorRefusesReplacedRootAndCanceledContext(t *testing.T) {
	root := filepath.Join(daemonRoot(t), "runtime")
	ctx := context.Background()
	service, err := (infra.LocalOperatorFactory{}).CreateAutoStart(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetOptIn(ctx, true); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if value, err := service.Descriptor(canceled); err == nil || value != "" {
		t.Fatal("canceled descriptor succeeded", err)
	}
	if err = os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	replacement, err := (infra.LocalOperatorFactory{}).CreateAutoStart(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = replacement.SetOptIn(ctx, true); err != nil {
		t.Fatal(err)
	}
	if value, err := service.Descriptor(ctx); err == nil || value != "" {
		t.Fatal("replaced root adopted", err)
	}
}
