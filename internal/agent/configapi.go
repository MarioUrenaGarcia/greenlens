// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
)

// restartKeys change only when the agent starts again.
// The log daemons and the token file of the webhook must be reachable
// when the agent confines its own file access at start.
var restartKeys = []string{"agent.data_dir", "agent.socket", "agent.host_root", "proxy.", "server.",
	"alerts.sinks.journald", "alerts.sinks.syslog", "alerts.sinks.webhook.token_file"}

// configWritable reports whether the agent may change its configuration
// file: it must have one, and its user must be able to write the file and
// its directory. The system service runs under ProtectSystem=strict, which
// makes /etc read-only, so it never can.
func configWritable(path string) (bool, string) {
	if path == "" {
		return false, "no_file"
	}
	if unix.Access(filepath.Dir(path), unix.W_OK|unix.X_OK) != nil || unix.Access(path, unix.W_OK) != nil {
		return false, "read_only"
	}
	return true, ""
}

func (a *Agent) configDoc() (api.ConfigDoc, error) {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	doc := api.ConfigDoc{SchemaVersion: api.SchemaVersion, Path: a.o.ConfigPath, Writable: a.configWritable, Reason: a.configReason, Restart: restartKeys}
	if a.o.ConfigPath != "" {
		data, err := os.ReadFile(a.o.ConfigPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return doc, err
		}
		doc.YAML = string(data)
	}
	values := config.Values(cfg)
	for _, f := range config.Fields() {
		if strings.Contains(f.Key, "[]") || f.Type == "list of objects" {
			continue
		}
		doc.Fields = append(doc.Fields, api.ConfigField{Key: f.Key, Type: f.Type, Value: values[f.Key], Default: f.Default, Env: f.Env, Enum: f.Enum})
	}
	return doc, nil
}

func (a *Agent) handleConfig(w http.ResponseWriter, _ *http.Request) {
	doc, err := a.configDoc()
	if err != nil {
		a.log.Error("reading the configuration failed", "error", err)
		writeError(w, http.StatusInternalServerError, "reading the configuration failed")
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// preview applies changes to the configuration file and checks the result
// as the agent would at start.
func (a *Agent) preview(r *http.Request) (api.ConfigPreview, map[string]string, error) {
	var body api.ConfigChange
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return api.ConfigPreview{}, nil, err
	}
	var current []byte
	if a.o.ConfigPath != "" {
		data, err := os.ReadFile(a.o.ConfigPath)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return api.ConfigPreview{}, nil, err
		}
		current = data
	}
	out := api.ConfigPreview{SchemaVersion: api.SchemaVersion, Errors: []api.ConfigError{}}
	edited, err := config.Edit(current, body.Changes)
	if err != nil {
		for _, e := range strings.Split(err.Error(), "\n") {
			key, msg, _ := strings.Cut(e, ": ")
			out.Errors = append(out.Errors, api.ConfigError{Key: key, Message: msg})
		}
		return out, body.Changes, nil
	}
	out.YAML = string(edited)
	out.Errors = append(out.Errors, checkConfig(edited)...)
	out.Valid = len(out.Errors) == 0
	for k := range body.Changes {
		if slices.ContainsFunc(restartKeys, func(p string) bool { return k == p || strings.HasSuffix(p, ".") && strings.HasPrefix(k, p) }) {
			out.NeedsRestart = true
		}
	}
	return out, body.Changes, nil
}

func (a *Agent) handleConfigPreview(w http.ResponseWriter, r *http.Request) {
	out, _, err := a.preview(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with the changes: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleConfigApply writes the edited file next to the old one and renames
// it over it, so a crash never leaves half a file, then reloads.
func (a *Agent) handleConfigApply(w http.ResponseWriter, r *http.Request) {
	if !a.configWritable {
		writeError(w, http.StatusConflict, "the agent cannot write its configuration file; install the previewed file instead")
		return
	}
	out, changes, err := a.preview(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "the body must be JSON with the changes: "+err.Error())
		return
	}
	if !out.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, out)
		return
	}
	if err := a.writeConfig(out.YAML); err != nil {
		a.log.Error("writing the configuration failed", "error", err)
		writeError(w, http.StatusInternalServerError, "writing the configuration failed")
		return
	}
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if err := a.st.Audit(r.Context(), actor(r), "config", strings.Join(keys, ", ")); err != nil {
		a.log.Warn("audit log write failed", "error", err)
	}
	select {
	case a.selfReload <- struct{}{}:
	default:
	}
	out.Applied = true
	writeJSON(w, http.StatusOK, out)
}

// writeConfig writes a new configuration file next to the old one, with
// its permissions, and renames it over it, so a crash never leaves half a
// file.
func (a *Agent) writeConfig(content string) error {
	path := a.o.ConfigPath
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".greenlens-*.yaml")
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(content)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// checkConfig parses a configuration file and validates it as the agent
// would at start, and returns its problems by key.
func checkConfig(data []byte) []api.ConfigError {
	cfg := config.Defaults()
	if err := config.Parse(data, &cfg); err != nil {
		return []api.ConfigError{{Message: err.Error()}}
	}
	var out []api.ConfigError
	for _, e := range help.Flatten(cfg.Validate()) {
		he := help.As(e)
		ce := api.ConfigError{Message: e.Error()}
		for i := 0; i+1 < len(he.Args); i += 2 {
			switch he.Args[i] {
			case "key":
				ce.Key = fmt.Sprint(he.Args[i+1])
			case "detail":
				ce.Message = fmt.Sprint(he.Args[i+1])
			}
		}
		out = append(out, ce)
	}
	return out
}
