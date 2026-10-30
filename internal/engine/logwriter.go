package engine

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// timestampWriter prefixes each complete line with the time it was written,
// so a step log shows where the time went. It is safe for concurrent use.
type timestampWriter struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
	buf []byte // partial line waiting for its newline
}

func newTimestampWriter(w io.Writer, now func() time.Time) *timestampWriter {
	return &timestampWriter{w: w, now: now}
}

func (t *timestampWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		if err := t.line(t.buf[:i]); err != nil {
			return len(p), err
		}
		t.buf = t.buf[i+1:]
	}
}

// Flush writes a trailing partial line, if any.
func (t *timestampWriter) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) == 0 {
		return nil
	}
	err := t.line(t.buf)
	t.buf = nil
	return err
}

func (t *timestampWriter) line(l []byte) error {
	ts := t.now().Format("15:04:05.000 ")
	_, err := t.w.Write(append(append([]byte(ts), bytes.TrimRight(l, "\r")...), '\n'))
	return err
}
