package buildinfo

import "fmt"

// Build-time metadata populated via ldflags.
var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info describes build metadata for a GatewayForge service.
type Info struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

// ForService returns immutable build metadata for the named service.
func ForService(service string) Info {
	return Info{
		Service:   service,
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}
}

// String formats build metadata concisely for startup logs.
func (i Info) String() string {
	return fmt.Sprintf("%s version=%s commit=%s built=%s", i.Service, i.Version, i.Commit, i.BuildDate)
}
