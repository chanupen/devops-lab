// Package tui is the lab's terminal UI. The first frame renders from
// ~/.lab/state.json alone; live state then arrives from client-go informers
// as messages, so the key loop never waits on the cluster.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/drills"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/labs"
	"github.com/chanupen/devops-lab/internal/state"
)

// Options configure the TUI.
type Options struct {
	Config     config.Config
	ConfigPath string      // the lab.yaml in use; "" when the defaults apply
	State      state.State // loaded before New; the first frame renders from it alone
	StatePath  string      // re-read when it changes on disk
	Kubeconfig string      // the lab cluster; watched while it exists
	Catalog    []engine.Stack
	AppURL     func(host string) string // how the mode reaches a Gateway hostname
	AccessHint string                   // one line under the app URLs
	// Connect builds clients for a kubeconfig. Nil means the real cluster;
	// tests pass fakes.
	Connect func(kubeconfig string) (*kube.Clients, error)
	// FilePoll is how often state.json and the kubeconfig are checked for
	// changes made by lab up or lab down in another shell. Default 2s.
	FilePoll time.Duration
	// FirstView, when set, is called once, when the first View the running
	// program asks for returns. LAB_TRACE_FIRSTFRAME uses it.
	FirstView func()
	// Actions runs lab up, lab down and the read-only reports. Nil turns the
	// action keys into hints.
	Actions Actions
	// LookPath finds k9s for K. Default exec.LookPath.
	LookPath func(file string) (string, error)
	// BranchExists reports whether a GitOps branch still exists on the
	// remote. Nil skips the deleted-branch check.
	BranchExists func(ctx context.Context, branch string) (bool, error)
	// Now is the clock for durations on screen. Default time.Now.
	Now func() time.Time
	// ReadOnly, when set, names the lab that holds the lab lock: the TUI
	// opens read-only, with views and reports but no actions. Release is
	// called on exit when this TUI holds the lock. Claim tries to take it,
	// on the file check, so a read-only TUI turns read-write once the other
	// lab is done; it returns an error wrapping state.ErrBusy while held.
	ReadOnly string
	Release  func()
	Claim    func() (release func(), err error)
	// Units lists the lab's containers (cluster nodes, the image cache),
	// so Home can say a cluster is stopped rather than off. Nil leaves
	// them out. It runs in the background, at start and every few polls.
	Units func(ctx context.Context) ([]Unit, error)
	// Labs are the labs in labs/, loaded before New. LabVerify and LabHint
	// verify a step of the started lab and show its next hint; nil turns
	// v and h into hints for the CLI.
	Labs      []labs.Lab
	LabVerify func(ctx context.Context, step int) (labs.Result, error)
	LabHint   func(step int) (int, string, error)
	// DrillList are the drills in drills/; Drills runs them. Nil actions
	// turn the drill keys into hints for the CLI.
	DrillList []drills.Drill
	Drills    DrillActions
}

// Screens, in tab order. The key is the screen's number in the design;
// 6 comes in a later release.
const (
	screenHome = iota
	screenStacks
	screenLabs
	screenDrills
	screenOps
)

var screens = []struct {
	key  string
	name string
}{{"1", "Home"}, {"2", "Stacks"}, {"3", "Labs"}, {"4", "Drills"}, {"5", "Ops"}}

// data is what every screen renders from.
type data struct {
	cfg     config.Config
	st      state.State
	live    cluster
	ctx     string // kube context name
	order   []string
	deps    map[string][]string
	appURL  func(string) string
	hint    string
	cfgPath string
	// readOnly names the lab holding the lock while this TUI cannot act.
	readOnly string
	labs     []labs.Lab
	drills   []drills.Drill
	// branchChecked and branchGone record the deleted-branch check by
	// revision. Maps, so copies of the model share them.
	branchChecked map[string]bool
	branchGone    map[string]bool
	// units are the lab's containers, once a Units call has answered.
	units      []Unit
	unitsKnown bool
}

// Unit is a part of the lab made of containers, as Home shows it.
type Unit struct {
	Kind    string // UnitCluster, UnitScratch or UnitCache
	Name    string
	Running bool   // every container of it
	Fix     string // the command that starts the stopped ones
	Uptime  labs.Uptime
	// Version is a running cluster's Kubernetes version from its node
	// ("v1.36.4"); VersionErr says why it could not be read.
	Version    string
	VersionErr string
}

