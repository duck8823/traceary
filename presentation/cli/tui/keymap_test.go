package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
)

func TestDefaultKeyMap_QuitMatchesCtrlC(t *testing.T) {
	km := DefaultKeyMap()
	for _, k := range []string{"q", "ctrl+c", "esc"} {
		if !key.Matches(fakeKey(k), km.Quit) {
			t.Errorf("Quit binding should match %q", k)
		}
	}
}

func TestDefaultKeyMap_NavigationCovered(t *testing.T) {
	km := DefaultKeyMap()
	cases := []struct {
		name    string
		press   string
		binding key.Binding
	}{
		{"up arrow", "up", km.Up},
		{"vim k", "k", km.Up},
		{"down arrow", "down", km.Down},
		{"vim j", "j", km.Down},
		{"select", "enter", km.Select},
		{"search", "/", km.Search},
		{"refresh", "r", km.Refresh},
		{"help", "?", km.Help},
		{"home", "g", km.Home},
		{"end", "G", km.End},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !key.Matches(fakeKey(tc.press), tc.binding) {
				t.Errorf("expected binding to match %q", tc.press)
			}
		})
	}
}

func TestDefaultKeyMap_HelpListsBindings(t *testing.T) {
	km := DefaultKeyMap()
	if got := len(km.ShortHelp()); got == 0 {
		t.Fatalf("ShortHelp should expose bindings, got %d", got)
	}
	if got := len(km.FullHelp()); got < 2 {
		t.Fatalf("FullHelp should expose at least two rows, got %d", got)
	}
}

// fakeKey constructs a fmt.Stringer value usable with key.Matches. We don't
// import bubbletea here to keep the test focused on binding configuration;
// key.Matches accepts any fmt.Stringer whose String() matches one of the
// binding's keys.
func fakeKey(s string) stringKey { return stringKey(s) }

type stringKey string

func (s stringKey) String() string { return string(s) }
