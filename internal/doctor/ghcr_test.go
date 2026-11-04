package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeGHCR struct {
	addrs   []string
	urlErr  error
	speeds  map[string]float64 // by address; missing fails
	queried []string
}

func (f *fakeGHCR) Resolve(context.Context, string) ([]string, error) { return f.addrs, nil }
func (f *fakeGHCR) BlobURL(context.Context) (string, error) {
	return "https://" + GHCRCDN + "/blob", f.urlErr
}
func (f *fakeGHCR) Speed(_ context.Context, _, addr string, _ time.Duration) (float64, error) {
	f.queried = append(f.queried, addr)
	if s, ok := f.speeds[addr]; ok {
		return s, nil
	}
	return 0, errors.New("connection refused")
}

func (f *fakeGHCR) Reach(_ context.Context, addr string, _ time.Duration) error {
	if _, ok := f.speeds[addr]; ok {
		return nil
	}
	return errors.New("i/o timeout")
}

func TestCheckGHCR(t *testing.T) {
	const mib = 1 << 20
	tests := []struct {
		name       string
		probe      *fakeGHCR
		hosts      map[string]string
		want       Status
		wantDetail string
		wantFix    string
	}{
		{name: "fast", probe: &fakeGHCR{addrs: []string{"185.199.110.154"}, speeds: map[string]float64{"185.199.110.154": 10 * mib}},
			want: OK, wantDetail: "185.199.110.154, 10.0 MiB/s"},
		{name: "slow, a faster address is suggested for lab-zot", probe: &fakeGHCR{addrs: []string{"10.1.1.1"},
			speeds: map[string]float64{"10.1.1.1": 0.2 * mib, "185.199.109.154": 8 * mib, "185.199.110.154": 3 * mib}},
			want: Warn, wantDetail: "slow; 185.199.109.154 gives 8.0 MiB/s", wantFix: "zot: {addHosts: {" + GHCRCDN + ": 185.199.109.154}}"},
		{name: "slow everywhere", probe: &fakeGHCR{addrs: []string{"10.1.1.1"}, speeds: map[string]float64{"10.1.1.1": 0.2 * mib}},
			want: Warn, wantFix: "no other CDN address is faster"},
		{name: "override is measured", probe: &fakeGHCR{addrs: []string{"10.1.1.1"},
			speeds: map[string]float64{"10.1.1.1": 0.2 * mib, "185.199.109.154": 8 * mib}},
			hosts: map[string]string{GHCRCDN: "185.199.109.154"}, want: OK, wantDetail: "lab-zot uses 185.199.109.154 (zot.addHosts), 8.0 MiB/s"},
		{name: "broken override, DNS is fast: drop the pin", probe: &fakeGHCR{addrs: []string{"185.199.110.154"}, speeds: map[string]float64{"185.199.110.154": 9 * mib}},
			hosts: map[string]string{GHCRCDN: "192.0.2.1"}, want: Warn, wantDetail: "pinned 192.0.2.1 (zot.addHosts) fails", wantFix: "remove zot.addHosts"},
		{name: "slow override: a faster pin is suggested", probe: &fakeGHCR{addrs: []string{"10.1.1.1"},
			speeds: map[string]float64{"10.1.1.1": 0.1 * mib, "185.199.109.154": 0.3 * mib, "185.199.111.154": 6 * mib}},
			hosts: map[string]string{GHCRCDN: "185.199.109.154"}, want: Warn, wantDetail: "pinned 185.199.109.154 (zot.addHosts) is slow: 0.3 MiB/s; 185.199.111.154 gives 6.0 MiB/s",
			wantFix: "zot: {addHosts: {" + GHCRCDN + ": 185.199.111.154}}"},
		{name: "override works while DNS's address fails", probe: &fakeGHCR{addrs: []string{"10.1.1.1"},
			speeds: map[string]float64{"185.199.109.154": 8 * mib}},
			hosts: map[string]string{GHCRCDN: "185.199.109.154"}, want: OK, wantDetail: "lab-zot uses 185.199.109.154"},
		{name: "another pinned host does not answer", probe: &fakeGHCR{addrs: []string{"185.199.110.154"}, speeds: map[string]float64{"185.199.110.154": 9 * mib}},
			hosts: map[string]string{"ghcr.io": "192.0.2.7"}, want: Warn, wantDetail: "pinned 192.0.2.7 for ghcr.io does not answer", wantFix: "ghcr.io in zot.addHosts"},
		{name: "ghcr.io unreachable", probe: &fakeGHCR{addrs: []string{"185.199.110.154"}, urlErr: errors.New("dial tcp: timeout")},
			want: Warn, wantDetail: "ghcr.io blob: dial tcp: timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := checkGHCR(context.Background(), System{GHCR: tt.probe}, Options{ZotHosts: tt.hosts})
			if r.Status != tt.want || !strings.Contains(r.Detail, tt.wantDetail) || !strings.Contains(r.Fix, tt.wantFix) {
				t.Errorf("got %v %q (fix %q); want %v with %q, fix %q", r.Status, r.Detail, r.Fix, tt.want, tt.wantDetail, tt.wantFix)
			}
		})
	}
	if r := checkGHCR(context.Background(), System{}, Options{}); r.Status != Skip {
		t.Errorf("no probe: %v, want skip", r.Status)
	}
}

func TestCheckDrill(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		d     *DrillState
		want  Status
		match string
	}{
		{"none", nil, OK, "none running"},
		{"running", &DrillState{Label: "drill dns", Phase: "running", Deadline: now.Add(time.Minute), Now: now}, Warn, "drill dns is running"},
		{"past its limit", &DrillState{Label: "a random drill", Phase: "running", Deadline: now.Add(-time.Minute), Now: now}, Fail, "past its time limit"},
		{"interrupted inject", &DrillState{Label: "drill oom", Phase: "injecting", Deadline: now.Add(time.Hour), Now: now}, Fail, "only partly"},
		{"failed restore", &DrillState{Label: "drill oom", Phase: "restoring", Error: "spec differ", Deadline: now.Add(time.Hour), Now: now}, Fail, "spec differ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := checkDrill(context.Background(), System{}, Options{Drill: tt.d})
			if r.Status != tt.want || !strings.Contains(r.Detail, tt.match) {
				t.Errorf("got %v %q, want %v with %q", r.Status, r.Detail, tt.want, tt.match)
			}
			if r.Status == Fail && !strings.Contains(r.Fix, "lab drills abort") {
				t.Errorf("fix = %q", r.Fix)
			}
		})
	}
}
