package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRequireLinux(t *testing.T) {
	tests := []struct {
		goos     string
		wantCode int // 0 means no error
	}{
		{"linux", 0},
		{"darwin", 2},
		{"windows", 2},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			var stderr bytes.Buffer
			err := requireLinux(tt.goos, &stderr)
			if tt.wantCode == 0 {
				if err != nil || stderr.Len() > 0 {
					t.Fatalf("err = %v, stderr = %q; want neither", err, stderr.String())
				}
				return
			}
			var exit exitError
			if !errors.As(err, &exit) || exit.code != tt.wantCode {
				t.Fatalf("err = %v, want exit code %d", err, tt.wantCode)
			}
			if !strings.Contains(stderr.String(), "ssh -t fedora lab doctor") {
				t.Errorf("stderr = %q, want the ssh hint", stderr.String())
			}
		})
	}
}
