package hooks

import "testing"

func TestNormalizeScriptContent_CRLFIsConvertedToLF(t *testing.T) {
	t.Parallel()

	got := normalizeScriptContent("#!/bin/bash\r\necho hi\r\n")
	want := "#!/bin/bash\necho hi\n"
	if got != want {
		t.Fatalf("normalizeScriptContent() = %q, want %q", got, want)
	}
}
