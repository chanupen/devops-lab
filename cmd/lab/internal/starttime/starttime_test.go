package starttime

import (
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
)

type marker struct{}

func TestAtIsBeforeNow(t *testing.T) {
	if !At().Before(time.Now()) {
		t.Errorf("At() = %s, want a time before now", At())
	}
}

// The stamp only covers the heavy package inits while this path sorts
// before theirs.
func TestSortsBeforeHeavyInits(t *testing.T) {
	ours := reflect.TypeOf(marker{}).PkgPath()
	for _, p := range []string{"github.com/charmbracelet/bubbletea", "helm.sh/helm/v4", reflect.TypeOf(runtime.Scheme{}).PkgPath()} {
		if ours >= p {
			t.Errorf("%s must sort before %s", ours, p)
		}
	}
}
