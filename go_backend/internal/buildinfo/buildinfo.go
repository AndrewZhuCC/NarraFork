// Package buildinfo holds build-time metadata injected via -ldflags -X.
package buildinfo

// These variables are set at build time via ldflags, e.g.:
//
//	-X github.com/narrafork/narrafork/internal/buildinfo.Version=1.2.3
//
// They default to "dev"/"none" for local (go run / go build) builds.
var (
	// Version is the release version.
	Version = "dev"
	// Commit is the short git commit hash.
	Commit = "none"
	// Channel is the release channel (e.g. "stable", "beta").
	Channel = "dev"
)
