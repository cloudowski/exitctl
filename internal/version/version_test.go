package version

import (
	"runtime/debug"
	"testing"
)

func TestModuleVersion(t *testing.T) {
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"nil build info", nil, ""},
		{"empty version", &debug.BuildInfo{}, ""},
		{"source checkout", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, ""},
		{"go install of a tag", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}}, "0.1.0"},
		{"pre-release tag", &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0-rc.1"}}, "0.2.0-rc.1"},
		{"pseudo-version", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.1-0.20261020101010-abcdef123456"}}, "0.1.1-0.20261020101010-abcdef123456"},
		{"dirty VCS build", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0+dirty"}}, "0.1.0+dirty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := moduleVersion(c.info); got != c.want {
				t.Errorf("moduleVersion = %q, want %q", got, c.want)
			}
		})
	}
}

// A test binary has no module version, so the development version stays in place.
func TestToolKeepsDevVersionWithoutModuleVersion(t *testing.T) {
	if Tool != devVersion {
		t.Errorf("Tool = %q, want %q in a source build", Tool, devVersion)
	}
}
