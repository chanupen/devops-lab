package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/state"
)

// DrillActions run the drills on screen 4, the same calls as lab drills.
// Each is called off the key loop.
type DrillActions struct {
	Start  func(ctx context.Context, id string, random bool) (state.DrillRun, error)
	Verify func(ctx context.Context) (drills.Result, error)
	// Ack names a cause; right reports whether it was the drill's.
	Ack func(ctx context.Context, cause string) (run state.DrillRun, right bool, err error)
	// AckCauses are the causes ack takes for the running drill, in order.
	AckCauses func() []Cause
	Hint      func() (n, total int, hint string, err error)
	Abort     func(ctx context.Context) (state.DrillRun, error)
	// Expire restores the active drill once its time limit has passed;
	// an open TUI holds the lab lock, so the time-limit timer cannot.
	Expire func(ctx context.Context) (bool, error)
	// CheckAlert records when the drill's alert fired.
	CheckAlert func(ctx context.Context) (state.DrillRun, error)
}

// Cause is one suspect lab drills ack takes.
type Cause struct{ ID, Text string }

// causePicker is the list a opens: the learner picks the cause they found.
type causePicker struct {
	causes []Cause
	cursor int
}

// drillsModel is screen 4: the drills, the running one with its timers.
type drillsModel struct {
	cursor int
	busy   string    // the command running, if any
	hints  *[]string // hints shown this session; a pointer, so copies share it
	last   string    // the last verify's reason, or a finished run's times
	pick   *causePicker
}

func newDrillsModel() drillsModel { return drillsModel{hints: &[]string{}} }

// drillReq is a drill action waiting for y/N.
type drillReq struct {
	kind string // start, random, abort
	id   string
}

func (r drillReq) command() string {
	switch r.kind {
	case "start":
		return "lab drills start " + r.id
	case "random":
		return "lab drills start --random"
	}
	return "lab drills abort"
}

// drillMsg reports a finished drill action.
type drillMsg struct {
	command string
	run     state.DrillRun
	res     *drills.Result
	hint    string
	note    string // a flash for the action
	err     error
}

// alertEvery is how many file polls pass between alert checks while a
// drill runs (2s polls: every 30s).
const alertEvery = 15

func (m Model) drillsKey(k string) (tea.Model, tea.Cmd) {
	dm := &m.drills
	all := m.d.drills
	switch k {
	case "j", "down":
		dm.cursor = min(dm.cursor+1, max(0, len(all)-1))
	case "k", "up":
		dm.cursor = max(dm.cursor-1, 0)
	case "enter":
		if dm.cursor < len(all) {
			return m.askDrill(drillReq{kind: "start", id: all[dm.cursor].ID}, "Start drill "+all[dm.cursor].ID+"? It breaks the sample app on purpose")
		}
	case "R":
		return m.askDrill(drillReq{kind: "random"}, "Start a random drill? It breaks the sample app on purpose")
	case "x":
		if m.d.st.ActiveDrill == nil {
			m.flash = "no drill is running"
			return m, nil
		}
		return m.askDrill(drillReq{kind: "abort"}, "Abort the drill and put everything back?")
	case "a":
		return m.openCausePicker()
	case "h", "v":
		return m.drillAction(k)
	}
	return m, nil
}

// openCausePicker lists the causes ack takes; enter acks the selected one.
func (m Model) openCausePicker() (tea.Model, tea.Cmd) {
	if m.d.st.ActiveDrill == nil {
		m.flash = "no drill is running: enter starts the selected one, R a random one"
		return m, nil
	}
	if why := m.drillBlocked("lab drills ack <cause>"); why != "" {
		m.flash = why
		return m, nil
	}
	var causes []Cause
	if m.opts.Drills.AckCauses != nil {
		causes = m.opts.Drills.AckCauses()
	}
	if len(causes) == 0 {
		m.flash = "run it in a shell: $ lab drills ack --list"
		return m, nil
	}
	m.drills.pick = &causePicker{causes: causes}
	return m, nil
}

// pickKey handles a key while the cause list is open: j/k or a number
// moves, enter acks, esc closes it.
func (m Model) pickKey(k string) (tea.Model, tea.Cmd) {
	p := m.drills.pick
	switch k {
	case "esc", "q":
		m.drills.pick = nil
		m.flash = "cancelled: lab drills ack"
	case "j", "down":
		p.cursor = min(p.cursor+1, len(p.causes)-1)
	case "k", "up":
		p.cursor = max(p.cursor-1, 0)
	case "enter":
		c := p.causes[p.cursor]
		m.drills.pick = nil
		return m.ack(c.ID)
	default:
		if len(k) == 1 && k[0] >= '1' && k[0] <= '9' && int(k[0]-'1') < len(p.causes) {
			p.cursor = int(k[0] - '1')
		}
	}
	return m, nil
}

