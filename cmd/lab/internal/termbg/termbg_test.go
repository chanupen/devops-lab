package termbg

import (
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type marker struct{}

// The init in this package only wins if it runs before Bubble Tea's, which
// holds while this import path sorts first. Renaming either breaks it.
func TestRunsBeforeBubbleTea(t *testing.T) {
	ours, theirs := reflect.TypeOf(marker{}).PkgPath(), reflect.TypeOf(tea.Program{}).PkgPath()
	if ours >= theirs {
		t.Fatalf("%s must sort before %s for its init to run first", ours, theirs)
	}
}