// Unit kinds.
const (
	UnitCluster = "cluster"     // the main lab's cluster
	UnitScratch = "scratch"     // a scratch lab's cluster
	UnitCache   = "image cache" // the pull-through registry
)

// unit returns the unit of this kind and name.
func (d *data) unit(kind, name string) (Unit, bool) {
	for _, u := range d.units {
		if u.Kind == kind && (name == "" || u.Name == name) {
			return u, true
		}
	}
	return Unit{}, false
}

// clusterStopped reports whether the main cluster's nodes are there but
// stopped, as after a reboot.
func (d *data) clusterStopped() bool {
	u, ok := d.unit(UnitCluster, "")
	return ok && !u.Running
}

// labUptime is the uptime of scratch lab l's cluster: nil while unknown,
// zero (gone) when the list has no such cluster.
func (d *data) labUptime(l labs.Lab) *labs.Uptime {
	if !d.unitsKnown || l.Needs != labs.NeedsScratch {
		return nil
	}
	if u, ok := d.unit(UnitScratch, labs.ClusterName(l)); ok {
		return &u.Uptime
	}
	return &labs.Uptime{}
}

// revisionGone reports whether the recorded revision's branch was deleted.
func (d *data) revisionGone() bool {
	return d.st.Revision != engine.DefaultRevision && d.branchGone[d.st.Revision]
}

// mode is the recorded mode, or the configured one before the first apply.
func (d *data) mode() string {
	if d.st.Mode != "" {
		return d.st.Mode
	}
	return d.cfg.Mode
}

// Model is the root Bubble Tea model. It owns the shared data and hands
// keys to the current screen's model.
type Model struct {
	opts   Options
	d      data
	screen int
	home   homeModel
	stacks stacksModel
	labs   labsModel
	drills drillsModel
	ops    opsModel
	help   bool
	flash  string // shown in the ops strip until the next key
	width  int
	height int

	op       *opState      // the running operation, if any
	opEvents chan tea.Msg  // its progress and result
	confirm  *confirmState // a pending yes/no question
	palette  *palette      // the open command palette
	view     *viewer       // the open log or report view
	viewPath string        // the log file the view is loading
	lease    *lease        // the lab lock, once this TUI holds it
	claiming bool          // a Claim is in flight

	events   chan tea.Msg
	gen      int // connect generation; reports from older watchers are dropped
	cancel   context.CancelFunc
	kubeMod  time.Time
	kubeSeen bool
	stateMod time.Time
	ticks    int // file polls so far
}

