// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command gen-demo builds greenlens, runs a short sequence of commands with
// it and writes an animated SVG of a terminal session showing their real
// output. The README and the documentation site embed the result, so the
// demo always matches what the binary does. Run it with "make demo".
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// commands is the session shown in the demo, as arguments to greenlens.
var commands = [][]string{
	{"--help"},
	{"verson"},
	{"version", "--nope"},
}

const (
	fontSize   = 14.0
	charWidth  = 8.4
	lineHeight = 20.0
	padX       = 22.0
	padTop     = 54.0
	padBottom  = 22.0
	maxCols    = 100
	rows       = 18

	typeMS        = 55
	afterTypeMS   = 450
	perLineMS     = 35
	afterOutputMS = 2200
	holdMS        = 3500
	fadeMS        = 600
)

type line struct {
	text    string
	command bool
	startMS int
	endMS   int
}

func main() {
	lang := flag.String("lang", "en", "language of the session")
	out := flag.String("o", "", "output SVG file")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "gen-demo: -o is required")
		os.Exit(2)
	}
	if err := run(*lang, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen-demo:", err)
		os.Exit(1)
	}
}

func run(lang, out string) error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "gen-demo-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bin := filepath.Join(dir, "greenlens")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", bin, "./cmd/greenlens") //nolint:gosec // output path is a fresh temporary directory
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %w\n%s", err, b)
	}

	var lines []line
	t := 600
	for _, args := range commands {
		prompt := "$ greenlens " + strings.Join(args, " ")
		typing := typeMS * (len(prompt) - 2)
		lines = append(lines, line{text: prompt, command: true, startMS: t, endMS: t + typing})
		t += typing + afterTypeMS
		output, err := execute(ctx, bin, lang, args)
		if err != nil {
			return err
		}
		for _, l := range output {
			lines = append(lines, line{text: l, startMS: t})
			t += perLineMS
		}
		lines = append(lines, line{startMS: t})
		t += afterOutputMS
	}
	total := t + holdMS
	return os.WriteFile(out, render(lines, total, lang), 0o644) //nolint:gosec // public documentation asset
}

// execute runs greenlens with a clean environment so the output does not
// depend on the locale of the machine that generates the demo.
func execute(ctx context.Context, bin, lang string, args []string) ([]string, error) {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the binary was just built from this repository
	cmd.Env = []string{"GREENLENS_LANG=" + lang, "NO_COLOR=1", "PATH=/usr/bin:/bin"}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, err
		}
	}
	text := strings.TrimRight(buf.String(), "\n")
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if len([]rune(l)) > maxCols {
			return nil, fmt.Errorf("output line longer than %d columns: %q", maxCols, l)
		}
		out = append(out, l)
	}
	return out, nil
}

