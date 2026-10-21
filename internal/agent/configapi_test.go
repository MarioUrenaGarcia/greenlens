// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
)

func TestConfigAPI(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "greenlens.yaml")
	text := "# The agent of this test.\nindicators:\n  # Facility overhead.\n  pue: 1.0\n"
	if err := os.WriteFile(cfgPath, []byte(text), 0o640); err != nil { //nolint:gosec // the test checks that the mode of the file is kept
		t.Fatal(err)
	}
	r := startAgent(t, cfgPath)
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	resp, body := sock.do("GET", api.PathConfig, nil)
	var doc api.ConfigDoc
	if resp.code != 200 || json.Unmarshal(body, &doc) != nil || !doc.Writable || doc.Path != cfgPath || !strings.Contains(doc.YAML, "pue: 1.0") {
		t.Fatalf("config: %d %s", resp.code, body)
	}
	pue := ""
	for _, f := range doc.Fields {
		if f.Key == "indicators.pue" {
			pue = f.Value
		}
		if strings.HasPrefix(f.Key, "backends") {
			t.Errorf("a list of objects is offered for editing: %s", f.Key)
		}
	}
	if pue != "1" {
		t.Fatalf("pue in force %q", pue)
	}

	resp, body = sock.do("PUT", api.PathConfig, api.ConfigChange{Changes: map[string]string{"indicators.pue": "0.5"}})
	if resp.code != http.StatusUnprocessableEntity || !strings.Contains(string(body), `"key":"indicators"`) {
		t.Fatalf("invalid change: %d %s", resp.code, body)
	}
	if data, _ := os.ReadFile(cfgPath); string(data) != text {
		t.Fatalf("an invalid change was written:\n%s", data)
	}
	resp, body = sock.do("PUT", api.PathConfig, api.ConfigChange{Changes: map[string]string{"indicators.pue": "1.25", "server.enabled": "false"}})
	var out api.ConfigPreview
	if resp.code != 200 || json.Unmarshal(body, &out) != nil || !out.Applied || !out.NeedsRestart {
		t.Fatalf("apply: %d %s", resp.code, body)
	}
	data, _ := os.ReadFile(cfgPath)
	st, _ := os.Stat(cfgPath)
	if !strings.Contains(string(data), "# Facility overhead.") || !strings.Contains(string(data), "pue: 1.25") || st.Mode().Perm() != 0o640 {
		t.Fatalf("written %v:\n%s", st.Mode(), data)
	}
	// The agent reloads the file by itself.
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := r.client.Status(t.Context())
		if err == nil && st.Factors.PUE == 1.25 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not reloaded: %+v %v", st.Factors, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp, body = sock.do("GET", api.PathAudit, nil)
	if resp.code != 200 || !strings.Contains(string(body), `"action":"config"`) {
		t.Fatalf("audit: %s", body)
	}
}

func TestConfigOfTheSystemServiceIsReadOnly(t *testing.T) {
	r := startAgent(t, "")
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	resp, body := sock.do("GET", api.PathConfig, nil)
	if resp.code != 200 || !strings.Contains(string(body), `"writable":false`) || !strings.Contains(string(body), `"reason":"no_file"`) {
		t.Fatalf("config: %d %s", resp.code, body)
	}
	if resp, _ := sock.do("PUT", api.PathConfig, api.ConfigChange{Changes: map[string]string{"indicators.pue": "1.2"}}); resp.code != http.StatusConflict {
		t.Fatalf("apply without a file: %d", resp.code)
	}
	if ok, reason := configWritable("/etc/greenlens/greenlens.yaml"); ok && os.Geteuid() != 0 {
		t.Fatalf("a file under /etc is writable: %s", reason)
	}
}