// New builds the model. It does no I/O, so the first View is as fast as
// rendering the state it was given.
func New(opts Options) Model {
	if opts.Connect == nil {
		opts.Connect = connect
	}
	if opts.FilePoll == 0 {
		opts.FilePoll = 2 * time.Second
	}
	if opts.FirstView != nil {
		opts.FirstView = sync.OnceFunc(opts.FirstView)
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	d := data{cfg: opts.Config, st: opts.State, appURL: opts.AppURL, hint: opts.AccessHint, cfgPath: opts.ConfigPath, deps: map[string][]string{},
		branchChecked: map[string]bool{}, branchGone: map[string]bool{}, readOnly: opts.ReadOnly, labs: opts.Labs, drills: opts.DrillList}
	for _, s := range opts.Catalog {
		d.order = append(d.order, s.Name)
		d.deps[s.Name] = s.DependsOn
	}
	if g, err := engine.NewGraph(opts.Catalog); err == nil {
		if order, err := g.Order(g.Names()...); err == nil {
			d.order = order
		}
	}
	return Model{opts: opts, d: d, stacks: newStacksModel(), labs: newLabsModel(), drills: newDrillsModel(), width: 100, height: 30,
		events: make(chan tea.Msg, 1), opEvents: make(chan tea.Msg, 1), gen: 1, lease: &lease{release: opts.Release}}
}

// lease holds the release for the lab lock. A pointer, so every copy of the
// model releases the same one, once.
type lease struct {
	once    sync.Once
	release func()
}

func (l *lease) drop() {
	if l != nil && l.release != nil {
		l.once.Do(l.release)
	}
}

// claimMsg reports a background attempt to take the lab lock.
type claimMsg struct {
	release func()
	err     error
}

// claim tries to take the lab lock off the key loop. Nil unless read-only.
func (m Model) claim() tea.Cmd {
	if m.d.readOnly == "" || m.opts.Claim == nil || m.claiming {
		return nil
	}
	c := m.opts.Claim
	return func() tea.Msg {
		release, err := c()
		return claimMsg{release: release, err: err}
	}
}

func (m Model) now() time.Time { return m.opts.Now() }

// Run shows the TUI until the user quits or ctx ends.
func Run(ctx context.Context, opts Options) error {
	m0 := New(opts)
	defer m0.lease.drop() // the same lease as the final model's
	final, err := tea.NewProgram(m0, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if m, ok := final.(Model); ok {
		m.stop()
	}
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

// Messages from background commands.
type (
	connectedMsg struct {
		gen     int
		cancel  context.CancelFunc
		context string
		mod     time.Time
		missing bool
		err     error
	}
	filesMsg struct {
		state, kube time.Time
		kubeExists  bool
	}
	stateMsg struct {
		st  state.State
		err error
	}
	unitsMsg struct {
		units []Unit
		err   error
	}
	tickMsg struct{}
)

// Init starts the cluster connection, the watcher listener, the file check
// and, for a branch revision, the deleted-branch check.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.connect(m.gen), m.listen(), m.tick(), m.checkBranch(), m.checkUnits())
}

// unitsEvery is how many file polls pass between container checks.
const unitsEvery = 5

// checkUnits lists the lab's containers off the key loop.
func (m Model) checkUnits() tea.Cmd {
	list := m.opts.Units
	if list == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		units, err := list(ctx)
		return unitsMsg{units: units, err: err}
	}
}

func (m Model) listen() tea.Cmd {
	ch := m.events
	return func() tea.Msg { return <-ch }
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.opts.FilePoll, func(time.Time) tea.Msg { return tickMsg{} })
}

// connect reads the kubeconfig and starts the watcher for gen.
func (m Model) connect(gen int) tea.Cmd {
	path, newClients, out := m.opts.Kubeconfig, m.opts.Connect, m.events
	return func() tea.Msg {
		fi, err := os.Stat(path)
		if err != nil {
			return connectedMsg{gen: gen, missing: true}
		}
		name := ""
		if kc, err := clientcmd.LoadFromFile(path); err == nil {
			name = kc.CurrentContext
		}
		c, err := newClients(path)
		if err != nil {
			return connectedMsg{gen: gen, context: name, mod: fi.ModTime(), err: err}
		}
		ctx, cancel := context.WithCancel(context.Background())
		go watch(ctx, c, gen, out, redrawEvery)
		return connectedMsg{gen: gen, cancel: cancel, context: name, mod: fi.ModTime()}
	}
}

// checkFiles reports the modification times of state.json and the kubeconfig.
func (m Model) checkFiles() tea.Cmd {
	statePath, kubePath := m.opts.StatePath, m.opts.Kubeconfig
	return func() tea.Msg {
		var fm filesMsg
		if fi, err := os.Stat(statePath); err == nil {
			fm.state = fi.ModTime()
		}
		if fi, err := os.Stat(kubePath); err == nil {
			fm.kube, fm.kubeExists = fi.ModTime(), true
		}
		return fm
	}
}

func (m Model) loadState() tea.Cmd {
	path := m.opts.StatePath
	return func() tea.Msg {
		st, err := state.NewStore(path).Load()
		return stateMsg{st: st, err: err}
	}
}

