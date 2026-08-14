package cli

import "testing"

func TestStoreRetentionCommandsAreVisibleButRemainExplicit(t *testing.T) {
	t.Parallel()

	rootCLI := NewRootCLI()
	command := rootCLI.newStoreFileRetentionOnlyCommand()
	if command.Hidden {
		t.Fatal("store retention command is hidden after copied-store dogfood")
	}
	files, _, err := command.Find([]string{"files"})
	if err != nil {
		t.Fatalf("Find(files) error = %v", err)
	}
	if files.Hidden {
		t.Fatal("store retention files command is hidden after copied-store dogfood")
	}
	if _, _, err := command.Find([]string{"apply"}); err == nil {
		t.Fatal("raw-body store retention apply must not remain under store retention")
	}
	if _, _, err := command.Find([]string{"plan"}); err == nil {
		t.Fatal("raw-body store retention plan must not remain under store retention")
	}
	if _, _, err := command.Find([]string{"restore"}); err == nil {
		t.Fatal("raw-body store retention restore must not remain under store retention")
	}
}
