package version

// Populated at build time via -ldflags "-X github.com/anyproto/any/internal/version.Version=..."
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func String() string {
	return "any " + Version + " (commit " + Commit + ", built " + BuildDate + ")"
}
