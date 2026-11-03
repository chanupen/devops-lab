package local

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"sigs.k8s.io/kind/pkg/log"
)

// kindLogger writes everything kind logs, at every verbosity, to the step
// log. Debug lines are tagged with their level; nothing is filtered.
type kindLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func newKindLogger(w io.Writer) *kindLogger { return &kindLogger{w: w} }

func (l *kindLogger) line(prefix, msg string) {
	msg = strings.TrimRight(msg, "\n")
	if strings.TrimSpace(msg) == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range strings.Split(msg, "\n") {
		_, _ = fmt.Fprintf(l.w, "kind: %s%s\n", prefix, m)
	}
}

func (l *kindLogger) Warn(m string)                    { l.line("warning: ", m) }
func (l *kindLogger) Warnf(f string, a ...any)         { l.Warn(fmt.Sprintf(f, a...)) }
func (l *kindLogger) Error(m string)                   { l.line("error: ", m) }
func (l *kindLogger) Errorf(f string, a ...any)        { l.Error(fmt.Sprintf(f, a...)) }
func (l *kindLogger) V(level log.Level) log.InfoLogger { return infoLogger{l: l, level: level} }

type infoLogger struct {
	l     *kindLogger
	level log.Level
}

func (i infoLogger) prefix() string {
	if i.level == 0 {
		return ""
	}
	return fmt.Sprintf("[v%d] ", i.level)
}

func (i infoLogger) Info(m string)            { i.l.line(i.prefix(), m) }
func (i infoLogger) Infof(f string, a ...any) { i.l.line(i.prefix(), fmt.Sprintf(f, a...)) }
func (i infoLogger) Enabled() bool            { return true }

// Detailer is a log that keeps detail apart from progress: kind's own
// output and node diagnostics go to Detail(), progress lines to the log
// itself. lab labs start writes detail to the op log only, unless -v.
type Detailer interface {
	io.Writer
	Detail() io.Writer
}

// detail is where w's detail goes: Detail() for a Detailer, else w.
func detail(w io.Writer) io.Writer {
	if d, ok := w.(Detailer); ok {
		return d.Detail()
	}
	return w
}