func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return m.key(msg)
	case liveMsg:
		var units tea.Cmd
		if msg.gen == m.gen {
			// The cluster went down or came back: was it stopped?
			if msg.c.up != m.d.live.up {
				units = m.checkUnits()
			}
			m.d.live = msg.c
		}
		return m, tea.Batch(m.listen(), units)
	case unitsMsg:
		m.d.units, m.d.unitsKnown = msg.units, msg.err == nil
		return m, nil
	case connectedMsg:
		if msg.gen != m.gen {
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		m.cancel, m.d.ctx, m.kubeMod, m.kubeSeen = msg.cancel, msg.context, msg.mod, !msg.missing
		switch {
		case msg.missing:
			m.d.live = cluster{known: true, reason: "no cluster"}
		case msg.err != nil:
			m.d.live = cluster{known: true, reason: firstLine(msg.err.Error())}
		}
	case tickMsg:
		c := m.claim()
		if c != nil {
			m.claiming = true
		}
		m.ticks++
		var units tea.Cmd
		if m.ticks%unitsEvery == 0 {
			units = m.checkUnits()
		}
		var drill tea.Cmd
		m, drill = m.drillTick()
		return m, tea.Batch(m.checkFiles(), c, units, drill)
	case claimMsg:
		m.claiming = false
		var busy *state.BusyError
		switch {
		case msg.err == nil:
			m.lease.release, m.d.readOnly = msg.release, ""
			m.flash = statusHealthy.symbol() + " the other lab is done; this TUI can act now"
		case errors.As(msg.err, &busy) && busy.Holder != "":
			m.d.readOnly = busy.Holder
		}
	case opProgressMsg:
		return m.applyProgress(msg.batch), m.listenOp()
	case opDoneMsg:
		return m.finishOp(msg)
	case planMsg:
		return m.confirmFor(msg), nil
	case labVerifyMsg, labHintMsg:
		return m.labDone(msg)
	case drillMsg:
		return m.drillDone(msg)
	case reportMsg:
		m.flash = ""
		lines := newRing(logLines)
		lines.add(strings.Split(strings.TrimRight(msg.out, "\n"), "\n")...)
		v := &viewer{title: "$ lab " + msg.name, sub: "output of the CLI command; esc closes", lines: lines}
		if msg.err != nil {
			v.err = "exit: " + firstLine(msg.err.Error())
		}
		m.view = v
	case logMsg:
		if m.view != nil && msg.path == m.viewPath {
			m.view.loading = false
			if msg.err != nil {
				m.view.err = firstLine(msg.err.Error())
			} else {
				m.view.lines = msg.lines
			}
		}
	case k9sDoneMsg:
		m.flash = "back from k9s"
		if msg.err != nil {
			m.flash = statusFailed.symbol() + " k9s: " + firstLine(msg.err.Error())
		}
	case branchMsg:
		if msg.err == nil {
			m.d.branchGone[msg.rev] = !msg.exists
		}
	case filesMsg:
		var cmds []tea.Cmd
		if !msg.state.Equal(m.stateMod) {
			m.stateMod = msg.state
			cmds = append(cmds, m.loadState())
		}
		if msg.kubeExists != m.kubeSeen || (msg.kubeExists && !msg.kube.Equal(m.kubeMod)) {
			// lab up or lab down replaced the cluster: start over with new clients.
			m.stop()
			m.gen++
			m.kubeSeen, m.kubeMod = msg.kubeExists, msg.kube
			m.d.live, m.d.ctx = cluster{}, ""
			cmds = append(cmds, m.connect(m.gen))
		}
		return m, tea.Batch(append(cmds, m.tick())...)
	case stateMsg:
		if msg.err == nil {
			m.d.st = msg.st
			return m, m.checkBranch()
		}
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Keys typed faster than one read (common over SSH) arrive as one
	// message with several runes; handle them one at a time.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
		var next tea.Model = m
		var cmds []tea.Cmd
		for _, r := range msg.Runes {
			var cmd tea.Cmd
			next, cmd = next.(Model).key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			cmds = append(cmds, cmd)
		}
		return next, tea.Batch(cmds...)
	}
	m.flash = ""
	k := msg.String()
	if k == "ctrl+c" {
		return m.quit()
	}
	// Overlays take keys first: a question, the palette, a text filter, a
	// log view, help.
	switch {
	case m.confirm != nil:
		c := m.confirm
		m.confirm = nil
		if k == "y" || k == "Y" {
			if c.drill != nil {
				return m.runDrill(*c.drill)
			}
			return m.request(c.req)
		}
		m.flash = "cancelled: " + c.command()
		return m, nil
	case m.palette != nil:
		switch k {
		case "esc":
			m.palette = nil
			return m, nil
		case "enter":
			e, ok := m.palette.selected()
			m.palette = nil
			if !ok {
				return m, nil
			}
			return m.runEntry(e)
		}
		return m, m.palette.key(msg)
	case m.screen == screenStacks && m.stacks.filtering:
		var cmd tea.Cmd
		m.stacks, cmd = m.stacks.update(msg, &m.d)
		return m, cmd
	case m.view != nil:
		switch k {
		case "esc", "q":
			m.view, m.viewPath = nil, ""
		default:
			m.view.key(msg, m.bodyHeight())
		}
		return m, nil
	case m.help:
		if k == "q" {
			return m.quit()
		}
		m.help = false
		return m, nil
	}
	for i, s := range screens {
		if k == s.key {
			m.screen = i
			return m, nil
		}
	}
	switch k {
	case "q":
		return m.quit()
	case "?":
		m.help = true
	case "6":
		m.flash = "screen " + k + " comes in a later release"
	case ":":
		m.palette = newPalette(m.d.order)
		return m, textinput.Blink
	case "U":
		return m.request(Request{})
	case "K":
		return m.k9s()
	case "u", "d", "r":
		return m.stackAction(k)
	default:
		switch m.screen {
		case screenStacks:
			var cmd tea.Cmd
			m.stacks, cmd = m.stacks.update(msg, &m.d)
			return m, cmd
		case screenLabs:
			return m.labsKey(k)
		case screenDrills:
			return m.drillsKey(k)
		case screenOps:
			if k == "enter" {
				return m.openOp()
			}
			m.ops = m.ops.update(msg, opsRows(&m.d, m.op != nil))
		}
	}
	return m, nil
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	m.stop()
	if m.op != nil {
		m.op.cancel() // the engine stops before its next step
	}
	return m, tea.Quit
}