func render(lines []line, totalMS int, lang string) []byte {
	cols := 0
	for _, l := range lines {
		cols = max(cols, len([]rune(l.text)))
	}
	width := padX*2 + float64(cols+1)*charWidth
	visible := min(rows, len(lines))
	height := padTop + float64(visible)*lineHeight + padBottom
	pct := func(ms int) string { return fmt.Sprintf("%.3f%%", float64(ms)*100/float64(totalMS)) }
	fadeStart := totalMS - fadeMS

	var b strings.Builder
	title := "greenlens"
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img" aria-label="%s">`+"\n",
		width, height, width, height, html.EscapeString(demoLabel(lang)))
	b.WriteString("<style>\n")
	b.WriteString(`text{font-family:"IBM Plex Mono","SFMono-Regular",Menlo,Consolas,"Liberation Mono",monospace;font-size:14px;white-space:pre}` + "\n")
	b.WriteString(".o{fill:#E8F0FA}.c{fill:#E8F0FA}.p{fill:#35C9B0}.e{fill:#F2A541}.m{fill:#9DB2C9}\n")
	b.WriteString("@keyframes cur{0%,49%{opacity:1}50%,100%{opacity:0}}.cur{animation:cur 1s steps(1) infinite}\n")
	for i, l := range lines {
		if l.text == "" {
			continue
		}
		// Every element shares the cycle length, so all of them restart
		// together and the session loops.
		fmt.Fprintf(&b, "@keyframes s%d{0%%,%s{opacity:0}%s,%s{opacity:1}100%%{opacity:0}}.s%d{animation:s%d %dms linear infinite}\n",
			i, pct(max(l.startMS-1, 0)), pct(l.startMS), pct(fadeStart), i, i, totalMS)
		if l.command {
			n := len([]rune(l.text)) - 2
			dx := float64(n) * charWidth
			fmt.Fprintf(&b, "@keyframes t%d{0%%,%s{transform:translateX(0);animation-timing-function:steps(%d,end)}%s,%s{transform:translateX(%.1fpx);opacity:1}%s,100%%{transform:translateX(%.1fpx);opacity:0}}.t%d{animation:t%d %dms linear infinite}\n",
				i, pct(l.startMS), n, pct(l.endMS), pct(l.endMS+afterTypeMS-1), dx, pct(l.endMS+afterTypeMS), dx, i, i, totalMS)
		}
	}
	// The session scrolls like a real terminal: when line i appears below
	// the last visible row, everything moves up so that line i is the last.
	b.WriteString("@keyframes scroll{0%{transform:translateY(0)}")
	last, lastMS := 0.0, 0
	for i, l := range lines {
		offset := float64(max(0, i-visible+1)) * lineHeight
		if offset == last {
			continue
		}
		// Hold the previous position until just before the new line, so
		// long pauses do not turn into a slow drift.
		if hold := l.startMS - 120; hold > lastMS {
			fmt.Fprintf(&b, "%s{transform:translateY(-%.1fpx)}", pct(hold), last)
		}
		fmt.Fprintf(&b, "%s{transform:translateY(-%.1fpx)}", pct(l.startMS), offset)
		last, lastMS = offset, l.startMS
	}
	fmt.Fprintf(&b, "%s{transform:translateY(-%.1fpx)}100%%{transform:translateY(-%.1fpx)}}", pct(fadeStart), last, last)
	fmt.Fprintf(&b, ".scroll{animation:scroll %dms linear infinite}\n", totalMS)
	b.WriteString("@media (prefers-reduced-motion:reduce){*{animation:none!important}}\n</style>\n")
	fmt.Fprintf(&b, `<rect width="%.0f" height="%.0f" rx="12" fill="#0F1B2D"/>`+"\n", width, height)
	fmt.Fprintf(&b, `<rect x=".5" y=".5" width="%.0f" height="%.0f" rx="12" fill="none" stroke="#E2EEFF" stroke-opacity=".16"/>`+"\n", width-1, height-1)
	fmt.Fprintf(&b, `<rect width="%.0f" height="34" rx="12" fill="#D6E8FF" fill-opacity=".07"/>`+"\n", width)
	for i, c := range []string{"#EF5A5A", "#F2A541", "#35C9B0"} {
		fmt.Fprintf(&b, `<circle cx="%d" cy="17" r="6" fill="%s" fill-opacity=".85"/>`+"\n", 22+i*20, c)
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="22" text-anchor="middle" class="m">%s</text>`+"\n", width/2, title)
	fmt.Fprintf(&b, `<clipPath id="screen"><rect x="0" y="%.1f" width="%.0f" height="%.1f"/></clipPath>`+"\n",
		padTop-6, width, float64(visible)*lineHeight+8)
	b.WriteString(`<g clip-path="url(#screen)"><g class="scroll">` + "\n")

	for i, l := range lines {
		if l.text == "" {
			continue
		}
		y := padTop + float64(i)*lineHeight + fontSize
		if l.command {
			n := len([]rune(l.text)) - 2
			x := padX + 2*charWidth
			fmt.Fprintf(&b, `<g class="s%d"><text x="%.1f" y="%.1f" class="p">$</text>`, i, padX, y)
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="c" textLength="%.1f" lengthAdjust="spacingAndGlyphs">%s</text>`,
				x, y, float64(n)*charWidth, html.EscapeString(l.text[2:]))
			fmt.Fprintf(&b, `<g class="t%d"><rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="#0F1B2D"/>`,
				i, x, y-fontSize, float64(n)*charWidth+charWidth*2, lineHeight)
			fmt.Fprintf(&b, `<rect class="cur" x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="#E8F0FA" fill-opacity=".8"/></g></g>`+"\n",
				x, y-fontSize+2, charWidth, fontSize+2)
			continue
		}
		class := "o"
		if strings.HasPrefix(l.text, "error ") {
			class = "e"
		} else if strings.HasPrefix(strings.TrimSpace(l.text), "cause") || strings.HasPrefix(strings.TrimSpace(l.text), "fix") ||
			strings.HasPrefix(strings.TrimSpace(l.text), "causa") || strings.HasPrefix(strings.TrimSpace(l.text), "soluci") {
			class = "m"
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="%s s%d" textLength="%.1f" lengthAdjust="spacingAndGlyphs">%s</text>`+"\n",
			padX, y, class, i, float64(len([]rune(l.text)))*charWidth, html.EscapeString(l.text))
	}
	b.WriteString("</g></g>\n</svg>\n")
	return []byte(b.String())
}

func demoLabel(lang string) string {
	if lang == "es" {
		return "Sesión de terminal con greenlens: ayuda, sugerencia de comando y error con causa y solución"
	}
	return "Terminal session with greenlens: help, a command suggestion and an error with its cause and fix"
}
