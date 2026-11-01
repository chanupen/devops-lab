// Package starttime records the time as early in package initialisation as
// a Go program can, so the first-frame trace covers package init.
//
// Go initialises packages whose imports are ready in import-path order (Go
// spec, "Package initialization", since 1.21). This package imports only
// time, so its init runs once time and its standard-library imports are
// ready, before any github.com/charmbracelet, golang.org, k8s.io, helm.sh or
// sigs.k8s.io package. What it misses is exec, Go runtime start and the few
// standard-library and github.com/[a-c]… packages initialised before it;
// GODEBUG=inittrace=1 shows how long those take.
package starttime

import "time"

var at = time.Now()

// At returns when this package was initialised.
func At() time.Time { return at }
