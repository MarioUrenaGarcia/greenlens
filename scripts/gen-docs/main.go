// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Command gen-docs writes the reference pages that are derived from the
// code, in every supported language, and the JSON schema of the
// configuration, so they cannot drift from the binary. Run it from the
// repository root with "make docs".
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/MarioUrenaGarcia/greenlens/internal/cli"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
)

type texts struct {
	errorsTitle, errorsIntro          string
	code, message, cause, fix, exit   string
	cliTitle, cliIntro, usage, flags  string
	globalFlags, examples, flag, desc string
	configTitle, configIntro          string
	key, env, typ, def                string
}

var pages = map[string]texts{
	"en": {
		errorsTitle: "Error codes",
		errorsIntro: "Every error that GreenLens reports has a stable code, a one-line message, the cause and a suggested fix. Text in braces is replaced with the actual values, such as the name of the command.",
		code:        "Code", message: "Message", cause: "Cause", fix: "Fix", exit: "Exit code",
		cliTitle: "Commands",
		cliIntro: "Every command of greenlens with its options and examples. The same text is available offline with 'greenlens help <command>'.",
		usage:    "Usage", flags: "Options", globalFlags: "Global options", examples: "Examples", flag: "Option", desc: "Description",
		configTitle: "Configuration",
		configIntro: "Every key of the configuration file, the environment variable that overrides it and its default value. Values come, from the highest precedence to the lowest, from command line options, environment variables, the file and the defaults. Unknown keys are errors. The file deploy/config/greenlens.example.yaml lists every key, and api/config.schema.json describes them for editors.",
		key:         "Key", env: "Environment variable", typ: "Type", def: "Default",
	},
	"es": {
		errorsTitle: "Códigos de error",
		errorsIntro: "Cada error que reporta GreenLens tiene un código estable, un mensaje de una línea, la causa y una solución sugerida. El texto entre llaves se reemplaza por los valores reales, como el nombre del comando.",
		code:        "Código", message: "Mensaje", cause: "Causa", fix: "Solución", exit: "Código de salida",
		cliTitle: "Comandos",
		cliIntro: "Todos los comandos de greenlens con sus opciones y ejemplos. El mismo texto está disponible sin conexión con 'greenlens help <comando>'.",
		usage:    "Uso", flags: "Opciones", globalFlags: "Opciones globales", examples: "Ejemplos", flag: "Opción", desc: "Descripción",
		configTitle: "Configuración",
		configIntro: "Cada clave del archivo de configuración, la variable de entorno que la reemplaza y su valor por defecto. Los valores vienen, de mayor a menor precedencia, de las opciones de la línea de comandos, las variables de entorno, el archivo y los valores por defecto. Las claves desconocidas son errores. El archivo deploy/config/greenlens.example.yaml lista todas las claves, y api/config.schema.json las describe para los editores.",
		key:         "Clave", env: "Variable de entorno", typ: "Tipo", def: "Por defecto",
	},
}

func main() {
	root := flag.String("root", "docs", "documentation directory")
	schema := flag.String("schema", "api/config.schema.json", "configuration schema file")
	flag.Parse()
	if err := run(*root, *schema); err != nil {
		fmt.Fprintln(os.Stderr, "gen-docs:", err)
		os.Exit(1)
	}
}

