// Package tui is the lab's terminal UI. The first frame renders from
// ~/.lab/state.json alone; live state then arrives from client-go informers
// as messages, so the key loop never waits on the cluster.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/chanupen/devops-lab/internal/config"
	"github.com/chanupen/devops-lab/internal/engine"
	"github.com/chanupen/devops-lab/internal/kube"
	"github.com/chanupen/devops-lab/internal/state"
)

// Options configure the TUI.
type Options struct {
	Config     config.Config
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
}

// Screens, in tab order.
const (
	screenHome = iota
	screenStacks
)

var screenNames = []string{"Home", "Stacks"}

// data is what every screen renders from.
type data struct {
	cfg    config.Config
	st     state.State
	live   cluster
	ctx    string // kube context name
	order  []string
	deps   map[string][]string
	appURL func(string) string
	hint   string
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
	help   bool
	flash  string // shown in the ops strip until the next key
	width  int
	height int

	events   chan tea.Msg
	gen      int // connect generation; reports from older watchers are dropped
	cancel   context.CancelFunc
	kubeMod  time.Time
	kubeSeen bool
	stateMod time.Time
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
	d := data{cfg: opts.Config, st: opts.State, appURL: opts.AppURL, hint: opts.AccessHint, deps: map[string][]string{}}
	for _, s := range opts.Catalog {
		d.order = append(d.order, s.Name)
		d.deps[s.Name] = s.DependsOn
	}
	if g, err := engine.NewGraph(opts.Catalog); err == nil {
		if order, err := g.Order(g.Names()...); err == nil {
			d.order = order
		}
	}
	return Model{opts: opts, d: d, stacks: newStacksModel(), width: 100, height: 30,
		events: make(chan tea.Msg, 1), gen: 1}
}

// Run shows the TUI until the user quits or ctx ends.
func Run(ctx context.Context, opts Options) error {
	final, err := tea.NewProgram(New(opts), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
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
	tickMsg struct{}
)

// Init starts the cluster connection, the watcher listener and the file check.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.connect(m.gen), m.listen(), m.tick())
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
		if msg.gen == m.gen {
			m.d.live = msg.c
		}
		return m, m.listen()
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
		return m, m.checkFiles()
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
	if msg.String() == "ctrl+c" {
		m.stop()
		return m, tea.Quit
	}
	if m.screen == screenStacks && m.stacks.filtering {
		var cmd tea.Cmd
		m.stacks, cmd = m.stacks.update(msg, &m.d)
		return m, cmd
	}
	if m.help {
		if s := msg.String(); s == "q" {
			m.stop()
			return m, tea.Quit
		}
		m.help = false
		return m, nil
	}
	switch msg.String() {
	case "q":
		m.stop()
		return m, tea.Quit
	case "?":
		m.help = true
	case "1":
		m.screen = screenHome
	case "2":
		m.screen = screenStacks
	case "U":
		m.flash = "U runs lab up from the TUI in a later release; for now run: lab up"
	default:
		if m.screen == screenStacks {
			var cmd tea.Cmd
			m.stacks, cmd = m.stacks.update(msg, &m.d)
			return m, cmd
		}
	}
	return m, nil
}

// View renders the four zones: header, tabs, the list and detail split, and
// the ops strip with the key hints under it.
func (m Model) View() string {
	if m.opts.FirstView != nil {
		defer m.opts.FirstView()
	}
	w, h := max(m.width, 40), max(m.height, 10)
	bodyH := h - 6
	leftW := max(30, w*2/5)
	rightW := w - leftW - 1

	var b strings.Builder
	b.WriteString(fitLine(m.header(), w) + "\n")
	b.WriteString(fitLine(m.tabs(), w) + "\n")
	if m.help {
		b.WriteString(strings.Repeat("─", w) + "\n")
		for _, l := range fit(helpText, w, bodyH) {
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
	return fmt.Sprintf("%s  mode: %s  ctx: %s  health: %s  rev: %s  ttl: —",
		lipgloss.NewStyle().Bold(true).Reverse(true).Render(" lab "), d.mode(), ctx, health(d), revision(d.st))
}

// health summarises the cluster in a few characters.
func health(d *data) string {
	c := d.live
	switch {
	case !c.known && len(d.st.Stacks) == 0:
		return statusOff.symbol() + " off"
	case !c.known:
		return statusProgressing.symbol() + " connecting"
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

// revision is the GitOps revision, highlighted when it is not main.
func revision(st state.State) string {
	switch {
	case !st.IsApplied(engine.RevisionStack):
		return "—"
	case st.Revision == engine.DefaultRevision:
		return st.Revision
	case st.Revision == "":
		return warn.Render("unrecorded")
	default:
		return warn.Render(st.Revision)
	}
}

func (m Model) tabs() string {
	var parts []string
	for i, name := range screenNames {
		t := fmt.Sprintf(" %d %s ", i+1, name)
		if i == m.screen {
			t = lipgloss.NewStyle().Reverse(true).Render(t)
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " ")
}

// opsStrip shows a message for the last key, else the latest operation.
func (m Model) opsStrip() string {
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
	case m.help:
		return "any key closes help  q quit"
	case m.screen == screenStacks && m.stacks.filtering:
		return "type to filter  enter keep  esc clear"
	case m.screen == screenStacks:
		return "j/k move  / filter  1 home  2 stacks  U up  ? help  q quit"
	default:
		return "1 home  2 stacks  U up  ? help  q quit"
	}
}

const helpText = `Keys

  1 / 2        Home / Stacks
  j k ↑ ↓      Move in a list
  g G          First / last item
  /            Filter the list (enter keeps it, esc clears it)
  U            Bring the lab up (later release; run lab up for now)
  ?            This help
  q  ctrl+c    Quit

State: ● healthy  ◐ progressing  ○ off  ✕ failed`

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
