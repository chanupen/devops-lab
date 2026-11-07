package tui

import "strings"

// Key help, by screen. Every key the screen handles is listed.
var (
	helpGlobal = [][2]string{
		{"1 2 3 4 5", "Home, Stacks, Labs, Drills, Ops log"},
		{"U", "Bring the whole profile up (lab up)"},
		{":", "Command palette: up, down, status, doctor, plan"},
		{"K", "Open k9s on the lab cluster; quit it to come back"},
		{"a", "Assistant: choose a firing alert; on Drills, investigate the running drill"},
		{"?", "This help"},
		{"q  ctrl+c", "Quit"},
	}
	helpScreen = map[int][][2]string{
		screenHome: {},
		screenStacks: {
			{"j k ↑ ↓", "Move"},
			{"g G", "First, last stack"},
			{"/", "Filter (enter keeps it, esc clears it)"},
			{"u", "Bring the selected stack up (lab up --stacks <stack>)"},
			{"d", "Tear the selected stack down, after a prompt (lab down --stacks <stack>)"},
			{"r", "Retry the selected stack if it failed"},
		},
		screenLabs: {
			{"j k ↑ ↓", "Move between labs; in a step, scroll its text"},
			{"enter", "Open the current step: task, hints shown, expected result"},
			{"n p", "Next, previous step"},
			{"v", "Verify the step shown (lab labs verify N); passing it moves you on"},
			{"h", "Show the step's next hint (lab labs hint N)"},
			{"esc", "Back to the lab"},
		},
		screenDrills: {
			{"j k ↑ ↓", "Move between drills"},
			{"enter", "Start the selected drill, after a prompt (lab drills start <id>)"},
			{"R", "Start a random drill, without saying which (lab drills start --random)"},
			{"A", "Name the cause you found; the right one stops the detect timer (lab drills ack <cause>)"},
			{"v", "Verify the fix; a pass stops the recover timer and puts the lab back (lab drills verify)"},
			{"h", "Show the next hint (lab drills hint)"},
			{"x", "Abort: put back everything the drill changed, after a prompt (lab drills abort)"},
		},
		screenOps: {
			{"j k ↑ ↓", "Move"},
			{"g G", "Newest, oldest"},
			{"enter", "Open the output, with the CLI command at the top"},
		},
	}
)

// helpFor is the help overlay for a screen: its own keys, then the global
// ones, then the state symbols.
func helpFor(screen int) string {
	var b strings.Builder
	b.WriteString(bold.Render(" Keys: "+screens[screen].name) + "\n\n")
	w := 0
	all := append(append([][2]string{}, helpScreen[screen]...), helpGlobal...)
	for _, k := range all {
		w = max(w, len([]rune(k[0])))
	}
	line := func(k [2]string) {
		b.WriteString("  " + k[0] + strings.Repeat(" ", w-len([]rune(k[0]))+3) + k[1] + "\n")
	}
	for _, k := range helpScreen[screen] {
		line(k)
	}
	if len(helpScreen[screen]) > 0 {
		b.WriteString("\n")
	}
	for _, k := range helpGlobal {
		line(k)
	}
	b.WriteString("\n  Only one operation runs at a time; other action keys say busy.\n")
	b.WriteString("  A TUI opened while another lab changes things is read-only: views and\n")
	b.WriteString("  reports work, actions do not, until the other lab is done.\n")
	b.WriteString("  Every action shows its CLI command in the ops strip.\n\n")
	b.WriteString("  State: " + statusHealthy.symbol() + " healthy  " + statusProgressing.symbol() + " progressing  " +
		statusOff.symbol() + " off  " + statusFailed.symbol() + " failed\n")
	return b.String()
}
