package version

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// withLinkerValues temporarily replaces the link-time variables and clears the
// memoised result, so each case starts from a clean slate.
func withLinkerValues(t *testing.T, v, c, d string) {
	t.Helper()
	origV, origC, origD := version, commit, buildDate
	version, commit, buildDate = v, c, d
	t.Cleanup(func() { version, commit, buildDate = origV, origC, origD })
}

func buildInfo(settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: settings}, true
	}
}

func noBuildInfo() (*debug.BuildInfo, bool) { return nil, false }

func TestLinkerValuesWinOverBuildInfo(t *testing.T) {
	withLinkerValues(t, "1.2.3", "abc1234", "2026-08-29T10:00:00Z")

	got := resolve(buildInfo(
		debug.BuildSetting{Key: "vcs.revision", Value: "ffffffff"},
		debug.BuildSetting{Key: "vcs.time", Value: "2020-01-01T00:00:00Z"},
	))

	if got.Version != "1.2.3" {
		t.Errorf("Version = %q, want the linker-supplied 1.2.3", got.Version)
	}
	if got.Commit != "abc1234" {
		t.Errorf("Commit = %q, want the linker-supplied abc1234", got.Commit)
	}
	if got.BuildDate != "2026-08-29T10:00:00Z" {
		t.Errorf("BuildDate = %q, want the linker-supplied timestamp", got.BuildDate)
	}
}

func TestFallsBackToVCSStamps(t *testing.T) {
	withLinkerValues(t, "", "", "")

	got := resolve(buildInfo(
		debug.BuildSetting{Key: "vcs.revision", Value: "deadbeef"},
		debug.BuildSetting{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
		debug.BuildSetting{Key: "vcs.modified", Value: "false"},
	))

	if got.Version != DevVersion {
		t.Errorf("Version = %q, want %q for an unstamped build", got.Version, DevVersion)
	}
	if got.Commit != "deadbeef" {
		t.Errorf("Commit = %q, want the VCS revision", got.Commit)
	}
	if got.BuildDate != "2026-01-02T03:04:05Z" {
		t.Errorf("BuildDate = %q, want the VCS timestamp", got.BuildDate)
	}
}

func TestDirtyWorkingTreeIsMarked(t *testing.T) {
	// A build from a modified tree must never be mistaken for a reproducible
	// release build when someone is reading an incident report.
	withLinkerValues(t, "", "", "")

	got := resolve(buildInfo(
		debug.BuildSetting{Key: "vcs.revision", Value: "deadbeef"},
		debug.BuildSetting{Key: "vcs.modified", Value: "true"},
	))

	if got.Commit != "deadbeef-dirty" {
		t.Fatalf("Commit = %q, want it suffixed with -dirty", got.Commit)
	}
}

func TestUnknownEverything(t *testing.T) {
	withLinkerValues(t, "", "", "")

	got := resolve(noBuildInfo)

	if got.Version != DevVersion {
		t.Errorf("Version = %q, want %q", got.Version, DevVersion)
	}
	if got.Commit != "unknown" {
		t.Errorf("Commit = %q, want \"unknown\"", got.Commit)
	}
	if got.BuildDate != "unknown" {
		t.Errorf("BuildDate = %q, want \"unknown\"", got.BuildDate)
	}
}

func TestModuleVersionIsUsedWhenNotDevel(t *testing.T) {
	withLinkerValues(t, "", "", "")

	got := resolve(func() (*debug.BuildInfo, bool) {
		bi := &debug.BuildInfo{}
		bi.Main.Version = "v0.4.0"
		return bi, true
	})

	if got.Version != "v0.4.0" {
		t.Fatalf("Version = %q, want the module version v0.4.0", got.Version)
	}
}

func TestDevelModuleVersionIsIgnored(t *testing.T) {
	withLinkerValues(t, "", "", "")

	got := resolve(func() (*debug.BuildInfo, bool) {
		bi := &debug.BuildInfo{}
		bi.Main.Version = "(devel)"
		return bi, true
	})

	if got.Version != DevVersion {
		t.Fatalf("Version = %q, want %q rather than the literal \"(devel)\"", got.Version, DevVersion)
	}
}

func TestRuntimeFieldsAreAlwaysPopulated(t *testing.T) {
	got := resolve(noBuildInfo)

	if got.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", got.GoVersion, runtime.Version())
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; got.Platform != want {
		t.Errorf("Platform = %q, want %q", got.Platform, want)
	}
}

func TestStringMentionsEveryComponent(t *testing.T) {
	got := Info{
		Version:   "1.2.3",
		Commit:    "abc1234",
		BuildDate: "2026-08-29T10:00:00Z",
		GoVersion: "go1.24.6",
		Platform:  "linux/amd64",
	}.String()

	for _, want := range []string{"1.2.3", "abc1234", "2026-08-29T10:00:00Z", "go1.24.6", "linux/amd64"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}

func TestGetIsStableAndPopulated(t *testing.T) {
	first := Get()
	if first.Version == "" || first.Commit == "" || first.BuildDate == "" {
		t.Fatalf("Get() left a field empty: %+v", first)
	}
	if second := Get(); second != first {
		t.Fatal("Get() is not stable across calls")
	}
}
