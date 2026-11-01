package tui

// logLines is how many lines a log view keeps; older lines are dropped.
const logLines = 5000

// ring keeps the last cap lines appended to it.
type ring struct {
	buf   []string
	start int // index of the oldest line once the buffer is full
	cap   int
	total int // lines ever appended, so a view can say how many were dropped
}

func newRing(capacity int) *ring { return &ring{cap: capacity} }

// add appends lines, dropping the oldest past the capacity.
func (r *ring) add(lines ...string) {
	for _, l := range lines {
		r.total++
		if len(r.buf) < r.cap {
			r.buf = append(r.buf, l)
			continue
		}
		r.buf[r.start] = l
		r.start = (r.start + 1) % r.cap
	}
}

// len is how many lines the ring holds.
func (r *ring) len() int { return len(r.buf) }

// dropped is how many lines fell off the front.
func (r *ring) dropped() int { return r.total - len(r.buf) }

// slice returns lines [from, to) in order, oldest first.
func (r *ring) slice(from, to int) []string {
	from, to = max(0, from), min(to, len(r.buf))
	out := make([]string, 0, max(0, to-from))
	for i := from; i < to; i++ {
		out = append(out, r.buf[(r.start+i)%len(r.buf)])
	}
	return out
}
