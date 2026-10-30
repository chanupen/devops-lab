package local

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"sigs.k8s.io/kind/pkg/log"
)

// kindLogger writes kind's user-facing messages (V(0), warnings, errors) as
// indented lines and drops debug output and expected warnings.
type kindLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func newKindLogger(w io.Writer) *kindLogger { return &kindLogger{w: w} }

func (l *kindLogger) line(prefix, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.w, "    %s%s\n", prefix, msg)
}

// podmanNotice is kind's warning when the Podman provider is chosen; the lab
// always chooses it, so it is noise.
const podmanNotice = "enabling experimental podman provider"

func (l *kindLogger) Warn(m string) {
	if !strings.Contains(m, podmanNotice) {
		l.line("warning: ", m)
	}
}

func (l *kindLogger) Warnf(f string, a ...any)         { l.Warn(fmt.Sprintf(f, a...)) }
func (l *kindLogger) Error(m string)                   { l.line("error: ", m) }
func (l *kindLogger) Errorf(f string, a ...any)        { l.line("error: ", fmt.Sprintf(f, a...)) }
func (l *kindLogger) V(level log.Level) log.InfoLogger { return infoLogger{l: l, on: level == 0} }

type infoLogger struct {
	l  *kindLogger
	on bool
}

func (i infoLogger) Info(m string) {
	if i.on {
		i.l.line("", m)
	}
}

func (i infoLogger) Infof(f string, a ...any) {
	if i.on {
		i.l.line("", fmt.Sprintf(f, a...))
	}
}

func (i infoLogger) Enabled() bool { return i.on }
