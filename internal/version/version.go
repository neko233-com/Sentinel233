package version

import "fmt"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func Full() string {
	return fmt.Sprintf("sentinel233 %s (commit: %s, built: %s)", Version, Commit, Date)
}
