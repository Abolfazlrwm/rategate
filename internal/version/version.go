// Package version holds build-time metadata injected via -ldflags.
//
// The values are populated by the Makefile (see the `LDFLAGS` variable)
// and by the release workflow (.github/workflows/release.yml) via goreleaser.
package version

// Version is the semantic version of the binary. Defaults to "dev" when
// the binary is built without -ldflags injection.
var Version = "dev"

// Commit is the short Git SHA the binary was built from.
var Commit = "unknown"

// BuildDate is the UTC build timestamp in RFC3339.
var BuildDate = "unknown"

// String returns a human-readable version banner.
//
// Example: "rategate v0.1.0 (commit abc1234, built 2025-11-12T09:30:00Z)".
func String() string {
	return "rategate " + Version + " (commit " + Commit + ", built " + BuildDate + ")"
}