func run(root, schemaPath string) error {
	en, err := i18n.New(i18n.Default)
	if err != nil {
		return err
	}
	for _, f := range config.Fields() {
		for _, lang := range i18n.Supported {
			l, err := i18n.New(lang)
			if err != nil {
				return err
			}
			if !l.Has(f.DocKey()) {
				return fmt.Errorf("%s: no description for %s (%s)", lang, f.Key, f.DocKey())
			}
		}
	}
	for _, lang := range i18n.Supported {
		l, err := i18n.New(lang)
		if err != nil {
			return err
		}
		dir := filepath.Join(root, "reference")
		if lang != i18n.Default {
			dir = filepath.Join(root, lang, "reference")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // documentation tree
			return err
		}
		tree, err := cli.Tree(lang)
		if err != nil {
			return err
		}
		for name, body := range map[string]string{
			"errors.md":        errorsPage(l, pages[lang]),
			"cli.md":           cliPage(l, pages[lang], tree),
			"configuration.md": configPage(l, pages[lang]),
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil { //nolint:gosec // documentation file
				return err
			}
		}
	}
	data, err := json.MarshalIndent(config.Schema(func(f config.Field) string { return en.T(f.DocKey()) }), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil { //nolint:gosec // repository tree
		return err
	}
	return os.WriteFile(schemaPath, append(data, '\n'), 0o644) //nolint:gosec // public schema
}

func frontMatter(title string, weight int) string {
	return fmt.Sprintf("---\n# Generated by \"make docs\" from the code and the translation catalogs.\ntitle: %q\nweight: %d\n---\n\n", title, weight)
}

func errorsPage(l *i18n.Localizer, p texts) string {
	codes := help.Codes()
	slices.Sort(codes)
	var b strings.Builder
	b.WriteString(frontMatter(p.errorsTitle, 30))
	b.WriteString(p.errorsIntro + "\n\n")
	fmt.Fprintf(&b, "| %s | %s | %s |\n|---|---|---|\n", p.code, p.message, p.exit)
	for _, c := range codes {
		e := help.New(c)
		fmt.Fprintf(&b, "| [%s](#%s) | %s | %d |\n", c, strings.ToLower(string(c)), escape(e.Message(l)), e.ExitCode())
	}
	for _, c := range codes {
		e := help.New(c)
		fmt.Fprintf(&b, "\n## %s\n\n", c)
		fmt.Fprintf(&b, "- **%s:** %s\n", p.message, escape(e.Message(l)))
		fmt.Fprintf(&b, "- **%s:** %s\n", p.cause, escape(e.Cause(l)))
		fmt.Fprintf(&b, "- **%s:** %s\n", p.fix, escape(e.Fix(l)))
		fmt.Fprintf(&b, "- **%s:** %d\n", p.exit, e.ExitCode())
	}
	return b.String()
}

func cliPage(l *i18n.Localizer, p texts, root *cobra.Command) string {
	var b strings.Builder
	b.WriteString(frontMatter(p.cliTitle, 10))
	b.WriteString(escape(p.cliIntro) + "\n\n")
	b.WriteString("## " + p.globalFlags + "\n\n")
	flagTable(&b, p, root.PersistentFlags())
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden {
				continue
			}
			fmt.Fprintf(&b, "\n## %s\n\n", sub.CommandPath())
			long := sub.Long
			if long == "" {
				long = sub.Short
			}
			b.WriteString(strings.ReplaceAll(long, "\n", " ") + "\n\n")
			use := sub.UseLine()
			if sub.HasAvailableFlags() {
				use += " [" + cli.FlagsPlaceholder(l) + "]"
			}
			fmt.Fprintf(&b, "**%s**\n\n```text\n%s\n```\n", p.usage, use)
			if sub.HasAvailableLocalFlags() {
				fmt.Fprintf(&b, "\n**%s**\n\n", p.flags)
				flagTable(&b, p, sub.LocalFlags())
			}
			if sub.Example != "" {
				fmt.Fprintf(&b, "\n**%s**\n\n```bash\n%s\n```\n", p.examples, strings.TrimLeft(dedent(sub.Example), "\n"))
			}
			walk(sub)
		}
	}
	walk(root)
	return b.String()
}

func flagTable(b *strings.Builder, p texts, fs *pflag.FlagSet) {
	fmt.Fprintf(b, "| %s | %s | %s |\n|---|---|---|\n", p.flag, p.desc, p.def)
	fs.VisitAll(func(f *pflag.Flag) {
		// Every command has --help; listing it everywhere adds nothing.
		if f.Hidden || f.Name == "help" {
			return
		}
		name := "`--" + f.Name + "`"
		if f.Shorthand != "" {
			name = "`-" + f.Shorthand + "`, " + name
		}
		_, usage := pflag.UnquoteUsage(f)
		def := f.DefValue
		if def == "" || def == "false" || def == "0" || def == "0s" {
			def = ""
		} else {
			def = "`" + def + "`"
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", name, escape(usage), def)
	})
}

func configPage(l *i18n.Localizer, p texts) string {
	var b strings.Builder
	b.WriteString(frontMatter(p.configTitle, 20))
	b.WriteString(p.configIntro + "\n\n")
	section := ""
	for _, f := range config.Fields() {
		top := strings.SplitN(f.Key, ".", 2)[0]
		if top != section {
			section = top
			fmt.Fprintf(&b, "\n## %s\n\n| %s | %s | %s | %s | %s |\n|---|---|---|---|---|\n", section, p.key, p.env, p.typ, p.def, p.desc)
		}
		def := f.Default
		if def != "empty" && def != "" {
			def = "`" + def + "`"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s |\n", f.Key, f.Env, f.Type, def, escape(l.T(f.DocKey())))
	}
	return b.String()
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "  ")
	}
	return strings.Join(lines, "\n")
}

// escape keeps placeholders and quotes readable in Markdown tables.
func escape(s string) string {
	return strings.NewReplacer("|", "\\|", "{", "`{", "}", "}`", "<", "&lt;", ">", "&gt;").Replace(s)
}
