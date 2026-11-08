package state

import (
	"fmt"
	"math"
	"time"
)

// Progress survives session cleanup and raw-history trimming.
type Progress struct {
	Labs   map[string]LabSummary   `json:"labs,omitempty"`
	Drills map[string]DrillSummary `json:"drills,omitempty"`
}

// LabSummary records completed attempts, independently of the current session.
type LabSummary struct {
	Completions   int            `json:"completions"`
	LastCompleted time.Time      `json:"lastCompleted,omitzero"`
	BestActive    *time.Duration `json:"bestActiveNs,omitempty"`
}

// DrillSummary retains ended/recovered counts and positive all-time bests.
type DrillSummary struct {
	Runs          int            `json:"runs"`
	Recovered     int            `json:"recovered"`
	LastCompleted time.Time      `json:"lastCompleted,omitzero"`
	BestAlert     *time.Duration `json:"bestAlertNs,omitempty"`
	BestDetect    *time.Duration `json:"bestDetectNs,omitempty"`
	BestRecovery  *time.Duration `json:"bestRecoveryNs,omitempty"`
}

func (p *Progress) init() {
	if p.Labs == nil {
		p.Labs = map[string]LabSummary{}
	}
	if p.Drills == nil {
		p.Drills = map[string]DrillSummary{}
	}
}

func better(best **time.Duration, candidate time.Duration) {
	if candidate > 0 && (*best == nil || candidate < **best) {
		*best = &candidate
	}
}

// RecordLabCompletion belongs in the same transaction as the final step.
func (s *State) RecordLabCompletion(id string, at time.Time, elapsed time.Duration, known bool) {
	s.Progress.init()
	b := s.Progress.Labs[id]
	b.Completions++
	if at.After(b.LastCompleted) {
		b.LastCompleted = at.UTC()
	}
	if known {
		better(&b.BestActive, elapsed)
	}
	s.Progress.Labs[id] = b
}

func (p *Progress) recordDrill(r DrillRun) {
	if r.Test {
		return
	}
	p.init()
	b := p.Drills[r.Drill]
	b.Runs++
	if r.Outcome == DrillRecovered {
		b.Recovered++
		if r.Recovered.After(b.LastCompleted) {
			b.LastCompleted = r.Recovered.UTC()
		}
		better(&b.BestAlert, r.TimeTo(r.AlertFired))
		better(&b.BestDetect, r.TimeTo(r.Acked))
		better(&b.BestRecovery, r.TimeTo(r.Recovered))
	}
	p.Drills[r.Drill] = b
}

// FoldDrills is shared by migration and retained-history reporting.
func FoldDrills(history []DrillRun) Progress {
	var p Progress
	for _, r := range history {
		p.recordDrill(r)
	}
	return p
}

// DrillBest is the existing reporting shape; zero durations mean unknown.
type DrillBest struct {
	Runs, Recovered      int
	Alert, Detect, Fixed time.Duration
}

// DrillBests adapts durable summaries for the CLI and TUI.
func (p Progress) DrillBests() map[string]DrillBest {
	out := make(map[string]DrillBest, len(p.Drills))
	for id, b := range p.Drills {
		best := DrillBest{Runs: b.Runs, Recovered: b.Recovered}
		if b.BestAlert != nil {
			best.Alert = *b.BestAlert
		}
		if b.BestDetect != nil {
			best.Detect = *b.BestDetect
		}
		if b.BestRecovery != nil {
			best.Fixed = *b.BestRecovery
		}
		out[id] = best
	}
	return out
}

// FoldAttempt uses the existing TimeSpent result, without another uptime clock.
func (p *LabProgress) FoldAttempt(spent time.Duration) {
	if !p.Completed.IsZero() {
		return
	}
	if spent < p.AttemptOffset {
		p.AttemptKnown = false
	} else {
		delta := spent - p.AttemptOffset
		if delta > math.MaxInt64-p.AttemptElapsed {
			p.AttemptKnown = false
		} else {
			p.AttemptElapsed += delta
		}
	}
	p.AttemptOffset = max(0, spent)
}

func (s *State) migrate() {
	s.Progress = FoldDrills(s.Drills)
	s.Progress.init()
	for id, p := range s.Labs {
		if !p.Completed.IsZero() {
			s.RecordLabCompletion(id, p.Completed, 0, false)
		}
		p.AttemptElapsed, p.AttemptOffset, p.AttemptKnown = 0, max(0, p.Spent), false
		p.AttemptStarted = time.Time{}
		s.Labs[id] = p
	}
	s.Version = SchemaVersion
}

func (s State) validateProgress() error {
	check := func(id string, bests ...*time.Duration) error {
		for _, best := range bests {
			if best != nil && *best <= 0 {
				return fmt.Errorf("progress %s: best duration must be positive", id)
			}
		}
		return nil
	}
	for id, b := range s.Progress.Labs {
		if b.Completions < 0 {
			return fmt.Errorf("progress %s: negative completion count", id)
		}
		if err := check(id, b.BestActive); err != nil {
			return err
		}
	}
	for id, b := range s.Progress.Drills {
		if b.Runs < 0 || b.Recovered < 0 || b.Recovered > b.Runs {
			return fmt.Errorf("progress %s: invalid drill counts", id)
		}
		if err := check(id, b.BestAlert, b.BestDetect, b.BestRecovery); err != nil {
			return err
		}
	}
	for id, p := range s.Labs {
		if p.AttemptElapsed < 0 || p.AttemptOffset < 0 {
			return fmt.Errorf("progress %s: negative attempt duration", id)
		}
	}
	return nil
}

// ProgressCounts counts unique completions within the supplied catalogs.
type ProgressCounts struct {
	Labs, LabTotal, Drills, DrillTotal int
}

// Counts uses catalog IDs, not historical entries that may no longer be available.
func (p Progress) Counts(labs, drills []string) ProgressCounts {
	var c ProgressCounts
	seen := map[string]bool{}
	for _, id := range labs {
		if seen[id] {
			continue
		}
		seen[id] = true
		c.LabTotal++
		if p.Labs[id].Completions > 0 {
			c.Labs++
		}
	}
	seen = map[string]bool{}
	for _, id := range drills {
		if seen[id] {
			continue
		}
		seen[id] = true
		c.DrillTotal++
		if p.Drills[id].Recovered > 0 {
			c.Drills++
		}
	}
	return c
}

func (c ProgressCounts) String() string {
	labs, drills := "n/a", "n/a"
	if c.LabTotal > 0 {
		labs = fmt.Sprintf("%d/%d complete", c.Labs, c.LabTotal)
	}
	if c.DrillTotal > 0 {
		drills = fmt.Sprintf("%d/%d recovered", c.Drills, c.DrillTotal)
	}
	return "labs " + labs + " | drills " + drills
}
