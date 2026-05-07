package tui

import (
	"strings"
	"testing"
)

func TestDefaultStyles_RenderableForEachField(t *testing.T) {
	styles := DefaultStyles()
	cases := map[string]func(...string) string{
		"Title":   styles.Title.Render,
		"Subtle":  styles.Subtle.Render,
		"Active":  styles.Active.Render,
		"Idle":    styles.Idle.Render,
		"Success": styles.Success.Render,
		"Warning": styles.Warning.Render,
		"Error":   styles.Error.Render,
		"Help":    styles.Help.Render,
		"Border":  styles.Border.Render,
	}
	for name, render := range cases {
		got := render("traceary")
		if !strings.Contains(got, "traceary") {
			t.Errorf("%s.Render dropped the inner text: %q", name, got)
		}
	}
}
