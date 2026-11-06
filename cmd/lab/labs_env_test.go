package main

import (
	"github.com/chanupen/devops-lab/internal/labs"

	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestWriteExports(t *testing.T) {
	tests := []struct {
		name string
		env  []string
		want string
	}{
		{"plain", []string{"A=1"}, "export A='1'\n"},
		{"json", []string{`T=[{"Key":"lab"}]`}, `export T='[{"Key":"lab"}]'` + "\n"},
		{"quote", []string{"Q=it's"}, `export Q='it'\''s'` + "\n"},
		{"equals in value", []string{"E=a=b"}, "export E='a=b'\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := writeExports(&b, tt.env); err != nil {
				t.Fatal(err)
			}
			if b.String() != tt.want {
				t.Errorf("got %q, want %q", b.String(), tt.want)
			}
			// The shell reads the value back unchanged.
			k, v, _ := strings.Cut(tt.env[0], "=")
			out, err := exec.Command("sh", "-c", b.String()+`printf %s "$`+k+`"`).Output()
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != v {
				t.Errorf("shell read %q, want %q", out, v)
			}
		})
	}
}

func TestDoneHint(t *testing.T) {
	tests := []struct {
		needs, want string
	}{
		{labs.NeedsScratch, "lab labs stop deletes its cluster."},
		{labs.NeedsLab, "lab labs reset removes its work from the main lab, then lab labs stop ends it."},
	}
	for _, tt := range tests {
		if got := doneHint(labs.Lab{Needs: tt.needs}); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.needs, got, tt.want)
		}
	}
}