// stackAction handles u, d and r, which act on the stack selected on the
// Stacks screen.
func (m Model) stackAction(k string) (tea.Model, tea.Cmd) {
	name := ""
	if m.screen == screenStacks {
		name = m.stacks.selected(&m.d)
	}
	if name == "" {
		m.flash = k + " acts on the selected stack: open 2 Stacks first (U brings up the whole profile)"
		return m, nil
	}
	switch k {
	case "u":
		return m.request(Request{Stacks: []string{name}})
	case "d":
		return m.askDown(Request{Down: true, Stacks: []string{name}})
	default: // r
		if m.d.st.Stacks[name].Status != state.Failed {
			m.flash = "r retries a failed stack; " + name + " has not failed (u brings it up)"
			return m, nil
		}
		return m.request(Request{Stacks: []string{name}})
	}
}

// runEntry runs a palette entry the way its key would.
func (m Model) runEntry(e paletteEntry) (tea.Model, tea.Cmd) {
	switch {
	case e.req != nil && e.req.Down:
		return m.askDown(*e.req)
	case e.req != nil:
		return m.request(*e.req)
	default:
		return m.runReport(e.report)
	}
}

// bodyHeight is the number of rows between the tab bar and the ops strip.
func (m Model) bodyHeight() int { return max(m.height, 10) - 6 }

