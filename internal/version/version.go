// Package version exposes the build identity of a Headnet binary.
//
// Values are injected at link time by the build system:
//
//	go build -ldflags "-X github.com/headnet/headnet/internal/version.version=0.1.0 ..."
//
// When they are absent — a plain `go build`, `go run`, or `go test` — the
// package falls back to the module information the Go toolchain embeds, so a
// developer build still reports a truthful revision instead of "unknown".
package version

import (
	"runtime"
	"runtime/debug"
	"sync"
)

// Injected at link time. They are deliberately unexported: everything outside
// this package reads them through Get, which applies the fallbacks.
var (
	version   = ""
	commit    = ""
	buildDate = ""
)

// DevVersion is reported when no version was stamped into the binary. The
// "-dev" suffix makes it obvious in a bug report that the build did not come
// from the release pipeline.
const DevVersion = "0.0.0-dev"

// Info describes one build.
type Info struct {
	// Version is the semantic version of the release, or DevVersion.
	Version string `json:"version"`
	// Commit is the git revision, suffixed with "-dirty" when the working
	// tree had uncommitted changes at build time.
	Commit string `json:"commit"`
	// BuildDate is an RFC 3339 timestamp, or "unknown" for a local build.
	BuildDate string `json:"build_date"`
	// GoVersion is the toolchain that produced the binary.
	GoVersion string `json:"go_version"`
	// Platform is the target, e.g. "linux/amd64".
	Platform string `json:"platform"`
}

// String renders the build identity on one line, which is what `--version`
// prints and what belongs in a bug report.
func (i Info) String() string {
	return i.Version + " (commit " + i.Commit + ", built " + i.BuildDate +
		", " + i.GoVersion + " " + i.Platform + ")"
}

var (
	once   sync.Once
	cached Info
)

// Get returns the build identity. The result is computed once and reused;
// reading build info is not free and this is called on every /health request.
func Get() Info {
	once.Do(func() { cached = resolve(debug.ReadBuildInfo) })
	return cached
}

// resolve builds the Info, taking link-time values first and falling back to
// the toolchain's embedded VCS stamps. It takes the reader as a parameter so
// the fallback logic is testable without producing real builds.
func resolve(readBuildInfo func() (*debug.BuildInfo, bool)) Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}

	var vcsRevision, vcsTime string
	var vcsDirty bool
	if bi, ok := readBuildInfo(); ok && bi != nil {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				vcsRevision = setting.Value
			case "vcs.time":
				vcsTime = setting.Value
			case "vcs.modified":
				vcsDirty = setting.Value == "true"
			}
		}
	}

	if info.Version == "" {
		info.Version = DevVersion
	}
	if info.Commit == "" {
		info.Commit = vcsRevision
		if info.Commit != "" && vcsDirty {
			info.Commit += "-dirty"
		}
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.BuildDate == "" {
		info.BuildDate = vcsTime
	}
	if info.BuildDate == "" {
		info.BuildDate = "unknown"
	}
	return info
}
