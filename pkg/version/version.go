package version

// Version holds the current semantic version of tracker-proxy.
// It can be overridden at build time via -ldflags="-X 'github.com/cineclaw/tracker-proxy/pkg/version.Version=v1.0.0'"
var (
	Version   = "1.3.0"
	BuildTime = ""
	Commit    = ""
)
