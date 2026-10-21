// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package tui is greenlens top: a live view of the node, its backends and
// models, its consumers and its energy sources, read from the agent. It
// works from 80 by 24 cells, in color or monochrome, and in ASCII when the
// terminal cannot show Unicode.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/i18n"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// Minimum size of the terminal.
const (
	MinWidth  = 80
	MinHeight = 24
)

// Source is what the TUI reads: the agent.
type Source interface {
	Status(ctx context.Context) (api.Status, error)
	Live(ctx context.Context) (api.Live, error)
	BackendUsage(ctx context.Context, from, to time.Time, groupBy string) (api.BackendUsage, error)
}

// Usage reports the CPU time, in clock ticks, and the resident memory of
// the TUI itself.
type Usage func() (ticks uint64, rssBytes uint64, ok bool)

// Options configure the TUI.
type Options struct {
	Source  Source
	L       *i18n.Localizer
	Refresh time.Duration
	Color   bool
	Unicode bool
	Raw     bool
	// Export writes the CSV of a view and returns where.
	Export func(name string, rows [][]string) (string, error)
	Self   Usage
	Now    func() time.Time
}

type view int

const (
	viewNode view = iota
	viewBackends
	viewConsumers
	viewSources
	viewCount
)

var viewKeys = [viewCount]string{"node", "backends", "consumers", "sources"}

type tickMsg time.Time

type dataMsg struct {
	at        time.Time
	status    api.Status
	live      api.Live
	consumers api.BackendUsage
	err       error
}

type keyMap struct {
	next, prev, views, up, down, sort, filter, pause, export, help, quit key.Binding
}

func newKeys(l *i18n.Localizer) keyMap {
	b := func(help string, keys ...string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(keys[0], l.T("tui.key."+help)))
	}
	k := keyMap{
		next:   b("next", "tab"),
		prev:   b("prev", "shift+tab"),
		views:  b("views", "1", "2", "3", "4"),
		up:     b("up", "up", "k"),
		down:   b("down", "down", "j"),
		sort:   b("sort", "s"),
		filter: b("filter", "/"),
		pause:  b("pause", "p"),
		export: b("export", "e"),
		help:   b("help", "?"),
		quit:   b("quit", "q", "ctrl+c"),
	}
	k.views.SetHelp("1-4", l.T("tui.key.views"))
	k.up.SetHelp("up/k", l.T("tui.key.up"))
	k.down.SetHelp("down/j", l.T("tui.key.down"))
	return k
}

// ShortHelp implements help.KeyMap.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.views, k.filter, k.sort, k.pause, k.export, k.help, k.quit}
}

// FullHelp implements help.KeyMap.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.next, k.prev, k.views, k.up, k.down}, {k.sort, k.filter, k.pause, k.export, k.help, k.quit}}
}

// row is a line of a table with the values it sorts by.
type row struct {
	cells  []string
	keys   []float64
	group  string
	header bool
	csv    []string
}

type model struct {
	o    Options
	l    *i18n.Localizer
	t    theme
	keys keyMap
	help help.Model
	tbl  table.Model

	width, height int
	view          view
	paused        bool
	showHelp      bool
	filtering     bool
	filter        input
	sortBy        [viewCount]int

	have      bool
	err       error
	at        time.Time
	status    api.Status
	live      api.Live
	consumers api.BackendUsage

	prevTokens map[string]int64
	prevAt     time.Time
	rates      map[string]float64

	selfTicks uint64
	selfAt    time.Time
	selfCPU   float64
	selfRSS   uint64

	message string
}