// ack runs lab drills ack <cause> in the background.
func (m Model) ack(cause string) (tea.Model, tea.Cmd) {
	command := "lab drills ack " + cause
	if why := m.drillBlocked(command); why != "" {
		m.flash = why
		return m, nil
	}
	acts := m.opts.Drills
	m.drills.busy, m.flash = command, statusProgressing.symbol()+" $ "+command
	return m, func() tea.Msg {
		run, right, err := acts.Ack(context.Background(), cause)
		note := fmt.Sprintf("detected after %s; now fix it, then v", clock(run.TimeTo(run.Acked)))
		if err == nil && !right {
			err = fmt.Errorf("not the cause: %s; the detect timer runs on (h for a hint)", cause)
		}
		return drillMsg{command: command, run: run, err: err, note: note}
	}
}

// drillBlocked says why a drill action cannot run now, or "".
func (m Model) drillBlocked(command string) string {
	switch {
	case m.d.readOnly != "":
		return "read-only: another lab is running; run it there: $ " + command
	case m.opts.Drills.Start == nil:
		return "run it in a shell: $ " + command
	case m.drills.busy != "":
		return statusProgressing.symbol() + " busy: $ " + m.drills.busy
	case m.op != nil:
		return statusProgressing.symbol() + " busy: " + m.op.req.Command() + " is running"
	}
	return ""
}

func (m Model) askDrill(r drillReq, question string) (tea.Model, tea.Cmd) {
	if why := m.drillBlocked(r.command()); why != "" {
		m.flash = why
		return m, nil
	}
	m.confirm = &confirmState{question: question, drill: &r}
	return m, nil
}

// runDrill starts a confirmed start or abort in the background.
func (m Model) runDrill(r drillReq) (Model, tea.Cmd) {
	if why := m.drillBlocked(r.command()); why != "" {
		m.flash = why
		return m, nil
	}
	acts, command := m.opts.Drills, r.command()
	m.drills.busy, m.flash = command, statusProgressing.symbol()+" $ "+command
	if r.kind == "abort" {
		return m, func() tea.Msg {
			run, err := acts.Abort(context.Background())
			return drillMsg{command: command, run: run, err: err, note: "everything the drill changed is back"}
		}
	}
	*m.drills.hints, m.drills.last = nil, ""
	return m, func() tea.Msg {
		run, err := acts.Start(context.Background(), r.id, r.kind == "random")
		return drillMsg{command: command, run: run, err: err, note: "drill started: the timer runs (a ack, v verify, h hint)"}
	}
}

// drillAction runs hint or verify for the running drill.
func (m Model) drillAction(k string) (tea.Model, tea.Cmd) {
	verb := map[string]string{"h": "hint", "v": "verify"}[k]
	command := "lab drills " + verb
	if m.d.st.ActiveDrill == nil {
		m.flash = "no drill is running: enter starts the selected one, R a random one"
		return m, nil
	}
	if why := m.drillBlocked(command); why != "" {
		m.flash = why
		return m, nil
	}
	acts := m.opts.Drills
	m.drills.busy, m.flash = command, statusProgressing.symbol()+" $ "+command
	switch k {
	case "h":
		return m, func() tea.Msg {
			n, total, h, err := acts.Hint()
			if err == nil && n <= total && !strings.HasPrefix(h, "No more hints") {
				h = fmt.Sprintf("%d/%d %s", n, total, h)
			}
			return drillMsg{command: command, hint: h, err: err}
		}
	}
	return m, func() tea.Msg {
		res, err := acts.Verify(context.Background())
		return drillMsg{command: command, run: res.Run, res: &res, err: err}
	}
}

// drillTick restores an expired drill and checks the alert, off the key
// loop, while this TUI can act.
func (m Model) drillTick() (Model, tea.Cmd) {
	r := m.d.st.ActiveDrill
	acts := m.opts.Drills
	if r == nil || m.d.readOnly != "" || m.drills.busy != "" || r.Phase != state.DrillRunning {
		return m, nil
	}
	if !m.now().Before(r.Deadline) && acts.Expire != nil {
		m.drills.busy = "lab drills abort --expired"
		return m, func() tea.Msg {
			done, err := acts.Expire(context.Background())
			note := ""
			if done {
				note = "the drill's time limit passed: everything it changed is back"
			}
			return drillMsg{command: "lab drills abort --expired", err: err, note: note}
		}
	}
	if acts.CheckAlert != nil && r.AlertFired.IsZero() && r.Alert != "" && m.ticks%alertEvery == 0 {
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			run, err := acts.CheckAlert(ctx)
			return drillMsg{command: "", run: run, err: err}
		}
	}
	return m, nil
}

