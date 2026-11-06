package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const tcpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func TestCheckLANListeners(t *testing.T) {
	tests := []struct {
		name       string
		tcp, tcp6  string
		want       Status
		wantDetail string
	}{
		{"loopback only",
			"   1: 0100007F:2CAA 00000000:0000 0A 0 0 0 1000 0 1\n   2: 0100007F:1388 00000000:0000 0A 0 0 0 1000 0 1\n",
			"   0: 00000000000000000000000001000000:20FB 00000000000000000000000000000000:0000 0A 0 0 0 1000 0 1\n",
			OK, "loopback only"},
		// ssh on 0.0.0.0:22 is not a lab port.
		{"other services on the LAN are not lab's", "   0: 00000000:0016 00000000:0000 0A 0 0 0 0 0 1\n", "", OK, ""},
		{"Ollama on every IPv6 and IPv4 address (pasta *:11434)", "",
			"   0: 00000000000000000000000000000000:2CAA 00000000000000000000000000000000:0000 0A 0 0 0 1000 0 1\n",
			Fail, "Ollama on [::]:11434"},
		{"Zot on the LAN address", "   0: 3232A8C0:1388 00000000:0000 0A 0 0 0 1000 0 1\n", "", Fail, "lab-zot on 192.168.50.50:5000"},
		{"a connection, not a listener", "   0: 3232A8C0:2CAA 0A3232C0:D431 01 0 0 0 1000 0 1\n", "", OK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sys := System{ReadFile: func(p string) ([]byte, error) {
				if strings.HasSuffix(p, "6") {
					return []byte(tcpHeader + tt.tcp6), nil
				}
				return []byte(tcpHeader + tt.tcp), nil
			}}
			r := checkLANListeners(context.Background(), sys, Options{})
			if r.Status != tt.want || !strings.Contains(r.Detail, tt.wantDetail) {
				t.Errorf("got %v %q, want %v %q", r.Status, r.Detail, tt.want, tt.wantDetail)
			}
		})
	}
}

func TestAIChecks(t *testing.T) {
	good := AIState{Address: "10.89.1.250", Slice: "10.89.1.250", Version: "0.40.13", Models: []string{"llama3.2:3b"},
		LangfuseVersion: "4.75.0", Apps: 5}
	tests := []struct {
		name  string
		check func(AIState) Result
		edit  func(*AIState)
		want  Status
		text  string
	}{
		{"ollama ok", checkOllamaInCluster, nil, OK, "0.40.13 through ollama.ai.svc"},
		{"ollama on the LAN", checkOllamaInCluster, func(s *AIState) { s.AddressErr = "ollama is published on 0.0.0.0:11434" }, Fail, "0.0.0.0:11434"},
		{"slice points elsewhere", checkOllamaInCluster, func(s *AIState) { s.Slice = "10.89.1.9" }, Fail, "points at 10.89.1.9"},
		{"no endpoints", checkOllamaInCluster, func(s *AIState) { s.SliceErr = "not found" }, Fail, "no endpoints"},
		{"no answer in cluster", checkOllamaInCluster, func(s *AIState) { s.InClusterErr = "curl: (28) timed out" }, Fail, "timed out"},
		{"model pulled", checkOllamaModel, nil, OK, "llama3.2:3b pulled"},
		{"model missing", checkOllamaModel, func(s *AIState) { s.Models = []string{"qwen2.5:1.5b"} }, Fail, "have: qwen2.5:1.5b"},
		{"model unknown when Ollama is down", checkOllamaModel, func(s *AIState) { s.InClusterErr = "x" }, Skip, ""},
		{"langfuse ok", checkLangfuse, nil, OK, "4.75.0 healthy, 5 ai apps"},
		{"app not ready", checkLangfuse, func(s *AIState) { s.AppsNotReady = []string{"langfuse Synced/Progressing"} }, Fail, "langfuse Synced/Progressing"},
		{"health fails", checkLangfuse, func(s *AIState) { s.LangfuseErr = "connection refused" }, Fail, "connection refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := good
			st.Models = append([]string(nil), good.Models...)
			if tt.edit != nil {
				tt.edit(&st)
			}
			r := tt.check(st)
			if r.Status != tt.want || !strings.Contains(r.Detail, tt.text) {
				t.Errorf("got %v %q, want %v %q", r.Status, r.Detail, tt.want, tt.text)
			}
		})
	}
}

// The ai checks run only while the stack is deployed, and fail closed
// when its state cannot be read.
func TestAIDeployed(t *testing.T) {
	run := aiDeployed(func(AIState) Result { return ok("ran") })
	read := func(context.Context) (AIState, error) { return AIState{}, nil }
	broken := func(context.Context) (AIState, error) { return AIState{}, errors.New("no kubeconfig") }
	tests := []struct {
		name   string
		stacks []string
		ai     func(context.Context) (AIState, error)
		want   Status
	}{
		{"not deployed", []string{"obs"}, read, Skip},
		{"deployed", []string{"obs", "ai"}, read, OK},
		{"unreadable", []string{"ai"}, broken, Fail},
	}
	for _, tt := range tests {
		if r := run(context.Background(), System{AI: tt.ai}, Options{Stacks: tt.stacks}); r.Status != tt.want {
			t.Errorf("%s: %v %q, want %v", tt.name, r.Status, r.Detail, tt.want)
		}
	}
}

// GPU checks fail (not warn) while the ai stack is deployed, whatever the profile.
func TestAICheckSeverity(t *testing.T) {
	failing := aiCheck(func(context.Context, System, Options) Result { return fail("", "no GPU") })
	if r := failing(context.Background(), System{}, Options{Profile: "minimal"}); r.Status != Warn {
		t.Errorf("minimal without ai: %v", r.Status)
	}
	if r := failing(context.Background(), System{}, Options{Profile: "minimal", Stacks: []string{"ai"}}); r.Status != Fail {
		t.Errorf("minimal with ai deployed: %v", r.Status)
	}
}
