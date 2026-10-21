// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestHelpTopics(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		env := map[string]string{"GREENLENS_LANG": lang}
		code, out, errOut := runWith(t, hooks{}, env, "help")
		if code != 0 || !strings.Contains(out, "privacy") || !strings.Contains(out, "attribution") {
			t.Fatalf("%s: help lists no topics: %d\n%s%s", lang, code, out, errOut)
		}
		for _, topic := range Topics {
			code, out, errOut := runWith(t, hooks{}, env, "help", topic)
			if code != 0 || strings.Count(out, "\n") < 6 {
				t.Errorf("%s %s: %d\n%s%s", lang, topic, code, out, errOut)
			}
			for i, l := range strings.Split(out, "\n") {
				if len([]rune(l)) > helpWidth {
					t.Errorf("%s %s: line %d is %d wide", lang, topic, i, len([]rune(l)))
				}
			}
		}
	}
}

func TestExplain(t *testing.T) {
	code, out, _ := runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": "en"}, "explain")
	for _, m := range Metrics {
		if !strings.Contains(out, m) {
			t.Errorf("explain does not list %s", m)
		}
	}
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, lang := range []string{"en", "es"} {
		for _, m := range Metrics {
			code, out, errOut := runWith(t, hooks{}, map[string]string{"GREENLENS_LANG": lang}, "explain", m)
			if code != 0 || strings.Count(out, "\n") < 8 {
				t.Errorf("%s %s: %d\n%s%s", lang, m, code, out, errOut)
			}
		}
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "explain", "watts_per_dollar"); code != 2 || !strings.Contains(errOut, "GL0017") {
		t.Errorf("unknown value: %d %s", code, errOut)
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "explain", "a", "b"); code != 2 || !strings.Contains(errOut, "GL0015") {
		t.Errorf("two values: %d %s", code, errOut)
	}
}

func TestCompletion(t *testing.T) {
	for shell, want := range map[string]string{"bash": "__start_greenlens", "zsh": "#compdef greenlens", "fish": "complete -c greenlens"} {
		code, out, errOut := runWith(t, hooks{}, nil, "completion", shell)
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("%s: %d %q\n%s", shell, code, want, errOut)
		}
	}
	if code, _, errOut := runWith(t, hooks{}, nil, "completion", "tcsh"); code != 2 || !strings.Contains(errOut, "GL0005") {
		t.Errorf("tcsh: %d %s", code, errOut)
	}
}

// Every command shows at least two examples in its help.
func TestEveryCommandHasExamples(t *testing.T) {
	root, err := Tree("en")
	if err != nil {
		t.Fatal(err)
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || !sub.IsAvailableCommand() && sub.Name() != "help" {
				continue
			}
			if sub.HasAvailableSubCommands() {
				walk(sub)
				continue
			}
			lines := 0
			for _, l := range strings.Split(sub.Example, "\n") {
				if strings.TrimSpace(l) != "" {
					lines++
				}
			}
			if lines < 2 {
				t.Errorf("%s has %d examples", sub.CommandPath(), lines)
			}
		}
	}
	walk(root)
}
