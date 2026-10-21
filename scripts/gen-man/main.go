// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command gen-man writes the manual pages of greenlens in English to
// <out>/man1 and <out>/man5, and in Spanish to <out>/es/man1 and
// <out>/es/man5, where man finds them by locale.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MarioUrenaGarcia/greenlens/internal/cli"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/manpage"
)

func main() {
	out := flag.String("out", "dist/man", "directory of the pages")
	date := flag.String("date", "", "date of the release, as YYYY-MM-DD")
	version := flag.String("version", "dev", "version of the release")
	flag.Parse()
	if err := run(*out, manpage.Header{Date: *date, Version: *version}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out string, h manpage.Header) error {
	for lang, dir := range map[string]string{"en": out, "es": filepath.Join(out, "es")} {
		l, err := i18n.New(lang)
		if err != nil {
			return err
		}
		tree, err := cli.Tree(lang)
		if err != nil {
			return err
		}
		pages := manpage.Commands(tree, l, h)
		for _, p := range pages {
			if err := write(filepath.Join(dir, "man1"), p); err != nil {
				return err
			}
		}
		if err := write(filepath.Join(dir, "man5"), manpage.Config(l, h)); err != nil {
			return err
		}
	}
	return nil
}

func write(dir string, p manpage.Page) error {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // manual pages are read by every user
		return err
	}
	return os.WriteFile(filepath.Join(dir, p.Name), []byte(p.Content), 0o644) //nolint:gosec // manual pages are read by every user
}
