package doctor

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
)

// GHCRCDN is the host ghcr.io redirects blob downloads to (GitHub's CDN).
// Zot pulls every ghcr.io layer from it, so a slow CDN address makes cold
// pulls of ghcr.io images slow.
const GHCRCDN = "pkg-containers.githubusercontent.com"

// SlowCDN is the download speed below which the check warns, in bytes per
// second: a 100 MiB image then takes more than about two minutes to cache.
const SlowCDN = 1 << 20

// githubCDN are addresses GitHub's CDN answers on, tried besides the ones
// DNS returns when the first is slow.
var githubCDN = []string{"185.199.108.154", "185.199.109.154", "185.199.110.154", "185.199.111.154", "185.199.108.133", "185.199.111.133"}

// GHCRProbe measures blob downloads from ghcr.io. The doctor check holds
// the logic; HostGHCRProbe does the network calls.
type GHCRProbe interface {
	// Resolve returns the addresses DNS gives for host.
	Resolve(ctx context.Context, host string) ([]string, error)
	// BlobURL returns the CDN URL ghcr.io redirects a public blob to.
	BlobURL(ctx context.Context) (string, error)
	// Speed downloads from url through addr (an IP; "" lets DNS choose)
	// for at most d and returns bytes per second.
	Speed(ctx context.Context, url, addr string, d time.Duration) (float64, error)
	// Reach opens a TCP connection to addr on port 443 within d.
	Reach(ctx context.Context, addr string, d time.Duration) error
}

// checkGHCR reports which GitHub CDN address DNS returns and how fast a
// ghcr.io blob comes from it. When it is slow it tries the other CDN
// addresses and suggests the fastest as a zot.addHosts override. A pinned
// address in lab.yaml's zot.addHosts is what lab-zot uses, so it is
// measured on its own: slow or unreachable, the check warns and suggests
// a faster address. Pins for other hosts must at least answer on 443.
func checkGHCR(ctx context.Context, sys System, opts Options) Result {
	p := sys.GHCR
	if p == nil {
		return Result{Status: Skip, Detail: "not measured"}
	}
	if r, bad := checkOtherPins(ctx, p, opts.ZotHosts); bad {
		return r
	}
	addrs, err := p.Resolve(ctx, GHCRCDN)
	if err != nil || len(addrs) == 0 {
		return warn("check the host's DNS", "%s does not resolve: %v", GHCRCDN, err)
	}
	url, err := p.BlobURL(ctx)
	if err != nil {
		return warn("check the network; Zot pulls ghcr.io images through it", "ghcr.io blob: %s", firstLine(err.Error()))
	}
	speed, dnsErr := p.Speed(ctx, url, addrs[0], 3*time.Second)
	detail := fmt.Sprintf("%s → %s, %s", GHCRCDN, strings.Join(addrs, " "), rate(speed))
	if dnsErr != nil {
		detail = fmt.Sprintf("%s → %s fails: %s", GHCRCDN, strings.Join(addrs, " "), firstLine(dnsErr.Error()))
	}
	if override := opts.ZotHosts[GHCRCDN]; override != "" {
		return checkPinnedCDN(ctx, p, url, addrs, override, speed, dnsErr, detail)
	}
	if dnsErr != nil {
		return warn("check the network; Zot pulls ghcr.io images through it", "download from %s (%s): %s", GHCRCDN, addrs[0], firstLine(dnsErr.Error()))
	}
	if speed >= SlowCDN {
		return ok("%s", detail)
	}
	best, bestSpeed := fastest(ctx, p, url, candidates(addrs), speed)
	if best == "" || bestSpeed < 2*speed {
		return warn("cold pulls of ghcr.io images will be slow; no other CDN address is faster now", "%s: slow", detail)
	}
	return warn(fmt.Sprintf("give lab-zot the faster address in lab.yaml: zot: {addHosts: {%s: %s}}, then lab up (only lab-zot uses it; /etc/hosts is not touched)", GHCRCDN, best),
		"%s: slow; %s gives %s", detail, best, rate(bestSpeed))
}

// checkPinnedCDN measures the address lab.yaml pins for the CDN, which is
// the one lab-zot uses. A slow or failing pin is a warning with a better
// choice: another CDN address, or no pin when DNS's own answer is fast.
func checkPinnedCDN(ctx context.Context, p GHCRProbe, url string, addrs []string, pin string, dnsSpeed float64, dnsErr error, detail string) Result {
	ovs, err := p.Speed(ctx, url, pin, 2*time.Second)
	if err == nil && ovs >= SlowCDN {
		return ok("%s; lab-zot uses %s (zot.addHosts), %s", detail, pin, rate(ovs))
	}
	what := fmt.Sprintf("lab-zot's pinned %s (zot.addHosts) is slow: %s", pin, rate(ovs))
	if err != nil {
		what = fmt.Sprintf("lab-zot's pinned %s (zot.addHosts) fails: %s", pin, firstLine(err.Error()))
	}
	var pool []string
	for _, a := range append(append([]string{}, addrs...), githubCDN...) {
		if a != pin && !slices.Contains(pool, a) {
			pool = append(pool, a)
		}
	}
	best, bestSpeed := fastest(ctx, p, url, pool, ovs)
	switch {
	case best == "":
		return warn("no CDN address is faster now; remove zot.addHosts from lab.yaml if the pin fails, then lab up", "%s; %s", detail, what)
	case dnsErr == nil && best == addrs[0] && dnsSpeed >= SlowCDN:
		return warn("remove zot.addHosts from lab.yaml (DNS's own answer is fast), then lab up", "%s; %s", detail, what)
	}
	return warn(fmt.Sprintf("pin the faster address instead: zot: {addHosts: {%s: %s}} in lab.yaml, then lab up", GHCRCDN, best),
		"%s; %s; %s gives %s", detail, what, best, rate(bestSpeed))
}

