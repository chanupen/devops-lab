package doctor

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestMemoryCollection(t *testing.T) {
	for _, tt := range []struct {
		name, content string
		bad           bool
	}{
		{"valid", "MemTotal: 25165824 kB\nMemAvailable: 8388608 kB\n", false},
		{"missing", "MemTotal: 25165824 kB\n", true},
		{"negative", "MemTotal: 25165824 kB\nMemAvailable: -1 kB\n", true},
		{"zero", "MemTotal: 25165824 kB\nMemAvailable: 0 kB\n", true},
		{"malformed", "MemTotal: nope kB\nMemAvailable: 100 kB\n", true},
		{"wrong unit", "MemTotal: 25165824 MB\nMemAvailable: 100 kB\n", true},
		{"overflow", "MemTotal: 9223372036854775807 kB\nMemAvailable: 100 kB\n", true},
		{"over total", "MemTotal: 100 kB\nMemAvailable: 200 kB\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads := 0
			sys := System{ReadFile: func(string) ([]byte, error) { reads++; return []byte(tt.content), nil }}
			mem, err := ReadMemory(sys)
			if (err != nil) != tt.bad {
				t.Fatalf("memory=%+v err=%v", mem, err)
			}
			if !tt.bad {
				r := checkMemory(context.Background(), sys, Options{Profile: "minimal", Memory: &mem})
				if r.Status != OK || !strings.Contains(r.Detail, "8.0 GiB available") {
					t.Fatalf("%+v", r)
				}
			}
			if reads != 1 {
				t.Fatal("snapshot was read twice")
			}
		})
	}
}

func TestRequestedAIReadiness(t *testing.T) {
	for _, tt := range []struct {
		requested, deployed []string
		profile             string
		want                Status
	}{
		{[]string{"ai"}, nil, "minimal", Fail},
		{[]string{}, nil, "full", Warn},
		{nil, nil, "full", Fail},
		{nil, []string{"ai"}, "minimal", Fail},
	} {
		t.Run(fmt.Sprintf("%s-%v-%v", tt.profile, tt.requested, tt.deployed), func(t *testing.T) {
			run := aiCheck(func(context.Context, System, Options) Result { return Result{Status: Fail} })
			r := run(context.Background(), System{}, Options{Profile: tt.profile, RequestedStacks: tt.requested, Stacks: tt.deployed})
			if r.Status != tt.want {
				t.Fatalf("%+v", r)
			}
		})
	}
}