func (m Model) drillDone(msg drillMsg) (tea.Model, tea.Cmd) {
	if msg.command == "" { // a background alert check
		return m, m.loadState()
	}
	m.drills.busy = ""
	switch {
	case msg.res != nil && !msg.res.Passed && msg.err == nil:
		m.drills.last = statusFailed.symbol() + " not recovered yet: " + msg.res.Reason
		m.flash = statusFailed.symbol() + " $ " + msg.command + ": " + msg.res.Reason
	case msg.err != nil:
		m.flash = statusFailed.symbol() + " $ " + msg.command + ": " + firstLine(msg.err.Error())
	case msg.hint != "":
		*m.drills.hints = append(*m.drills.hints, msg.hint)
		m.flash = "$ " + msg.command
	case msg.res != nil || msg.run.Outcome != "":
		m.drills.last = finishedText(msg.run)
		m.flash = statusHealthy.symbol() + " $ " + msg.command + ": " + msg.run.Outcome
	default:
		m.flash = statusHealthy.symbol() + " $ " + msg.command
		if msg.note != "" {
			m.flash += ": " + msg.note
		}
	}
	return m, m.loadState()
}

func clock(d time.Duration) string { return d.Round(time.Second).String() }

func clockOrDash(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return clock(d)
}

// finishedText is a finished run's outcome and times.
func finishedText(r state.DrillRun) string {
	alert := "—"
	if r.Alert != "" {
		alert = "did not fire"
		if !r.AlertFired.IsZero() {
			alert = clock(r.TimeTo(r.AlertFired))
		}
	}
	return fmt.Sprintf("%s drill %s %s · alert %s · detect %s · recover %s", statusHealthy.symbol(), r.Drill, r.Outcome,
		alert, clockOrDash(r.TimeTo(r.Acked)), clockOrDash(r.TimeTo(r.Recovered)))
}

// drillHeader is the running drill and its timer for the header, or "".
func drillHeader(d *data, now time.Time) string {
	r := d.st.ActiveDrill
	if r == nil {
		return ""
	}
	name := r.Drill
	if r.Random {
		name = "?"
	}
	switch r.Phase {
	case state.DrillRunning:
		limit := r.Deadline.Sub(r.Started)
		s := fmt.Sprintf("drill: %s %s/%s", name, mmss(now.Sub(r.Started)), mmss(limit))
		if !now.Before(r.Deadline) {
			return danger.Render(s + " expired")
		}
		return warn.Render(s)
	case state.DrillRestoring:
		return danger.Render("drill: " + name + " restoring")
	}
	return warn.Render("drill: " + name + " " + r.Phase)
}

