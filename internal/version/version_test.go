// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package version

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	bi := &debug.BuildInfo{
		GoVersion: "go1.27.2",
		Main:      debug.Module{Version: "v0.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "vcs.time", Value: "2026-10-19T00:00:00Z"},
		},
	}
	got := fromBuildInfo("", bi)
	if got.Version != "v0.1.0" || got.Revision != "abc123" || !got.Modified || got.CommitTime != "2026-10-19T00:00:00Z" || got.GoVersion != "go1.27.2" {
		t.Errorf("got %+v", got)
	}
	if got := fromBuildInfo("v9.9.9", bi); got.Version != "v9.9.9" {
		t.Errorf("ldflags version not preferred: %+v", got)
	}
	if got := fromBuildInfo("", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}); got.Version != Devel {
		t.Errorf("devel build reported %q", got.Version)
	}
	if got := fromBuildInfo("", nil); got.Version != Devel || got.Platform == "" {
		t.Errorf("nil build info: %+v", got)
	}
}
