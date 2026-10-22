// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/alerts"
	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
)

// An alert fires, reaches the webhook signed, shows in the API, and is
// resolved when the interface replaces the rules.
func TestAlertsEndToEnd(t *testing.T) {
	token := []byte("alert-token")
	notes := make(chan alerts.Notification, 8)
	hook := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get(alerts.HeaderSignature) != alerts.Sign(token, r.Header.Get(alerts.HeaderTimestamp), body) {
			t.Errorf("bad signature on %s", body)
		}
		var n alerts.Notification
		if err := json.Unmarshal(body, &n); err != nil {
			t.Error(err)
		}
		notes <- n
	}))
	defer hook.Close()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "webhook.token")
	if err := os.WriteFile(tokenFile, append(token, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	// The fake source reads 10 J every 50 ms: 200 W of package.
	rules := []config.AlertRule{{Name: "node_busy", Expr: alerts.NodePowerW, Op: ">", Threshold: 50}}
	sinks := config.AlertSinks{Webhook: config.AlertWebhook{Enabled: true, URL: hook.URL + "/hook", TokenFile: tokenFile}}
	cfgPath := filepath.Join(dir, "greenlens.yaml")
	yaml, err := config.SetAlertRules([]byte("alerts:\n  sinks:\n    log: false\n    webhook:\n      enabled: true\n      url: "+hook.URL+"/hook\n      token_file: "+tokenFile+"\n"), rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, yaml, 0o600); err != nil {
		t.Fatal(err)
	}
	r := startAgentWith(t, cfgPath, func(c *config.Config) {
		c.Alerts.Rules, c.Alerts.Sinks = rules, sinks
	})
	wait := func(state string) alerts.Notification {
		t.Helper()
		select {
		case n := <-notes:
			if n.State != state || n.Rule != "node_busy" || n.Expr != alerts.NodePowerW || n.Value <= 50 && state == alerts.Firing {
				t.Fatalf("notification %+v", n)
			}
			return n
		case <-time.After(15 * time.Second):
			t.Fatalf("no %s notification", state)
		}
		return alerts.Notification{}
	}
	fired := wait(alerts.Firing)
	if fired.FiredAt == nil || fired.ID == 0 {
		t.Fatalf("fired %+v", fired)
	}
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	var doc api.Alerts
	resp, body := sock.do("GET", api.PathAlerts, nil)
	if resp.code != 200 || json.Unmarshal(body, &doc) != nil {
		t.Fatalf("alerts: %d %s", resp.code, body)
	}
	if len(doc.Active) != 1 || doc.Active[0].State != alerts.Firing || len(doc.Rules) != 1 || doc.Rules[0].For != "0s" ||
		len(doc.Sinks) != 1 || doc.Sinks[0] != "webhook" || !doc.Writable || doc.EvaluatedAt == nil {
		t.Fatalf("alerts %+v", doc)
	}
	// The interface replaces the rules with none: the alert is resolved.
	resp, body = sock.do("PUT", api.PathAlertRules, api.AlertRules{Rules: []api.AlertRule{}})
	if resp.code != 200 {
		t.Fatalf("apply: %d %s", resp.code, body)
	}
	resolved := wait(alerts.Resolved)
	if resolved.ID != fired.ID || resolved.ResolvedAt == nil {
		t.Fatalf("resolved %+v", resolved)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	if err := config.Parse(data, &cfg); err != nil || len(cfg.Alerts.Rules) != 0 || !cfg.Alerts.Sinks.Webhook.Enabled {
		t.Fatalf("file %v\n%s", err, data)
	}
	resp, body = sock.do("GET", api.PathAlerts, nil)
	if resp.code != 200 || json.Unmarshal(body, &doc) != nil || len(doc.Active) != 0 || len(doc.History) != 1 ||
		doc.History[0].State != alerts.Resolved || doc.History[0].FiredAt == nil {
		t.Fatalf("after: %d %s", resp.code, body)
	}
	// A rule the agent cannot judge is refused and nothing is written.
	resp, body = sock.do("PUT", api.PathAlertRules, api.AlertRules{Rules: []api.AlertRule{{Name: "x", Expr: "gpu_temperature", Op: ">"}}})
	if resp.code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid rule: %d %s", resp.code, body)
	}
}
