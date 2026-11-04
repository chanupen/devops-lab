package main

import (
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// certFiles serves the key pair in a directory and loads it again when
// tls.crt changes: cert-manager renews the Secret, the kubelet updates the
// volume, and the next handshake gets the new certificate without a restart.
type certFiles struct {
	dir string

	mu      sync.Mutex
	modTime time.Time
	cert    *tls.Certificate
}

func newCertFiles(dir string) *certFiles { return &certFiles{dir: dir} }

func (c *certFiles) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	crt, key := filepath.Join(c.dir, "tls.crt"), filepath.Join(c.dir, "tls.key")
	fi, err := os.Stat(crt)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cert != nil && fi.ModTime().Equal(c.modTime) {
		return c.cert, nil
	}
	pair, err := tls.LoadX509KeyPair(crt, key)
	if err != nil {
		if c.cert != nil {
			return c.cert, nil // half-updated files: keep serving the last good pair
		}
		return nil, err
	}
	if len(pair.Certificate) == 0 {
		return nil, errors.New("no certificate in " + crt)
	}
	c.cert, c.modTime = &pair, fi.ModTime()
	return c.cert, nil
}
