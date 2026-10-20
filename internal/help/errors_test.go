// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package help

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

func TestEveryCodeIsDocumentedInEveryLanguage(t *testing.T) {
	for _, lang := range i18n.Supported {
		l, err := i18n.New(lang)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range Codes() {
			for _, part := range []string{"message", "cause", "fix"} {
				key := "errors." + string(c) + "." + part
				if !l.Has(key) {
					t.Errorf("%s: missing %s", lang, key)
				}
			}
		}
	}
}

func TestRender(t *testing.T) {
	en, _ := i18n.New("en")
	es, _ := i18n.New("es")
	err := New(GL0003, "flag", "--x", "command", "greenlens version")
	wantEN := "error GL0003: unknown flag '--x'\n" +
		"  cause: 'greenlens version' does not accept the flag '--x'\n" +
		"  fix:   run 'greenlens version --help' to list the accepted flags"
	if got := Render(en, err); got != wantEN {
		t.Errorf("en:\n%s\nwant:\n%s", got, wantEN)
	}
	wantES := "error GL0003: opción desconocida '--x'\n" +
		"  causa:    'greenlens version' no acepta la opción '--x'\n" +
		"  solución: ejecuta 'greenlens version --help' para ver las opciones aceptadas"
	if got := Render(es, err); got != wantES {
		t.Errorf("es:\n%s\nwant:\n%s", got, wantES)
	}
}

func TestFixVariant(t *testing.T) {
	en, _ := i18n.New("en")
	e := New(GL0002, "command", "verson", "parent", "greenlens", "suggestion", "version").WithFix("fix_suggest")
	if got := e.Fix(en); !strings.HasPrefix(got, "did you mean 'version'?") {
		t.Errorf("Fix = %q", got)
	}
}

func TestUncodedErrorsBecomeInternal(t *testing.T) {
	base := errors.New("disk on fire")
	e := As(fmt.Errorf("wrapped: %w", base))
	if e.Code != GL0001 || !errors.Is(e, base) {
		t.Fatalf("As = %#v", e)
	}
	if ExitCode(base) != ExitError {
		t.Errorf("ExitCode = %d", ExitCode(base))
	}
	coded := fmt.Errorf("context: %w", New(GL0007, "command", "x", "args", "y"))
	if ExitCode(coded) != ExitUsage {
		t.Errorf("wrapped coded error lost its exit code")
	}
	if ExitCode(nil) != ExitOK {
		t.Errorf("ExitCode(nil) != 0")
	}
	if got := New(GL0002, "command", "a", "parent", "b").Error(); got != "GL0002: unknown command 'a': 'a' is not a command of 'b'" {
		t.Errorf("Error() = %q", got)
	}
}
