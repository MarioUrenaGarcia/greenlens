// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package paths decides where the agent keeps its database and socket. The
// systemd unit provides STATE_DIRECTORY and RUNTIME_DIRECTORY for the
// system service; a user without installation gets the XDG directories.
package paths

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens/internal/config"
)

// SystemSocket is where the system service listens.
const SystemSocket = "/run/greenlens/greenlens.sock"

// DatabaseName is the file name of the database inside the data directory.
const DatabaseName = "greenlens.db"

// ErrNoRuntimeDir is returned when there is nowhere to put the socket.
var ErrNoRuntimeDir = errors.New("no runtime directory")

// DataDir returns the directory of the database.
func DataDir(cfg config.Config, getenv func(string) string) (string, error) {
	if cfg.Agent.DataDir != "" {
		return cfg.Agent.DataDir, nil
	}
	if d := first(getenv("STATE_DIRECTORY")); d != "" {
		return d, nil
	}
	if x := getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "greenlens"), nil
	}
	if h := getenv("HOME"); h != "" {
		return filepath.Join(h, ".local", "share", "greenlens"), nil
	}
	return "", errors.New("no data directory: set agent.data_dir")
}

// Database returns the path of the database file.
func Database(cfg config.Config, getenv func(string) string) (string, error) {
	d, err := DataDir(cfg, getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, DatabaseName), nil
}

// AgentSocket returns where the agent listens.
func AgentSocket(cfg config.Config, getenv func(string) string) (string, error) {
	if cfg.Agent.Socket != "" {
		return cfg.Agent.Socket, nil
	}
	if d := first(getenv("RUNTIME_DIRECTORY")); d != "" {
		return filepath.Join(d, "greenlens.sock"), nil
	}
	if x := getenv("XDG_RUNTIME_DIR"); x != "" {
		return filepath.Join(x, "greenlens", "greenlens.sock"), nil
	}
	return "", ErrNoRuntimeDir
}

// ClientSockets lists where a command looks for the agent: the configured
// socket, then the agent of the user, then the system service.
func ClientSockets(cfg config.Config, getenv func(string) string) []string {
	var out []string
	if cfg.Agent.Socket != "" {
		return []string{cfg.Agent.Socket}
	}
	if x := getenv("XDG_RUNTIME_DIR"); x != "" {
		out = append(out, filepath.Join(x, "greenlens", "greenlens.sock"))
	}
	return append(out, SystemSocket)
}

// first returns the first entry of a colon-separated list, as systemd sets
// these variables when a unit declares several directories.
func first(list string) string {
	s, _, _ := strings.Cut(list, ":")
	return s
}