// New returns the model of the TUI.
func New(o Options) tea.Model {
	if o.Refresh <= 0 {
		o.Refresh = time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	m := &model{o: o, l: o.L, t: newTheme(o.Color, o.Unicode), keys: newKeys(o.L), rates: map[string]float64{},
		width: MinWidth, height: MinHeight}
	m.help = help.New()
	m.help.ShortSeparator, m.help.FullSeparator, m.help.Ellipsis = "  ", "    ", "..."
	hs := help.Styles{ShortKey: m.t.keyHint, ShortDesc: m.t.muted, ShortSeparator: m.t.muted, Ellipsis: m.t.muted,
		FullKey: m.t.keyHint, FullDesc: m.t.muted, FullSeparator: m.t.muted}
	m.help.Styles = hs
	m.tbl = table.New(table.WithFocused(true))
	km := table.DefaultKeyMap()
	km.LineUp, km.LineDown = m.keys.up, m.keys.down
	km.PageUp = key.NewBinding(key.WithKeys("pgup"))
	km.PageDown = key.NewBinding(key.WithKeys("pgdown"))
	km.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"))
	km.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"))
	km.GotoTop = key.NewBinding(key.WithKeys("home", "g"))
	km.GotoBottom = key.NewBinding(key.WithKeys("end", "G"))
	m.tbl.KeyMap = km
	ts := table.Styles{Header: m.t.muted.Bold(true).Padding(0, 1), Cell: lipgloss.NewStyle().Padding(0, 1), Selected: m.t.selected}
	m.tbl.SetStyles(ts)
	return m
}

// Init implements tea.Model.
func (m *model) Init() tea.Cmd {
	return tea.Batch(m.fetch(), m.tick())
}

func (m *model) tick() tea.Cmd {
	return tea.Tick(m.o.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// fetch reads the agent without holding up the interface.
func (m *model) fetch() tea.Cmd {
	src, now, wantConsumers := m.o.Source, m.o.Now(), m.view == viewConsumers
	started := m.status.StartedAt
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d := dataMsg{at: now}
		if d.status, d.err = src.Status(ctx); d.err != nil {
			return d
		}
		if d.live, d.err = src.Live(ctx); d.err != nil {
			return d
		}
		if wantConsumers {
			from := started
			if from.IsZero() || now.Sub(from) > 24*time.Hour {
				from = now.Add(-24 * time.Hour)
			}
			d.consumers, d.err = src.BackendUsage(ctx, from, now.Add(time.Second), store.ByConsumer)
		}
		return d
	}
}

// Update implements tea.Model.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
	case tickMsg:
		cmds := []tea.Cmd{m.tick()}
		if !m.paused {
			cmds = append(cmds, m.fetch())
		}
		return m, tea.Batch(cmds...)
	case dataMsg:
		m.receive(msg)
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *model) receive(d dataMsg) {
	m.err = d.err
	if d.err != nil {
		return
	}
	consumers := m.consumers
	if m.view == viewConsumers {
		consumers = d.consumers
	}
	m.have, m.at, m.status, m.live, m.consumers = true, d.at, d.status, d.live, consumers
	m.updateRates()
	m.updateSelf()
	m.layout()
}

// updateRates turns the generated tokens of every model into tokens per
// second between two readings.
func (m *model) updateRates() {
	cur := map[string]int64{}
	for _, b := range m.status.Backends {
		for _, md := range b.Models {
			cur[b.Name+"/"+md.Name] = md.GeneratedTokens
		}
	}
	if m.prevTokens != nil {
		if dt := m.at.Sub(m.prevAt).Seconds(); dt > 0 {
			for k, v := range cur {
				if p, ok := m.prevTokens[k]; ok && v >= p {
					m.rates[k] = float64(v-p) / dt
				}
			}
		}
	}
	m.prevTokens, m.prevAt = cur, m.at
}

func (m *model) updateSelf() {
	if m.o.Self == nil {
		return
	}
	ticks, rss, ok := m.o.Self()
	if !ok {
		return
	}
	if !m.selfAt.IsZero() {
		if dt := m.at.Sub(m.selfAt).Seconds(); dt > 0 && ticks >= m.selfTicks {
			// Clock ticks are 10 ms.
			m.selfCPU = 100 * float64(ticks-m.selfTicks) / 100 / dt
		}
	}
	m.selfTicks, m.selfAt, m.selfRSS = ticks, m.at, rss
}

func (m *model) key(k tea.KeyPressMsg) tea.Cmd {
	if m.filtering {
		switch k.String() {
		case "enter":
			m.filtering = false
		case "esc":
			m.filtering = false
			m.filter = input{}
		default:
			m.filter.key(k)
		}
		m.refreshTable()
		return nil
	}
	if m.showHelp {
		switch {
		case key.Matches(k, m.keys.quit):
			return tea.Quit
		case key.Matches(k, m.keys.help), k.String() == "esc":
			m.showHelp = false
		}
		return nil
	}
	m.message = ""
	switch {
	case key.Matches(k, m.keys.quit):
		return tea.Quit
	case key.Matches(k, m.keys.help):
		m.showHelp = true
	case key.Matches(k, m.keys.next):
		m.setView((m.view + 1) % viewCount)
		return m.fetch()
	case key.Matches(k, m.keys.prev):
		m.setView((m.view + viewCount - 1) % viewCount)
		return m.fetch()
	case key.Matches(k, m.keys.views):
		m.setView(view(k.String()[0] - '1'))
		return m.fetch()
	case key.Matches(k, m.keys.pause):
		m.paused = !m.paused
	case key.Matches(k, m.keys.sort):
		if n := len(m.columns()); n > 0 {
			m.sortBy[m.view] = (m.sortBy[m.view] + 1) % n
			m.refreshTable()
		}
	case key.Matches(k, m.keys.filter):
		if m.view != viewNode {
			m.filtering = true
		}
	case key.Matches(k, m.keys.export):
		m.export()
	default:
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(k)
		return cmd
	}
	return nil
}

func (m *model) setView(v view) {
	if v == m.view {
		return
	}
	m.view, m.filter, m.filtering = v, input{}, false
	m.tbl.SetCursor(0)
	m.layout()
	m.refreshTable()
}

func (m *model) export() {
	if m.o.Export == nil {
		return
	}
	var rows [][]string
	if m.view == viewNode {
		rows = m.nodeCSV()
	} else {
		cols := m.columns()
		header := make([]string, len(cols))
		for i, c := range cols {
			header[i] = c.csv
		}
		rows = append(rows, header)
		for _, r := range m.rows() {
			rows = append(rows, r.csv)
		}
	}
	path, err := m.o.Export(viewKeys[m.view], rows)
	if err != nil {
		m.message = m.l.T("tui.export_failed", "detail", err.Error())
		return
	}
	m.message = m.l.T("tui.exported", "path", path)
}

// maxProblems bounds the problems listed under the sources.
const maxProblems = 5

// layout sizes the table to the space left by the header, the footer and,
// for the sources, the list of problems.
func (m *model) layout() {
	h := m.height - 6
	if n := len(m.status.Problems); m.view == viewSources && n > 0 {
		h -= min(n, maxProblems) + 2
	}
	m.tbl.SetHeight(max(1, h))
	m.tbl.SetWidth(m.width)
	m.refreshTable()
}

type column struct {
	title   string
	width   int
	numeric bool
	csv     string
}

func (m *model) refreshTable() {
	cols := m.columns()
	if len(cols) == 0 {
		return
	}
	// Cells are cut here, with an ASCII ellipsis, so the table never cuts
	// them itself.
	tc := make([]table.Column, len(cols))
	for i, c := range cols {
		title := c.title
		if i == m.sortBy[m.view] {
			title += " " + m.sortMark(c)
		}
		tc[i] = table.Column{Title: truncate(title, c.width), Width: c.width}
	}
	rs := m.rows()
	tr := make([]table.Row, len(rs))
	for i, r := range rs {
		cells := make(table.Row, len(r.cells))
		for j, c := range r.cells {
			if j < len(cols) {
				cells[j] = truncate(c, cols[j].width)
			}
		}
		tr[i] = cells
	}
	// Rows are replaced before columns so the table never holds rows with
	// more cells than columns.
	m.tbl.SetRows(nil)
	m.tbl.SetColumns(tc)
	m.tbl.SetRows(tr)
	if c := m.tbl.Cursor(); c >= len(tr) {
		m.tbl.SetCursor(max(0, len(tr)-1))
	}
}

func (m *model) sortMark(c column) string {
	if !c.numeric {
		return "^"
	}
	return "v"
}

// sortRows orders rows by the sort column, keeping every model under its
// backend.
func (m *model) sortRows(rs []row) []row {
	col := m.sortBy[m.view]
	cols := m.columns()
	numeric := col < len(cols) && cols[col].numeric
	less := func(a, b row) bool {
		if numeric {
			if a.keys[col] != b.keys[col] {
				return a.keys[col] > b.keys[col]
			}
		} else if c := strings.Compare(strings.ToLower(a.cells[0]), strings.ToLower(b.cells[0])); c != 0 {
			return c < 0
		}
		return a.cells[0] < b.cells[0]
	}
	groups := map[string][]row{}
	var heads []row
	for _, r := range rs {
		if r.header {
			heads = append(heads, r)
		} else {
			groups[r.group] = append(groups[r.group], r)
		}
	}
	if len(heads) == 0 {
		sort.SliceStable(rs, func(i, j int) bool { return less(rs[i], rs[j]) })
		return rs
	}
	sort.SliceStable(heads, func(i, j int) bool { return less(heads[i], heads[j]) })
	var out []row
	for _, h := range heads {
		out = append(out, h)
		g := groups[h.group]
		sort.SliceStable(g, func(i, j int) bool { return less(g[i], g[j]) })
		out = append(out, g...)
	}
	return out
}

// matches keeps the rows that contain the filter, with the backend of a
// model that matches.
func (m *model) matches(rs []row) []row {
	f := strings.ToLower(m.filter.value())
	if f == "" {
		return rs
	}
	keep := map[string]bool{}
	for _, r := range rs {
		if strings.Contains(strings.ToLower(strings.Join(r.cells, " ")), f) {
			keep[r.group] = true
		}
	}
	var out []row
	for _, r := range rs {
		if strings.Contains(strings.ToLower(strings.Join(r.cells, " ")), f) || (r.header && keep[r.group]) {
			out = append(out, r)
		}
	}
	return out
}

func (m *model) rows() []row {
	var rs []row
	switch m.view {
	case viewBackends:
		rs = m.backendRows()
	case viewConsumers:
		rs = m.consumerRows()
	case viewSources:
		rs = m.sourceRows()
	default:
		return nil
	}
	return m.sortRows(m.matches(rs))
}

func (m *model) columns() []column {
	l := m.l
	switch m.view {
	case viewBackends:
		// Every column has one cell of padding on each side; at 80 cells
		// the widths add up to 66 with the cost column and 65 without.
		cols := []column{
			{l.T("tui.col.backend"), 20, false, "backend_model"},
			{l.T("tui.col.power"), 9, true, "power_w"},
			{l.T("tui.col.energy"), 9, true, "energy_j"},
			{l.T("tui.col.jpt"), 9, true, "j_per_generated_token"},
			{l.T("tui.col.tps"), 7, true, "generated_tokens_per_s"},
			{"CO2e", 11, true, "co2e_g"},
		}
		if m.priced() {
			cols[0].width, cols[3].width, cols[4].width = 13, 8, 6
			cols = append(cols, column{l.T("tui.col.cost"), 10, true, "cost"})
		}
		return cols
	case viewConsumers:
		return []column{
			{l.T("tui.col.consumer"), 20, false, "consumer"},
			{l.T("tui.col.backend"), 16, false, "backend"},
			{l.T("tui.col.energy"), 9, true, "energy_j"},
			{l.T("tui.col.tokens"), 13, true, "generated_tokens"},
			{l.T("tui.col.jpt"), 9, true, "j_per_generated_token"},
		}
	case viewSources:
		return []column{
			{l.T("tui.col.channel"), 24, false, "channel"},
			{l.T("tui.col.domain"), 14, false, "domain"},
			{l.T("tui.col.quality"), 9, false, "quality"},
			{l.T("tui.col.reason"), 25, false, "reason"},
		}
	case viewNode, viewCount:
		// The node is a chart, not a table.
	}
	return nil
}

func (m *model) energy(j float64) string { return fmtEnergy(j, m.o.Raw) }

// priced reports whether the agent has a tariff, so there is a cost.
func (m *model) priced() bool {
	f := m.status.Factors
	return f.TariffPerKWh != nil || f.TariffPeriods > 0
}

func (m *model) backendRows() []row {
	var rs []row
	cost, currency := m.priced(), m.status.Factors.Currency
	add := func(r row) {
		if !cost {
			r.cells, r.keys, r.csv = r.cells[:6], r.keys[:6], r.csv[:6]
		}
		rs = append(rs, r)
	}
	for _, b := range m.status.Backends {
		ind := b.Indicators
		var tps float64
		for _, md := range b.Models {
			tps += m.rates[b.Name+"/"+md.Name]
		}
		add(row{
			group: b.Name, header: true,
			cells: []string{b.Name, fmtPower(b.PowerW, m.o.Raw), m.energy(b.EnergyJ), "", fmtRate(tps), fmtCO2e(ind.CO2eG, m.o.Raw), fmtCost(ind.Cost, currency, m.o.Raw)},
			keys:  []float64{0, b.PowerW, b.EnergyJ, 0, tps, ind.CO2eG, deref(ind.Cost)},
			csv:   []string{b.Name, num(b.PowerW), num(b.EnergyJ), "", num(tps), num(ind.CO2eG), numPtr(ind.Cost)},
		})
		for _, md := range b.Models {
			ind := md.Indicators
			rate := m.rates[b.Name+"/"+md.Name]
			jpt := "-"
			if md.JoulesPerToken > 0 {
				jpt = m.energy(md.JoulesPerToken)
			}
			add(row{
				group: b.Name,
				cells: []string{"  " + md.Name, "", m.energy(md.EnergyJ), jpt, fmtRate(rate), fmtCO2e(ind.CO2eG, m.o.Raw), fmtCost(ind.Cost, currency, m.o.Raw)},
				keys:  []float64{0, 0, md.EnergyJ, md.JoulesPerToken, rate, ind.CO2eG, deref(ind.Cost)},
				csv:   []string{b.Name + "/" + md.Name, "", num(md.EnergyJ), num(md.JoulesPerToken), num(rate), num(ind.CO2eG), numPtr(ind.Cost)},
			})
		}
	}
	return rs
}

func (m *model) consumerRows() []row {
	var rs []row
	for _, u := range m.consumers.Rows {
		if u.Consumer == "" {
			continue
		}
		var jpt float64
		if u.GeneratedTokens > 0 {
			jpt = u.EnergyJ / float64(u.GeneratedTokens)
		}
		rs = append(rs, row{
			group: u.Consumer + "/" + u.Backend,
			cells: []string{u.Consumer, u.Backend, m.energy(u.EnergyJ), fmt.Sprintf("%d / %d", u.PromptTokens, u.GeneratedTokens), m.energy(jpt)},
			keys:  []float64{0, 0, u.EnergyJ, float64(u.GeneratedTokens), jpt},
			csv:   []string{u.Consumer, u.Backend, num(u.EnergyJ), fmt.Sprint(u.GeneratedTokens), num(jpt)},
		})
	}
	return rs
}

func (m *model) sourceRows() []row {
	var rs []row
	for _, c := range m.status.Channels {
		args := make([]any, 0, 2*len(c.ReasonArgs))
		for k, v := range c.ReasonArgs {
			args = append(args, k, v)
		}
		reason := m.l.T(c.Reason, args...)
		domain := m.l.T("domain." + string(c.Domain))
		rs = append(rs, row{
			group: c.ID,
			cells: []string{c.ID, domain, m.l.T("quality." + string(c.Quality)), reason},
			keys:  []float64{0, 0, 0, 0},
			csv:   []string{c.ID, string(c.Domain), string(c.Quality), reason},
		})
	}
	return rs
}

func (m *model) nodeCSV() [][]string {
	domains := m.domainOrder()
	header := []string{"time"}
	for _, d := range domains {
		header = append(header, d+"_w")
	}
	out := [][]string{header}
	for _, p := range m.live.Points {
		r := []string{p.Time.UTC().Format(time.RFC3339Nano)}
		for _, d := range domains {
			r = append(r, num(p.PowerW[apiDomain(d)]))
		}
		out = append(out, r)
	}
	return out
}

// View implements tea.Model.
func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "greenlens top"
	return v
}

