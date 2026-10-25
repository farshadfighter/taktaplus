// Package buildinfo holds version information injected at build time with
// -ldflags "-X github.com/farshadfighter/taktaplus/internal/buildinfo.Version=...".
package buildinfo

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns a one-line description of the build.
func String() string { return Version + " (" + Commit + ", " + Date + ")" }
