package local

import (
	"bytes"
	"testing"

	"sigs.k8s.io/kind/pkg/log"
)

func TestKindLogger(t *testing.T) {
	tests := []struct {
		name string
		log  func(l *kindLogger)
		want string
	}{
		{"info at V(0)", func(l *kindLogger) { l.V(0).Info(" ✓ Preparing nodes") }, "kind:  ✓ Preparing nodes\n"},
		{"infof at V(0)", func(l *kindLogger) { l.V(0).Infof("Ready after %ds", 10) }, "kind: Ready after 10s\n"},
		{"debug kept with its level", func(l *kindLogger) { l.V(1).Info("Running: podman ps") }, "kind: [v1] Running: podman ps\n"},
		{"multi-line message split", func(l *kindLogger) { l.V(2).Info("a\nb\n") }, "kind: [v2] a\nkind: [v2] b\n"},
		{"blank dropped", func(l *kindLogger) { l.V(0).Info("  ") }, ""},
		{"podman notice kept", func(l *kindLogger) { l.Warn("enabling experimental podman provider") },
			"kind: warning: enabling experimental podman provider\n"},
		{"warnf", func(l *kindLogger) { l.Warnf("disk %s", "low") }, "kind: warning: disk low\n"},
		{"error", func(l *kindLogger) { l.Errorf("failed: %s", "x") }, "kind: error: failed: x\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			tt.log(newKindLogger(&b))
			if b.String() != tt.want {
				t.Errorf("got %q, want %q", b.String(), tt.want)
			}
		})
	}
	for _, lvl := range []int32{0, 1, 5} {
		if !newKindLogger(nil).V(levelOf(lvl)).Enabled() {
			t.Errorf("V(%d) disabled; every level should reach the ops log", lvl)
		}
	}
}

func levelOf(n int32) log.Level { return log.Level(n) }