func (m *model) render() string {
	if m.width < MinWidth || m.height < MinHeight {
		msg := m.l.T("tui.too_small", "width", MinWidth, "height", MinHeight)
		if !m.t.unicode {
			msg = asciiFold(msg)
		}
		return msg
	}
	var body string
	switch {
	case m.showHelp:
		body = m.helpView()
	case m.err != nil && !m.have:
		body = m.t.critical.Render(m.l.T("tui.unreachable")) + "\n\n" + m.problem(m.err)
	case !m.have:
		body = m.t.muted.Render(m.l.T("tui.waiting"))
	case m.view == viewNode:
		body = m.nodeView()
	default:
		body = m.tableView()
	}
	lines := []string{m.header(), m.summary()}
	content := strings.Split(body, "\n")
	room := m.height - 4
	for len(content) < room {
		content = append(content, "")
	}
	lines = append(lines, content[:room]...)
	lines = append(lines, m.statusLine(), m.footer())
	for i, l := range lines {
		if !m.t.unicode {
			l = asciiFold(l)
		}
		lines[i] = clip(l, m.width)
	}
	return strings.Join(lines, "\n")
}

func (m *model) header() string {
	parts := []string{m.t.title.Render("GreenLens")}
	for i := range viewCount {
		label := fmt.Sprintf(" %d %s ", i+1, m.l.T("tui.view."+viewKeys[i]))
		if i == m.view {
			parts = append(parts, m.t.tabOn.Render(label))
		} else {
			parts = append(parts, m.t.tab.Render(label))
		}
	}
	left := strings.Join(parts, " ")
	right := ""
	if m.paused {
		right = m.t.load.Render(m.l.T("tui.paused"))
	} else if m.have {
		right = m.t.muted.Render(m.at.Format("15:04:05"))
	}
	return spread(left, right, m.width)
}

