// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package version reports the build identity of the running binary.
package version

import (
	"runtime"
	"runtime/debug"
)

// Version is set for release builds with
// -ldflags "-X github.com/MarioUrenaGarcia/greenlens/internal/version.Version=v1.2.3".
// When empty, the module version recorded by the Go toolchain is used.
var Version = ""

// Devel is reported when the binary carries no version information, as with
// a local build of a checkout without commits.
const Devel = "devel"

// Info describes the running binary.
type Info struct {
	Version    string
	Revision   string
	Modified   bool
	CommitTime string
	GoVersion  string
	Platform   string
}

// Get returns the build information of the running binary.
func Get() Info {
	bi, _ := debug.ReadBuildInfo()
	return fromBuildInfo(Version, bi)
}

func fromBuildInfo(ldVersion string, bi *debug.BuildInfo) Info {
	info := Info{
		Version:   ldVersion,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi != nil {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		if bi.GoVersion != "" {
			info.GoVersion = bi.GoVersion
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Revision = s.Value
			case "vcs.modified":
				info.Modified = s.Value == "true"
			case "vcs.time":
				info.CommitTime = s.Value
			}
		}
	}
	if info.Version == "" {
		info.Version = Devel
	}
	return info
}