func mmss(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// view renders the drills on the left and, on the right, the running drill
// with its timers, or the selected drill.
func (dm drillsModel) view(d *data, now time.Time) (left, right func(w int) string) {
	left = func(int) string {
		if len(d.drills) == 0 {
			return " no drills in drills/"
		}
		ready := map[string]bool{}
		for _, x := range drills.Eligible(d.drills, d.st) {
			ready[x.ID] = true
		}
		var b strings.Builder
		b.WriteString(bold.Render(" Drills") + "\n")
		for i, x := range d.drills {
			mark := statusHealthy.symbol()
			switch {
			case d.st.ActiveDrill != nil && d.st.ActiveDrill.Drill == x.ID && !d.st.ActiveDrill.Random:
				mark = statusProgressing.symbol()
			case !ready[x.ID]:
				mark = statusOff.symbol()
			}
			row := fmt.Sprintf(" %s %-11s %s", mark, x.ID, x.Level)
			if i == dm.cursor {
				row = lipgloss.NewStyle().Reverse(true).Render(row)
			}
			b.WriteString(row + "\n")
		}
		return b.String()
	}
	right = func(w int) string {
		if r := d.st.ActiveDrill; r != nil {
			return dm.runText(d, *r, now, w)
		}
		if dm.cursor >= len(d.drills) {
			return ""
		}
		return dm.drillText(d, d.drills[dm.cursor], w)
	}
	return left, right
}

func (dm drillsModel) runText(d *data, r state.DrillRun, now time.Time, w int) string {
	var b strings.Builder
	i := slices.IndexFunc(d.drills, func(x drills.Drill) bool { return x.ID == r.Drill })
	var x drills.Drill
	if i >= 0 {
		x = d.drills[i]
	}
	if r.Random {
		b.WriteString(bold.Render(" A random drill") + faint.Render("  (which one shows when it ends)") + "\n")
	} else {
		b.WriteString(bold.Render(" "+x.Title) + faint.Render("  "+r.Drill) + "\n")
	}
	switch r.Phase {
	case state.DrillRunning:
		left := r.Deadline.Sub(now)
		fmt.Fprintf(&b, " %s running %s · %s left · lab puts everything back at %s\n", statusProgressing.symbol(),
			mmss(now.Sub(r.Started)), mmss(left), r.Deadline.Local().Format("15:04"))
	case state.DrillRestoring:
		fmt.Fprintf(&b, " %s putting the lab back did not finish: %s · x tries again\n", statusFailed.symbol(), firstLine(r.Error))
	default:
		fmt.Fprintf(&b, " %s %s\n", statusProgressing.symbol(), r.Phase)
	}
	if r.Alert != "" {
		alert := "alert " + r.Alert
		if r.Random {
			alert = "the alert" // its name would give the drill away
		}
		if r.AlertFired.IsZero() {
			fmt.Fprintf(&b, " %s %s: not fired yet\n", statusOff.symbol(), alert)
		} else {
			fmt.Fprintf(&b, " %s %s fired after %s\n", statusHealthy.symbol(), alert, clock(r.TimeTo(r.AlertFired)))
		}
	}
	if r.Acked.IsZero() {
		fmt.Fprintf(&b, " %s detect: not yet · a names the cause you found\n", statusOff.symbol())
	} else {
		fmt.Fprintf(&b, " %s detect: %s\n", statusHealthy.symbol(), clock(r.TimeTo(r.Acked)))
	}
	fmt.Fprintf(&b, " hints %d/%d · verify runs %d", r.Hints, len(x.Hints), r.Verifies)
	if wrong := r.WrongAcks(); wrong > 0 {
		fmt.Fprintf(&b, " · wrong acks %d", wrong)
	}
	b.WriteString("\n")
	if p := dm.pick; p != nil {
		b.WriteString("\n" + bold.Render(" What did you find?") + faint.Render("  j/k or 1-9 · enter acks · esc cancels") + "\n")
		for i, c := range p.causes {
			row := fmt.Sprintf(" %d %s", i+1, c.Text)
			if i == p.cursor {
				row = lipgloss.NewStyle().Reverse(true).Render(row)
			}
			b.WriteString(row + "\n")
		}
	}
	if dm.last != "" {
		b.WriteString(hanging("", dm.last, w-1) + "\n")
	}
	b.WriteString("\n")
	page := x.Page
	if r.Random {
		page = RandomPage
	}
	b.WriteString(wrap(page, w-1) + "\n")
	if hints := *dm.hints; len(hints) > 0 {
		b.WriteString("\n" + bold.Render(" Hints") + "\n")
		for _, h := range hints {
			b.WriteString(hanging("", h, w-1) + "\n")
		}
	}
	return b.String()
}

// RandomPage is what a random drill tells the learner.
const RandomPage = "The pager goes off: users of the sample app at https://podinfo.localtest.me:8443 report errors. " +
	"Nobody says what changed. Find what is broken, fix it the way the team runs this cluster (GitOps " +
	"through Argo CD), and run lab drills verify."

func (dm drillsModel) drillText(d *data, x drills.Drill, w int) string {
	var b strings.Builder
	b.WriteString(bold.Render(" "+x.Title) + "\n")
	fmt.Fprintf(&b, " %s · %s · limit %s · needs %s\n", x.ID, x.Level, mmss(x.Limit()), strings.Join(x.Stacks, ", "))
	if x.Alert.Name != "" {
		fmt.Fprintf(&b, " alert: %s\n", x.Alert.Name)
	}
	best := drills.BestTimes(d.st.Drills)[x.ID]
	if best.Runs > 0 {
		fmt.Fprintf(&b, " %d run(s), %d recovered · best detect %s, recover %s\n", best.Runs, best.Recovered,
			clockOrDash(best.Detect), clockOrDash(best.Fixed))
	}
	if dm.last != "" {
		b.WriteString(hanging("", dm.last, w-1) + "\n")
	}
	b.WriteString("\n" + wrap(x.Page, w-1) + "\n\n")
	b.WriteString(faint.Render(" enter starts it · R starts a random drill · "+"$ lab drills start "+x.ID) + "\n")
	return b.String()
}