// summary is the total power of the node and the state of the agent.
func (m *model) summary() string {
	if !m.have {
		return ""
	}
	var total float64
	for _, d := range m.status.Domains {
		if d.Level == "component" {
			total += d.PowerW
		}
	}
	q := m.l.T("quality.measured")
	if m.status.Estimated {
		q = m.l.T("quality.estimated")
	}
	s := m.l.T("tui.summary", "power", m.t.load.Render(fmtPower(total, m.o.Raw)), "quality", q,
		"backends", len(m.status.Backends), "version", m.status.Version)
	if m.err != nil {
		s += "  " + m.t.critical.Render(m.l.T("tui.stale"))
	}
	return s
}

func (m *model) statusLine() string {
	switch {
	case m.filtering:
		return m.t.keyHint.Render("/") + m.filter.view(m.t)
	case m.filter.value() != "":
		return m.t.muted.Render(m.l.T("tui.filtered", "filter", m.filter.value()))
	}
	return m.t.muted.Render(m.message)
}

func (m *model) footer() string {
	self := ""
	if m.selfRSS > 0 {
		self = m.t.muted.Render(m.l.T("tui.self", "cpu", fmt.Sprintf("%.1f", m.selfCPU), "memory", fmt.Sprintf("%.0f", float64(m.selfRSS)/(1<<20))))
	}
	m.help.SetWidth(max(10, m.width-lipgloss.Width(self)-2))
	return spread(m.help.View(m.keys), self, m.width)
}

func (m *model) tableView() string {
	if len(m.tbl.Rows()) == 0 {
		return m.t.muted.Render(m.l.T("tui.empty." + viewKeys[m.view]))
	}
	out := m.tbl.View()
	if ps := m.status.Problems; m.view == viewSources && len(ps) > 0 {
		out += "\n\n" + m.t.value.Render(m.l.T("tui.problems", "count", len(ps)))
		for _, p := range ps[:min(len(ps), maxProblems)] {
			e := p.Error()
			out += "\n" + m.t.critical.Render(string(e.Code)) + " " + m.t.muted.Render(e.Message(m.l))
		}
	}
	return out
}

func (m *model) problem(err error) string {
	return m.t.muted.Render(err.Error())
}

func (m *model) helpView() string {
	l := m.l
	var b strings.Builder
	b.WriteString(m.t.title.Render(l.T("tui.help.title")) + "\n\n")
	b.WriteString(wrapText(l.T("tui.help."+viewKeys[m.view]), m.width-2) + "\n\n")
	m.help.ShowAll = true
	b.WriteString(m.help.View(m.keys))
	m.help.ShowAll = false
	b.WriteString("\n\n" + m.t.muted.Render(l.T("tui.help.close")))
	return b.String()
}
