package thresher

import (
	"runtime/debug"
	"testing"
)

func TestShortRevision(t *testing.T) {
	for _, tc := range []struct {
		name string
		bi   buildInfo
		want string
	}{
		{
			name: "long revision truncated to seven chars",
			bi:   buildInfo{revision: "f9bf034ca1c660f65605913b66eccc09276faadb"},
			want: "f9bf034",
		},
		{
			name: "dirty tree gets suffix",
			bi:   buildInfo{revision: "f9bf034ca1c660f", modified: true},
			want: "f9bf034-dirty",
		},
		{
			name: "short unknown revision left intact",
			bi:   buildInfo{revision: "unknown"},
			want: "unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.bi.shortRevision(); got != tc.want {
				t.Fatalf("shortRevision() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildInfoFromNilInfo(t *testing.T) {
	bi := buildInfoFrom("v1.2.3", nil)

	if bi.version != "v1.2.3" || bi.revision != "unknown" || bi.revTime != "unknown" {
		t.Fatalf("nil info should degrade to unknown, got %+v", bi)
	}
}

func TestBuildInfoFromSettings(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "f9bf034ca1c660f"},
			{Key: "vcs.time", Value: "2026-06-13T18:50:42Z"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "unrelated", Value: "ignored"},
		},
	}

	// An empty version falls back to the main module version; the vcs.* settings
	// populate revision/time/modified.
	bi := buildInfoFrom("", info)

	if bi.version != "v9.9.9" {
		t.Fatalf("empty version should fall back to Main.Version, got %q", bi.version)
	}

	if bi.revision != "f9bf034ca1c660f" || bi.revTime != "2026-06-13T18:50:42Z" || !bi.modified {
		t.Fatalf("vcs settings not applied: %+v", bi)
	}
}

func TestReadBuildInfoNeverEmpty(t *testing.T) {
	bi := readBuildInfo()

	if bi.version == "" || bi.revision == "" || bi.revTime == "" {
		t.Fatalf("readBuildInfo left a field empty: %+v", bi)
	}
}
