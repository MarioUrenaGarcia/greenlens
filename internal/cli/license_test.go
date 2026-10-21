// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/version"
)

func TestLicense(t *testing.T) {
	code, out, _ := run(t, nil, "license")
	if code != 0 || !strings.Contains(out, "GNU Affero General Public License, version 3 only") ||
		!strings.Contains(out, "Source code of this version: https://github.com/MarioUrenaGarcia/greenlens") ||
		!strings.Contains(out, "modernc.org/sqlite") || !strings.Contains(out, "section 13") {
		t.Fatalf("license: %d\n%s", code, out)
	}
	code, out, _ = run(t, map[string]string{"GREENLENS_LANG": "es"}, "license")
	if code != 0 || !strings.Contains(out, "Código fuente de esta versión") || !strings.Contains(out, "de terceros (") {
		t.Fatalf("license es: %d\n%s", code, out)
	}
	code, out, _ = run(t, nil, "license", "--full")
	if code != 0 || !strings.Contains(out, "GNU AFFERO GENERAL PUBLIC LICENSE") || !strings.Contains(out, "13. Remote Network Interaction") {
		t.Fatalf("license --full: %d", code)
	}
	code, out, _ = run(t, nil, "license", "--third-party")
	if code != 0 || !strings.HasPrefix(out, "# Third-party notices") {
		t.Fatalf("license --third-party: %d", code)
	}
	code, out, _ = run(t, nil, "license", "-o", "json")
	var rep LicenseReport
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || rep.License != "AGPL-3.0-only" || len(rep.ThirdParty) < 10 {
		t.Fatalf("license json: %d %s", code, out)
	}
	for _, c := range rep.ThirdParty {
		if c.Name == "" || c.Version == "" || strings.Contains(c.Version, " ") {
			t.Errorf("component %+v", c)
		}
	}
}

func TestSourceURL(t *testing.T) {
	const repo = "https://github.com/MarioUrenaGarcia/greenlens"
	for _, tt := range []struct {
		info version.Info
		want string
	}{
		{version.Info{Version: "v1.2.3", Revision: "0123456789abcdef0123456789abcdef01234567"}, repo + "/tree/v1.2.3"},
		{version.Info{Version: "v1.3.0-rc.1"}, repo + "/tree/v1.3.0-rc.1"},
		{version.Info{Version: "v0.0.0-20261020194759-6763d05a1b2c+dirty", Revision: "6763d05a1b2c3d4e5f60718293a4b5c6d7e8f901"}, repo + "/tree/6763d05a1b2c3d4e5f60718293a4b5c6d7e8f901"},
		{version.Info{Version: "v0.0.0-20261020194759-6763d05a1b2c"}, repo + "/tree/6763d05a1b2c"},
		{version.Info{Version: "devel", Revision: "abc123"}, repo + "/tree/abc123"},
		{version.Info{Version: "devel"}, repo},
	} {
		if got := sourceURL(tt.info); got != tt.want {
			t.Errorf("%+v: %s, want %s", tt.info, got, tt.want)
		}
	}
	if !licenseReport(version.Info{Version: "v0.0.0-20261020194759-6763d05a1b2c+dirty"}, "").Modified {
		t.Error("a dirty build is not reported as modified")
	}
}
