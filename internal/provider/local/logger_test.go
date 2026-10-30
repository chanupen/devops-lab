package local

import (
	"bytes"
	"testing"
)

func TestKindLogger(t *testing.T) {
	tests := []struct {
		name string
		log  func(l *kindLogger)
		want string
	}{
		{"info at V(0)", func(l *kindLogger) { l.V(0).Info(" ✓ Preparing nodes ") }, "    ✓ Preparing nodes\n"},
		{"infof at V(0)", func(l *kindLogger) { l.V(0).Infof("Ready after %ds", 10) }, "    Ready after 10s\n"},
		{"debug dropped", func(l *kindLogger) { l.V(1).Info("debug detail") }, ""},
		{"blank dropped", func(l *kindLogger) { l.V(0).Info("  ") }, ""},
		{"warning", func(l *kindLogger) { l.Warn("disk low") }, "    warning: disk low\n"},
		{"podman notice dropped", func(l *kindLogger) { l.Warn("enabling experimental podman provider") }, ""},
		{"podman notice via Warnf dropped", func(l *kindLogger) { l.Warnf("%s", "enabling experimental podman provider") }, ""},
		{"error", func(l *kindLogger) { l.Errorf("failed: %s", "x") }, "    error: failed: x\n"},
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
	if newKindLogger(nil).V(1).Enabled() || !newKindLogger(nil).V(0).Enabled() {
		t.Error("Enabled should be true only for V(0)")
	}
}
