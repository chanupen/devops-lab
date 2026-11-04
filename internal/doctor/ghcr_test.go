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
		{name: "broken override", probe: &fakeGHCR{addrs: []string{"185.199.110.154"}, speeds: map[string]float64{"185.199.110.154": 9 * mib}},
			hosts: map[string]string{GHCRCDN: "192.0.2.1"}, want: Warn, wantFix: "remove or change zot.addHosts"},
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