// View renders the four zones: header, tabs, the list and detail split, and
// the ops strip with the key hints under it.
func (m Model) View() string {
	if m.opts.FirstView != nil {
		defer m.opts.FirstView()
	}
	w := max(m.width, 40)
	bodyH := m.bodyHeight()

	var b strings.Builder
	b.WriteString(fitLine(m.header(), w) + "\n")
	b.WriteString(fitLine(m.tabs(), w) + "\n")
	full := ""
	switch {
	case m.palette != nil:
		full = m.palette.render(bodyH)
	case m.view != nil:
		full = m.view.render(bodyH)
	case m.help:
		full = helpFor(m.screen)
	case m.screen == screenOps:
		full = m.ops.view(&m.d, m.op, m.now(), bodyH)
	}
	if full != "" {
		b.WriteString(strings.Repeat("─", w) + "\n")
		for _, l := range fit(full, w, bodyH) {
			b.WriteString(l + "\n")
		}
		b.WriteString(strings.Repeat("─", w) + "\n")
	} else {
		var left, right string
		switch m.screen {
		case screenHome:
			left, right = m.home.view(&m.d)
		case screenStacks:
			left, right = m.stacks.view(&m.d, bodyH)
		}
		leftW := leftWidth(left, w)
		rightW := w - leftW - 1
		if m.screen == screenLabs || m.screen == screenDrills {
			l, r := m.labs.view(&m.d, m.now(), bodyH)
			if m.screen == screenDrills {
				l, r = m.drills.view(&m.d, m.now())
			}
			left = l(0)
			leftW = leftWidth(left, w)
			rightW = w - leftW - 1
			right = r(rightW)
		}
		b.WriteString(strings.Repeat("─", leftW) + "┬" + strings.Repeat("─", rightW) + "\n")
		l, r := fit(left, leftW, bodyH), fit(right, rightW, bodyH)
		for i := range bodyH {
			b.WriteString(l[i] + "│" + r[i] + "\n")
		}
		b.WriteString(strings.Repeat("─", leftW) + "┴" + strings.Repeat("─", rightW) + "\n")
	}
	b.WriteString(fitLine(m.opsStrip(), w) + "\n")
	b.WriteString(fitLine(faint.Render(" "+m.hints()), w))
	return b.String()
}

func (m Model) header() string {
	d := &m.d
	ctx := d.ctx
	if ctx == "" {
		ctx = "—"
	}
	note := ""
	if d.readOnly != "" {
		note = "  " + warn.Render("read-only: another lab is running ("+holderPID(d.readOnly)+")")
	}
	if dh := drillHeader(d, m.now()); dh != "" {
		note += "  " + dh
	}
	return fmt.Sprintf("%s%s  mode: %s  ctx: %s  health: %s  rev: %s  ttl: —",
		lipgloss.NewStyle().Bold(true).Reverse(true).Render(" lab "), note, d.mode(), ctx, health(d), revision(d))
}

// holderPID cuts "pid 42" out of a lock holder such as
// "lab up (pid 42 on /dev/pts/3, since 17:20)", for the header.
func holderPID(holder string) string {
	if i := strings.Index(holder, "pid "); i >= 0 {
		end := i + 4
		for end < len(holder) && holder[end] >= '0' && holder[end] <= '9' {
			end++
		}
		return holder[i:end]
	}
	return holder
}

// health summarises the cluster in a few characters.
func health(d *data) string {
	c := d.live
	switch {
	case !c.known && len(d.st.Stacks) == 0:
		return statusOff.symbol() + " off"
	case !c.known:
		return statusProgressing.symbol() + " connecting"
	case !c.up && d.clusterStopped():
		return statusOff.symbol() + " stopped"
	case !c.up:
		return statusOff.symbol() + " off"
	case len(c.apps) == 0:
		s := statusHealthy
		if c.nodesReady < c.nodes {
			s = statusProgressing
		}
		return fmt.Sprintf("%s %d/%d nodes", s.symbol(), c.nodesReady, c.nodes)
	}
	ready, failed := 0, 0
	for _, a := range c.apps {
		switch appStatus(a) {
		case statusHealthy:
			ready++
		case statusFailed:
			failed++
		}
	}
	s := statusProgressing
	switch {
	case failed > 0:
		s = statusFailed
	case ready == len(c.apps):
		s = statusHealthy
	}
	return fmt.Sprintf("%s %d/%d apps", s.symbol(), ready, len(c.apps))
}

// revision is the GitOps revision, highlighted when it is not main and red
// when its branch is gone from the remote.
func revision(d *data) string {
	st := d.st
	switch {
	case !st.IsApplied(engine.RevisionStack):
		return "—"
	case st.Revision == engine.DefaultRevision:
		return st.Revision
	case st.Revision == "":
		return warn.Render("unrecorded")
	case d.revisionGone():
		return danger.Render(st.Revision + " (deleted)")
	default:
		return warn.Render(st.Revision)
	}
}

// deletedHint is shown next to a revision whose branch is gone.
const deletedHint = "press U or run lab up to return to main"

