package doctor

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
)

// AIState is the ai stack as the host and the cluster report it (the
// local provider's AIStatus). Each Err says why the field next to it could
// not be read.
type AIState struct {
	Address, AddressErr string   // the Ollama container on the kind network
	Slice, SliceErr     string   // where ollama.ai.svc points
	Version             string   // Ollama, read from inside the cluster
	Models              []string // the same way
	InClusterErr        string
	LangfuseVersion     string
	LangfuseErr         string
	Apps                int      // the ai stack's Argo CD apps
	AppsNotReady        []string // "name sync/health"
}

// AIModel is the model the ai stack expects Ollama to have pulled.
const AIModel = "llama3.2:3b"

// aiDeployed runs a check only while the ai stack is deployed, and reads
// its state once per check.
func aiDeployed(run func(AIState) Result) func(context.Context, System, Options) Result {
	return func(ctx context.Context, sys System, opts Options) Result {
		if !slices.Contains(opts.Stacks, "ai") {
			return Result{Status: Skip, Detail: "ai stack not deployed"}
		}
		if opts.DeferClusterHealth {
			return Result{Status: Skip, Detail: "cluster health unverified until stopped lab containers restart"}
		}
		if sys.AI == nil {
			return Result{Status: Skip, Detail: "no cluster to ask", Unavailable: true}
		}
		st, err := sys.AI(ctx)
		if err != nil {
			return fail("lab status", "cannot read the ai stack: %s", firstLine(err.Error()))
		}
		return run(st)
	}
}

// checkOllamaInCluster: the host's Ollama is on the kind network, published
// on loopback only, ollama.ai.svc points at it, and it answers through the
// Service's ClusterIP from the cluster's node.
func checkOllamaInCluster(st AIState) Result {
	switch {
	case st.AddressErr != "":
		return fail("see the message; lab never edits the Quadlet", "%s", st.AddressErr)
	case st.SliceErr != "":
		return fail("lab up --stacks ai", "ollama.ai.svc has no endpoints: %s", firstLine(st.SliceErr))
	case st.Slice != st.Address:
		return fail("lab up --stacks ai", "ollama.ai.svc points at %s, but Ollama is at %s on the kind network", st.Slice, st.Address)
	case st.InClusterErr != "":
		return fail("systemctl --user status ollama; lab up --stacks ai", "no answer through ollama.ai.svc from the cluster: %s", firstLine(st.InClusterErr))
	}
	return ok("%s through ollama.ai.svc (%s:11434), from the cluster's node", st.Version, st.Address)
}

// checkOllamaModel: the model the stack uses is pulled.
func checkOllamaModel(st AIState) Result {
	if st.InClusterErr != "" {
		return Result{Status: Skip, Detail: "Ollama does not answer from the cluster"}
	}
	if !slices.Contains(st.Models, AIModel) {
		return withRemediation(fail("podman exec ollama ollama pull "+AIModel, "%s is not pulled (have: %s)", AIModel, orNone(st.Models)),
			repair("podman exec ollama ollama pull "+AIModel, "Download the required local model into the existing Ollama container.", "host-change", false))
	}
	return ok("%s pulled", AIModel)
}

// checkLangfuse: every ai app is Synced and Healthy and Langfuse's health
// endpoint answers OK through its Service.
func checkLangfuse(st AIState) Result {
	switch {
	case len(st.AppsNotReady) > 0:
		return fail("lab status; argocd app get <app>", "not ready: %s", strings.Join(st.AppsNotReady, ", "))
	case st.LangfuseErr != "":
		return fail("kubectl -n ai get pods; kubectl -n ai logs deploy/langfuse-web", "langfuse-web health: %s", firstLine(st.LangfuseErr))
	}
	return ok("%s healthy, %d ai apps Synced and Healthy", st.LangfuseVersion, st.Apps)
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// labPorts are the host ports lab publishes, each only on 127.0.0.1:
// Ollama, Zot, Moto and the Gateway.
var labPorts = map[int]string{11434: "Ollama", 5000: "lab-zot", 4566: "lab-moto", 8080: "Gateway HTTP", 8443: "Gateway HTTPS"}

// checkLANListeners fails when a lab port listens on anything but
// loopback: Ollama has no auth, and lab apps are reached through the SSH
// tunnel only. It reads /proc/net/tcp and tcp6, the same table ss reads.
func checkLANListeners(_ context.Context, sys System, _ Options) Result {
	var exposed []string
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := sys.ReadFile(f)
		if err != nil {
			return Result{Status: Skip, Detail: fmt.Sprintf("read %s: %v", f, err), Unavailable: true}
		}
		for _, l := range ParseListeners(string(b)) {
			name, ok := labPorts[l.Port]
			if ok && !l.IP.IsLoopback() {
				exposed = append(exposed, fmt.Sprintf("%s on %s", name, net.JoinHostPort(l.IP.String(), strconv.Itoa(l.Port))))
			}
		}
	}
	if len(exposed) > 0 {
		slices.Sort(exposed)
		return fail("publish on 127.0.0.1 only (Ollama: PublishPort=127.0.0.1:11434:11434 in its Quadlet)",
			"reachable from the LAN: %s", strings.Join(exposed, ", "))
	}
	return ok("lab ports listen on loopback only")
}

// Listener is a listening TCP socket.
type Listener struct {
	IP   net.IP
	Port int
}

// ParseListeners reads the LISTEN sockets (state 0A) of /proc/net/tcp or
// tcp6. Addresses are hex in host byte order, one 32-bit word at a time
// (little-endian on x86).
func ParseListeners(table string) []Listener {
	var out []Listener
	for _, line := range strings.Split(table, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		addr, port, found := strings.Cut(f[1], ":")
		if !found {
			continue
		}
		raw, err := hex.DecodeString(addr)
		if err != nil || (len(raw) != 4 && len(raw) != 16) {
			continue
		}
		for i := 0; i < len(raw); i += 4 {
			raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
		}
		p, err := strconv.ParseUint(port, 16, 16)
		if err != nil {
			continue
		}
		out = append(out, Listener{IP: net.IP(raw), Port: int(p)})
	}
	return out
}
