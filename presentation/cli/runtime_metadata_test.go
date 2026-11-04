package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/duck8823/traceary/presentation/cli"
)

type metadataCLIFactory struct {
	app.OperatorFactory
	opens, exports, previews, forgets int
	confirmation                      domain.MetadataForgetConfirmation
}

func (f *metadataCLIFactory) OpenMetadata(context.Context, string) (app.MetadataAudit, error) {
	f.opens++
	return f, nil
}
func (f *metadataCLIFactory) Export(context.Context) (app.MetadataExport, error) {
	f.exports++
	return app.MetadataExport{Version: 1, Authority: "persisted_metadata_not_liveness"}, nil
}
func (f *metadataCLIFactory) PreviewForget(context.Context) (app.MetadataForgetPreview, error) {
	f.previews++
	return app.MetadataForgetPreview{Snapshot: app.MetadataExport{Version: 1, Digest: strings.Repeat("a", 64)}, RecoveryRecordLoss: true, OfflineRequired: true}, nil
}
func (f *metadataCLIFactory) Forget(_ context.Context, c domain.MetadataForgetConfirmation) (app.MetadataForgetResult, error) {
	f.forgets++
	f.confirmation = c
	return app.MetadataForgetResult{Outcome: "forgotten", Runs: 1}, nil
}

func TestRuntimeMetadataCLIRequiresExplicitRootPreviewAndBothLossAcknowledgements(t *testing.T) {
	f := &metadataCLIFactory{}
	run := func(args ...string) (string, error) {
		cmd := cli.NewRootCLI(cli.WithRuntimeOperators(f), cli.WithDatabasePathSetter(func(string) { t.Fatal("metadata command touched core bootstrap") })).Command()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"runtime", "metadata"}, args...))
		err := cmd.Execute()
		return out.String(), err
	}
	for _, operation := range []string{"export", "forget-preview"} {
		if _, err := run(operation, "--runtime-dir", "/explicit/private"); err != nil {
			t.Fatal(err)
		}
	}
	if f.exports != 1 || f.previews != 1 || f.forgets != 0 {
		t.Fatal("read operations mutated metadata")
	}
	before := f.opens
	for _, args := range [][]string{{"export"}, {"export", "--runtime-dir", "relative"}, {"forget", "--runtime-dir", "/explicit/private", "--preview-digest", strings.Repeat("a", 64)}, {"forget", "--runtime-dir", "/explicit/private", "--preview-digest", strings.Repeat("a", 64), "--acknowledge-forget-history"}, {"forget", "--runtime-dir", "/explicit/private", "--preview-digest", "invalid", "--acknowledge-forget-history", "--acknowledge-recovery-record-loss"}} {
		if _, err := run(args...); err == nil {
			t.Fatal("unsafe metadata arguments accepted")
		}
	}
	if f.opens != before || f.forgets != 0 {
		t.Fatal("invalid confirmation reached metadata port")
	}
	out, err := run("forget", "--runtime-dir", "/explicit/private", "--preview-digest", strings.Repeat("a", 64), "--acknowledge-forget-history", "--acknowledge-recovery-record-loss")
	if err != nil || f.forgets != 1 || !f.confirmation.Valid() || !strings.Contains(out, `"outcome":"forgotten"`) {
		t.Fatal("explicit confirmation lost", err)
	}
}
