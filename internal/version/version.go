// Package version is the single source of truth for the service version.
//
// Bump Version here only; plugin.json files, the WebUI package version, and the
// README are checked against it by TestVersionsAreConsistent.
package version

const (
	// Version is the semantic version of the service and all shipped artifacts.
	Version = "0.3.0"
	// Suffix distinguishes the Go implementation from the upstream Python service.
	Suffix = "-go"
)

// String returns the full version string, e.g. "0.3.0-go".
func String() string { return Version + Suffix }

// UserAgent returns the HTTP user agent used for outbound provider requests.
func UserAgent() string { return "bili-scroll-agent/" + Version }

// UsageSyncUserAgent returns the user agent of the cc-switch usage sync client.
func UsageSyncUserAgent() string { return "bili-scroll-go-usage-sync/" + Version }