func (m Model) tabs() string {
	var parts []string
	for i, s := range screens {
		t := fmt.Sprintf(" %s %s ", s.key, s.name)
		if i == m.screen {
			t = lipgloss.NewStyle().Reverse(true).Render(t)
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " ")
}

// opsStrip shows the running operation, else a message for the last key,
// else the latest operation.
func (m Model) opsStrip() string {
	if m.confirm != nil {
		return " " + warn.Render(m.confirm.question) + "  y/N  " + faint.Render("$ "+m.confirm.command())
	}
	if m.op != nil {
		s := " ops ▸ "
		if m.flash != "" { // e.g. busy: say why the key did nothing, then what runs
			s += m.flash + "  ·  "
		}
		s += statusProgressing.symbol() + " $ " + m.op.req.Command() + "  " +
			m.now().Sub(m.op.started).Round(time.Second).String()
		if m.op.step != "" {
			s += "  " + m.op.step
		}
		if m.op.last != "" {
			s += "  " + faint.Render(m.op.last)
		}
		return s
	}
	if m.flash != "" {
		return " ops ▸ " + m.flash
	}
	ops := m.d.st.Ops
	if len(ops) == 0 {
		return faint.Render(" ops ▸ none yet")
	}
	return " ops ▸ " + opLine(ops[len(ops)-1])
}

func (m Model) hints() string {
	switch {
	case m.confirm != nil:
		return "y confirm  any other key cancels"
	case m.palette != nil:
		return "type to match  ↑/↓ choose  enter run  esc close"
	case m.view != nil:
		return "j/k scroll  space/b page  g/G top/end  esc close"
	case m.help:
		return "any key closes help  q quit"
	case m.screen == screenStacks && m.stacks.filtering:
		return "type to filter  enter keep  esc clear"
	case m.screen == screenStacks:
		return "j/k move  u up  d down  r retry  / filter  : palette  K k9s  ? help  q quit"
	case m.screen == screenOps:
		return "j/k move  enter output  U up  : palette  K k9s  ? help  q quit"
	case m.screen == screenLabs && m.labs.open:
		return "j/k scroll  n/p next/previous step  v verify  h hint  esc back  ? help  q quit"
	case m.screen == screenLabs:
		return "j/k move  enter current step  v verify  h hint  n/p steps  ? help  q quit"
	case m.screen == screenDrills && m.d.st.ActiveDrill != nil:
		return "a found it (ack)  v verify  h hint  x abort  ? help  q quit"
	case m.screen == screenDrills:
		return "j/k move  enter start  R random drill  ? help  q quit"
	case m.d.readOnly != "":
		return "read-only: views and reports only  1 home  2 stacks  3 labs  4 drills  5 ops  : palette  K k9s  ? help  q quit"
	default:
		return "1 home  2 stacks  3 labs  4 drills  5 ops  U up  : palette  K k9s  ? help  q quit"
	}
}

// opLine is one ops log entry: symbol, time, command, stack action, duration.
func opLine(op state.Op) string {
	d := op.Finished.Sub(op.Started).Round(100 * time.Millisecond)
	s := fmt.Sprintf("%s %s  %s  %s %s  %s", opStatus(op).symbol(),
		op.Started.Local().Format("Jan 02 15:04"), op.Command, op.Stack, op.Action, d)
	if op.Error != "" {
		s += "  " + faint.Render(firstLine(op.Error))
	}
	return s
}

// Pane widths. The left pane sizes to its widest line, between minLeft and
// whatever leaves minRight for the detail pane; only past that is it cut.
const (
	minLeft  = 30
	minRight = 40
)

// leftWidth is the left pane's width for content left in a window w wide.
func leftWidth(left string, w int) int {
	widest := 0
	for _, l := range strings.Split(left, "\n") {
		widest = max(widest, ansi.StringWidth(l))
	}
	// One cell of margin before the divider.
	return min(max(widest+1, minLeft), max(minLeft, w-1-minRight))
}

// fitLine truncates s to w cells and pads it to exactly w.
func fitLine(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// fit cuts s into exactly h lines of exactly w cells.
func fit(s string, w, h int) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, h)
	for i := range out {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out[i] = fitLine(l, w)
	}
	return out
}