// checkOtherPins checks that every zot.addHosts pin besides the CDN's
// answers on port 443; bad is true when one does not.
func checkOtherPins(ctx context.Context, p GHCRProbe, pins map[string]string) (Result, bool) {
	hosts := make([]string, 0, len(pins))
	for h := range pins {
		if h != GHCRCDN {
			hosts = append(hosts, h)
		}
	}
	slices.Sort(hosts)
	for _, h := range hosts {
		if err := p.Reach(ctx, pins[h], 2*time.Second); err != nil {
			return warn(fmt.Sprintf("fix or remove %s in zot.addHosts in lab.yaml, then lab up", h),
				"lab-zot's pinned %s for %s does not answer on 443: %s", pins[h], h, firstLine(err.Error())), true
		}
	}
	return Result{}, false
}

// fastest measures each address and returns the fastest one faster than
// floor, or "".
func fastest(ctx context.Context, p GHCRProbe, url string, pool []string, floor float64) (string, float64) {
	best, bestSpeed := "", floor
	for _, a := range pool {
		if ctx.Err() != nil {
			break
		}
		if s, err := p.Speed(ctx, url, a, 1500*time.Millisecond); err == nil && s > bestSpeed {
			best, bestSpeed = a, s
		}
	}
	return best, bestSpeed
}

// candidates are the CDN addresses to try besides the first DNS answer.
func candidates(addrs []string) []string {
	var out []string
	for _, a := range append(append([]string{}, addrs[1:]...), githubCDN...) {
		if a != addrs[0] && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func rate(bps float64) string {
	return fmt.Sprintf("%.1f MiB/s", bps/(1<<20))
}

// HostGHCRProbe measures ghcr.io from this host. The blob is the largest
// layer of the pinned Zot image, so it is public and does not change.
type HostGHCRProbe struct {
	// Repo and Digest name the image whose largest layer is downloaded.
	Repo, Digest string
}

// Resolve implements GHCRProbe.
func (HostGHCRProbe) Resolve(ctx context.Context, host string) ([]string, error) {
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	var v4 []string
	for _, ip := range ips {
		if strings.Contains(ip, ".") {
			v4 = append(v4, ip)
		}
	}
	if len(v4) == 0 {
		return ips, nil
	}
	return v4, nil
}

// Reach implements GHCRProbe.
func (HostGHCRProbe) Reach(ctx context.Context, addr string, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(addr, "443"))
	if err != nil {
		return err
	}
	return c.Close()
}

// BlobURL implements GHCRProbe: an anonymous pull token, the image's
// manifest, then the redirect for its largest layer.
func (h HostGHCRProbe) BlobURL(ctx context.Context) (string, error) {
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var tok struct {
		Token string `json:"token"`
	}
	if err := getJSON(ctx, c, "https://ghcr.io/token?scope=repository:"+h.Repo+":pull", "", "", &tok); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	var m struct {
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
	}
	accept := "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
	if err := getJSON(ctx, c, "https://ghcr.io/v2/"+h.Repo+"/manifests/"+h.Digest, tok.Token, accept, &m); err != nil {
		return "", fmt.Errorf("manifest: %w", err)
	}
	if len(m.Layers) == 0 {
		return "", errors.New("manifest has no layers")
	}
	big := slices.MaxFunc(m.Layers, func(a, b struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	}) int {
		return int(a.Size - b.Size)
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ghcr.io/v2/"+h.Repo+"/blobs/"+big.Digest, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || loc == "" {
		return "", fmt.Errorf("blob: HTTP %d, want a redirect to %s", resp.StatusCode, GHCRCDN)
	}
	return loc, nil
}

func getJSON(ctx context.Context, c *http.Client, url, token, accept string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// Speed implements GHCRProbe. It times the body only, from the first byte,
// so the TLS handshake does not count.
func (HostGHCRProbe) Speed(ctx context.Context, url, addr string, d time.Duration) (float64, error) {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	tr := &http.Transport{
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if addr != "" {
				_, port, _ := net.SplitHostPort(address)
				address = net.JoinHostPort(addr, port)
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, d+3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 64<<10)
	var n int64
	start := time.Now()
	for time.Since(start) < d {
		k, err := resp.Body.Read(buf)
		n += int64(k)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if n == 0 {
				return 0, err
			}
			break
		}
	}
	el := time.Since(start).Seconds()
	if el <= 0 || n == 0 {
		return 0, errors.New("nothing downloaded")
	}
	return float64(n) / el, nil
}
