package kube

import (
	"fmt"
	"io"
	"sync"

	"github.com/go-logr/logr/funcr"
	"k8s.io/klog/v2"
)

// Detailer is a log that keeps detail apart from progress lines: client-go's
// own messages (a watch that fails while RBAC is not ready yet) go to
// Detail(), lab's status lines to the log itself. lab labs start writes
// detail to the op log only.
type Detailer interface {
	io.Writer
	Detail() io.Writer
}

// DetailOf is where w's detail goes: Detail() for a Detailer, else w.
func DetailOf(w io.Writer) io.Writer {
	if d, ok := w.(Detailer); ok {
		return d.Detail()
	}
	return w
}

// clientGoLogger sends what client-go logs, errors and V(0) info, to
// log's detail instead of klog's stderr. The informer keeps retrying on
// its own; its messages land in the op log.
func clientGoLogger(log io.Writer) klog.Logger {
	w := &lockedWriter{w: DetailOf(log)}
	return funcr.New(func(prefix, args string) {
		if prefix != "" {
			prefix += ": "
		}
		_, _ = fmt.Fprintf(w, "client-go: %s%s\n", prefix, args)
	}, funcr.Options{})
}

// lockedWriter serialises writes from client-go's goroutines.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
