package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/filter"
	"github.com/asyncpoint/logi/internal/search"
)

func (m *Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	// A live parser can become known between update notifications. Refreshing
	// here prevents the status header and the column layout from disagreeing
	// when the next render observes the newly detected profile first.
	m.refreshLayout()
	lines := []string{m.headerLine()}
	body := m.bodyHeight()
	switch m.ov {
	case ovDetail:
		lines = append(lines, m.detailBody(body+1)...)
	case ovHelp:
		lines = append(lines, m.helpBody(body+1)...)
	case ovPalette:
		lines = append(lines, m.paletteBody(body+1)...)
	case ovPicker:
		lines = append(lines, m.pickerBody(body+1)...)
	default:
		lines = append(lines, m.colHeaderLine())
		lines = append(lines, m.rowLines(body)...)
	}
	if prompt := m.promptLine(); prompt != "" {
		lines = append(lines, prompt)
	}
	lines = append(lines, m.statusLine())
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "logi — " + m.sourceTitle(m.sess.Status())
	return v
}

// sourceTitle names what is being shown: a file, a directory, or a directory
// plus the file currently followed.
func (m *Model) sourceTitle(st app.Status) string { return sanitize(m.rawSourceTitle(st)) }

func (m *Model) rawSourceTitle(st app.Status) string {
	if st.Dir == "" {
		return filepath.Base(st.Path)
	}
	name := filepath.Base(st.Dir) + "/"
	switch {
	case st.Mode == app.ModeStatic:
		return name + " → " + filepath.Base(st.Path)
	case st.DirMode == "all":
		return fmt.Sprintf("%s (%d files)", name, len(st.Files))
	case len(st.Files) == 1:
		return name + " → " + st.Files[0]
	}
	return name
}

func (m *Model) headerLine() string {
	st := m.sess.Status()
	badge := stBadge.Render(" STATIC ")
	if st.Mode == app.ModeLive {
		label := " LIVE "
		if !m.follow {
			label = " LIVE ‖ paused "
		}
		badge = stBadgeLv.Render(label)
	}
	line := badge + fmt.Sprintf(" logi  %s  %s", m.sourceTitle(st), stDim.Render("· "+st.Parser))
	if label := m.cursorSourceLabel(); label != "" {
		line += "  " + label
	}
	if pad := m.w - ansi.StringWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return ansi.Truncate(line, m.w, "")
}

// gutterOn reports whether rows carry the file-colour bar in the gutter: always
// in "on" mode, never in "off", and in "auto" (the default) whenever the source
// column is hidden, where the bar stands in for it. Multi-file views only.
func (m *Model) gutterOn() bool {
	if !m.sess.MultiSource() {
		return false
	}
	switch m.cfg.SourceGutter {
	case "on":
		return true
	case "off":
		return false
	}
	return m.isHidden("source")
}

// cursorSourceLabel names, in that file's own colour, the file the record under
// the cursor came from (multi-file view only). It shows the full name in the
// header, so the source column can stay narrow or be hidden altogether.
func (m *Model) cursorSourceLabel() string {
	if !m.sess.MultiSource() || m.cursorID == 0 {
		return ""
	}
	rec, ok := m.sess.Record(m.cursorID)
	if !ok || rec.SourceID == "" {
		return ""
	}
	slot := m.sess.SourceSlot(rec.SourceID)
	label := sanitize(rec.SourceID)
	if m.cfg.SourceMarkers && m.sess.FollowedFileCount() > len(sourcePalette) {
		label = strings.TrimLeft(sourceMarkerCell(slot), " ") + label
	}
	return sourceStyleFor(slot).Render(label)
}

// colHeaderLine renders the column titles; the current column is highlighted
// and a hint shows when the user has hidden columns.
func (m *Model) colHeaderLine() string {
	idw := m.idWidth()
	var b strings.Builder
	used := 0
	for _, i := range m.visibleCols() {
		c := m.cols[i]
		w := m.colWidth(i, idw)
		if c.kind == kMessage {
			w = max(1, m.w-used)
		}
		title := c.title
		if c.kind == kID {
			title = fmt.Sprintf("%*s ", idw-1, "#")
		}
		cell := ansi.Truncate(title, w, "…")
		cell += strings.Repeat(" ", max(0, w-ansi.StringWidth(cell)))
		if i == m.curCol {
			b.WriteString(stColSel.Render(cell))
		} else {
			b.WriteString(stColHead.Render(cell))
		}
		used += w
	}
	line := b.String()
	if n := m.hiddenByUser(); n > 0 {
		hint := fmt.Sprintf(" %d hidden · X shows all ", n)
		if w := ansi.StringWidth(hint); m.w > w+12 {
			// the message header is padded to the full width; trim that padding for the hint
			line = ansi.Truncate(line, m.w-w, "") + stDim.Render(hint)
		}
	}
	return line
}

func sanitize(s string) string { return engine.SanitizeText(s) }

func (m *Model) rowLines(h int) []string {
	n := m.sess.ViewLen()
	out := make([]string, 0, h)
	if n == 0 {
		msg := "  (no records yet)"
		st := m.sess.Status()
		switch {
		case st.Mode == app.ModeStatic && !st.IndexDone:
			msg = "  indexing…"
		case st.Filter != "":
			msg = "  no records match the filter"
		case st.Source.String() == "waiting":
			msg = "  " + st.SourceMsg
		}
		out = append(out, stDim.Render(msg))
		for len(out) < h {
			out = append(out, "")
		}
		return out
	}
	top := m.sess.RowOf(max(m.topID, 1))
	if m.topID == 0 || top < 0 {
		top = 0
	}
	if m.follow && m.sess.Mode() == app.ModeLive {
		top = max(0, n-h)
	}
	matcher := m.sess.Matcher()
	idw := m.idWidth()
	fixedWidth := m.fixedWidth(idw)
	for row := top; row < top+h && row < n; row++ {
		rec, ok := m.sess.ViewAt(row)
		if !ok {
			out = append(out, "")
			continue
		}
		out = append(out, m.renderRow(&rec, rec.ID == m.cursorID, idw, fixedWidth, matcher))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

func padTrunc(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

type segment struct {
	text string
	st   lipgloss.Style
}

// highlightInto appends text as one or more segments in base style, splitting
// out any part of it that matcher finds so it renders in stHit instead. text is
// matched exactly as it will be displayed (already truncated/padded), so a
// match that exists in the record but not in this particular rendering of this
// particular column (cut off by truncation, or simply elsewhere) correctly
// yields no highlight here — search still finds the record via the full raw
// entry (search.Matcher.Match), this only decides what lights up on screen.
func highlightInto(segs []segment, text string, base lipgloss.Style, matcher *search.Matcher) []segment {
	if matcher == nil || text == "" {
		return append(segs, segment{text, base})
	}
	pos := 0
	for _, sp := range matcher.Spans(text) {
		st, en := min(sp[0], len(text)), min(sp[1], len(text))
		if st >= len(text) || st < pos || en <= st {
			continue
		}
		segs = append(segs, segment{text[pos:st], base}, segment{text[st:en], stHit})
		pos = en
	}
	return append(segs, segment{text[pos:], base})
}

func (m *Model) renderRow(rec *engine.LogRecord, cursor bool, idw, fixedWidth int, matcher *search.Matcher) string {
	th := themeFor(rec.Severity)
	var segs []segment
	for _, ci := range m.visibleCols() {
		c := m.cols[ci]
		switch c.kind {
		case kID:
			// Keep bookmark and selection indicators independent so neither hides
			// the other when a bookmarked record is inside the selected range.
			bookmark, selection := " ", " "
			if _, ok := m.bookmarks[rec.ID]; ok {
				bookmark = "★"
			}
			if m.selected(rec.ID) {
				selection = "◆"
			}
			if m.gutterOn() {
				// The bookmark cell carries the file's colour: a bar normally, the
				// star on bookmarked rows, so bookmarks lose nothing.
				_, marked := m.bookmarks[rec.ID]
				slot := m.sess.SourceSlot(rec.SourceID)
				markers := m.cfg.SourceMarkers && m.sess.FollowedFileCount() > len(sourcePalette)
				segs = append(segs, segment{gutterGlyph(slot, marked, markers), sourceStyleFor(slot)}, segment{fmt.Sprintf("%s%*d ", selection, idw-3, rec.ID), th.meta})
			} else {
				segs = append(segs, segment{fmt.Sprintf("%s%s%*d ", bookmark, selection, idw-3, rec.ID), th.meta})
			}
		case kTime:
			ts := strings.Repeat(" ", timeColW-1)
			if !rec.Timestamp.IsZero() {
				ts = rec.Timestamp.Format("15:04:05.000")
			}
			segs = highlightInto(segs, ts, th.meta, matcher)
			segs = append(segs, segment{" ", th.meta})
		case kLevel:
			segs = highlightInto(segs, sevLabel(rec.Severity), th.label, matcher)
			segs = append(segs, segment{" ", th.label})
		case kSource:
			slot := m.sess.SourceSlot(rec.SourceID)
			nameW := sourceColW - 1
			if m.cfg.SourceMarkers && m.sess.FollowedFileCount() > len(sourcePalette) {
				// Opt-in (--source-markers): with more files than colours, repeats get a
				// shape marker (two cells).
				segs = append(segs, segment{sourceMarkerCell(slot), sourceStyleFor(slot)})
				nameW -= 2
			}
			segs = highlightInto(segs, padTrunc(sanitize(rec.SourceID), nameW), sourceStyleFor(slot), matcher)
			segs = append(segs, segment{" ", th.meta})
		case kField:
			v, _ := filter.FieldValue(rec, c.name)
			st := th.meta
			if c.name == "event" { // the event name reads like part of the message
				st = th.msg
			}
			segs = highlightInto(segs, padTrunc(sanitize(v), c.width), st, matcher)
			segs = append(segs, segment{" ", st})
		case kMessage:
			first, extra := engine.FirstLine(rec.Message)
			first = sanitize(first)
			suffix := ""
			if extra > 0 {
				suffix = fmt.Sprintf(" ⏎+%d", extra)
			}
			if rec.Truncated {
				suffix += " ✂"
			}
			avail := max(1, m.w-fixedWidth-ansi.StringWidth(suffix))
			msg := ansi.Truncate(first, avail, "…")
			segs = highlightInto(segs, msg, th.msg, matcher)
			if suffix != "" {
				segs = append(segs, segment{suffix, th.meta})
			}
		}
	}
	var b strings.Builder
	width := 0
	for _, sg := range segs {
		if sg.text == "" {
			continue
		}
		st := sg.st
		if cursor && sg.st.GetBackground() == nil {
			st = st.Reverse(true)
		} else if cursor {
			st = st.Reverse(true)
		}
		b.WriteString(st.Render(sg.text))
		width += ansi.StringWidth(sg.text)
	}
	if cursor && width < m.w { // extend the cursor highlight to the full width
		b.WriteString(stCursor.Render(strings.Repeat(" ", m.w-width)))
	}
	return b.String()
}

func (m *Model) promptLine() string {
	switch {
	case m.ov == ovPrompt:
		var label string
		switch m.pk {
		case pSearch:
			flags := m.searchReq.Mode.String()
			if m.searchReq.CaseSensitive {
				flags += ",Aa"
			}
			label = fmt.Sprintf("Search [%s]", flags)
		case pFilter:
			label = "Filter"
		case pOpen:
			label = "Open file or directory"
		case pJumpRecord:
			label = "Jump to record ID"
		}
		line := stPrompt.Render(label+": ") + m.input + "▏"
		if m.pk == pSearch && m.sstate != "" {
			line += "  " + stDim.Render(m.sstate)
		}
		if m.perr != "" {
			line += "  " + stErrText.Render(m.perr)
		}
		return ansi.Truncate(line, m.w, "…")
	case m.fatal != nil:
		return stErrText.Render("error: " + m.fatal.Error())
	case m.msg != "":
		return m.msg
	case m.sstate != "" && m.sess.Matcher() != nil:
		return stDim.Render(fmt.Sprintf("search %q: %s  (n/N next/prev)", m.searchReq.Query, m.sstate))
	}
	return ""
}

// statusHints are the shortcut hints of the status bar, in display order. drop
// says in which order they are given up when the terminal is too narrow (0 = never).
var statusHints = []struct {
	text string
	drop int
}{
	{"? help", 0}, {"/ search", 6}, {"f filter", 5}, {"h/l col", 1}, {"x hide", 2}, {"enter inspect", 3}, {": cmds", 4}, {"q quit", 0},
}

// fitHints joins as many hints as fit in budget columns, dropping the least
// useful first. With no room even for "? help  q quit" it returns "".
func fitHints(budget int) string {
	keep := make([]bool, len(statusHints))
	for i := range keep {
		keep[i] = true
	}
	build := func() string {
		var parts []string
		for i, h := range statusHints {
			if keep[i] {
				parts = append(parts, h.text)
			}
		}
		return strings.Join(parts, "  ")
	}
	for ansi.StringWidth(build()) > budget {
		next, nextDrop := -1, 1<<30
		for i, h := range statusHints {
			if keep[i] && h.drop > 0 && h.drop < nextDrop {
				next, nextDrop = i, h.drop
			}
		}
		if next < 0 {
			return ""
		}
		keep[next] = false
	}
	return build()
}

func (m *Model) statusLine() string {
	st := m.sess.Status()
	var left []string
	left = append(left, positionText(m.cursorRow(), st.ViewRows))
	if st.Filter != "" {
		f := "filter: " + st.Filter
		if st.Filtering {
			f += " …"
		}
		if st.Capped {
			f += " (capped)"
		}
		left = append(left, f)
	}
	l := " " + strings.Join(left, "  │  ")

	// right side: (plain text, style) parts, so counters can be coloured badges
	type part struct {
		text string
		st   lipgloss.Style
	}
	var right []part
	plain := func(t string) { right = append(right, part{t, stStatus}) }
	// Severity counters, the same badges in both modes. C covers the two
	// highest-severity buckets (CRITICAL and FATAL), while E and W stay separate,
	// so fatal records are visible without another badge. Live mode counts the
	// retained records; static mode counts the whole file in the background.
	badges := func(s engine.StatsSnapshot) {
		critical := s.BySeverity[engine.SevCritical] + s.BySeverity[engine.SevFatal]
		if critical > 0 {
			right = append(right, part{fmt.Sprintf(" C:%s ", commas(critical)), badgeStyles[engine.SevCritical]})
		}
		for _, sev := range []engine.Severity{engine.SevError, engine.SevWarn} {
			if n := s.BySeverity[sev]; n > 0 {
				right = append(right, part{fmt.Sprintf(" %c:%s ", sev.String()[0], commas(n)), badgeStyles[sev]})
			}
		}
	}
	if st.Mode == app.ModeStatic {
		if st.Counts {
			badges(st.Stats)
			if st.Counting {
				right = append(right, part{" counting… ", stDim})
			}
		}
		if st.IndexDone {
			plain(" " + humanBytes(st.FileSize))
		} else if st.FileSize > 0 {
			plain(fmt.Sprintf(" indexing %d%%", st.IndexBytes*100/st.FileSize))
		}
	} else {
		badges(st.Stats)
		plain(fmt.Sprintf(" buf %s/%s", commas(int64(st.Records)), commas(int64(st.BufferCap))))
		if st.Stats.Evicted > 0 {
			plain(fmt.Sprintf("  dropped %s", commas(st.Stats.Evicted)))
		}
		if st.Source.String() != "running" {
			plain("  " + st.Source.String())
		}
	}
	rw := 1
	for _, p := range right {
		rw += ansi.StringWidth(p.text)
	}
	if m.ov == ovNone {
		// Shortcut hints take whatever room is left, least useful first to go, so a
		// narrow terminal loses hints whole instead of cutting one in half. "?" and
		// "q" always stay.
		if h := fitHints(m.w - rw - ansi.StringWidth(l) - 5); h != "" {
			l += "  │  " + h
		}
	}
	gap := m.w - ansi.StringWidth(l) - rw
	if gap < 1 {
		gap = 1
		l = ansi.Truncate(l, max(1, m.w-rw-1), "…")
	}
	var b strings.Builder
	b.WriteString(stStatus.Render(l + strings.Repeat(" ", gap)))
	for _, p := range right {
		b.WriteString(p.st.Render(p.text))
	}
	b.WriteString(stStatus.Render(" "))
	return b.String()
}

// ---- detail ---------------------------------------------------------------

func (m *Model) openDetail(id uint64) {
	if id == 0 {
		return
	}
	m.ov, m.detailID, m.detailScroll, m.detailLines = ovDetail, id, 0, nil
}

func (m *Model) buildDetail() {
	rec, ok := m.sess.Record(m.detailID)
	if !ok {
		m.detailLines = []string{stDim.Render("(record is no longer available — it was evicted from the buffer)")}
		return
	}
	w := max(20, m.w-2)
	var l []string
	add := func(s string) { l = append(l, strings.Split(ansi.Hardwrap(s, w, true), "\n")...) }
	head := fmt.Sprintf("#%d", rec.ID)
	if !rec.Timestamp.IsZero() {
		head += "  " + rec.Timestamp.Format("2006-01-02 15:04:05.000")
	}
	if rec.Severity != engine.SevUnknown {
		// The header is short enough to render as one styled span. Do not pass
		// it through ansi.Hardwrap: that can split SGR sequences and expose
		// values such as "[32mINFO[m" in the detail view.
		l = append(l, stOverlay.Render(head)+"  "+sevStyle(rec.Severity).Render(rec.Severity.String()))
	} else {
		add(stOverlay.Render(head))
	}
	meta := fmt.Sprintf("source %s · offset %d · %d bytes", sanitize(rec.SourceID), rec.Raw.Offset, rec.Raw.Length)
	if rec.LineStart > 0 {
		meta += fmt.Sprintf(" · lines %d–%d", rec.LineStart, rec.LineEnd)
	}
	if rec.Truncated {
		meta += " · TRUNCATED by size limit"
	}
	if rec.ParseErr {
		meta += " · parse error (best-effort fields)"
	}
	add(stDim.Render(meta))

	raw, err := m.sess.Raw(&rec)
	if err != nil {
		add(stErrText.Render("cannot read record: " + err.Error()))
		m.detailLines, m.detailWidth = l, m.w
		return
	}
	trim := strings.TrimSpace(raw)

	// JSON is presented as JSON because its native structure is more useful
	// than flattening it into the record-list columns. This also handles JSON
	// arrays and nested objects without losing their hierarchy.
	if strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[") {
		var ind bytes.Buffer
		if jerr := json.Indent(&ind, []byte(trim), "", "  "); jerr == nil {
			l = append(l, "")
			add(stDim.Render("JSON"))
			for _, ln := range strings.Split(ind.String(), "\n") {
				add(sanitizeKeepTabs(ln))
			}
			m.detailLines, m.detailWidth = l, m.w
			return
		}
	}

	// Non-JSON records are rendered as a field/value table. The same layout
	// used by the main TUI determines the column names and ordering, so an
	// NCache record is inspected using its real NCache column names rather than
	// exposing the raw source line. Any parser-produced fields not declared by
	// the layout are appended in stable alphabetical order.
	l = append(l, "")
	l = append(l, stDim.Render("FIELDS"))
	for _, line := range m.detailTable(&rec, w) {
		l = append(l, line)
	}
	m.detailLines, m.detailWidth = l, m.w
}

func (m *Model) detailTable(rec *engine.LogRecord, w int) []string {
	type field struct{ name, value string }
	fields := make([]field, 0, len(m.sess.Layout())+4)
	seen := make(map[string]bool)

	valueFor := func(name string) (string, bool) {
		switch name {
		case "id":
			return fmt.Sprintf("%d", rec.ID), true
		case "time":
			if rec.Timestamp.IsZero() {
				return "", false
			}
			return rec.Timestamp.Format("2006-01-02 15:04:05.000000000"), true
		case "level":
			if rec.Severity == engine.SevUnknown {
				return "", false
			}
			return rec.Severity.String(), true
		case "source":
			return sanitize(rec.SourceID), rec.SourceID != ""
		case "message":
			return rec.Message, rec.Message != ""
		default:
			return filter.FieldValue(rec, name)
		}
	}

	// Keep the message last. It is normally the largest field, so putting it
	// after the compact metadata makes the detail view much easier to scan.
	var messageField *field
	for _, c := range m.sess.Layout() {
		if v, ok := valueFor(c.Name); ok {
			f := field{c.Title, v}
			if c.Name == "message" {
				messageField = &f
				seen[c.Name] = true
				continue
			}
			fields = append(fields, f)
			seen[c.Name] = true
		}
	}
	if _, ok := seen["id"]; !ok {
		fields = append([]field{{"#", fmt.Sprintf("%d", rec.ID)}}, fields...)
	}

	// Fields produced by a parser but not declared by its display layout (for
	// example an exception extracted from a multiline NCache record) still need
	// to be inspectable. Sort them so the overlay is deterministic.
	var extra []string
	for _, fv := range rec.Fields {
		if !seen[fv.Key] && fv.Key != "message" {
			extra = append(extra, fv.Key)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		fields = append(fields, field{k, rec.Fields.GetValue(k)})
	}
	if messageField != nil {
		fields = append(fields, *messageField)
	}

	if len(fields) == 0 {
		return []string{stDim.Render("  (no structured fields available)")}
	}

	labelW := 0
	for _, f := range fields {
		labelW = max(labelW, ansi.StringWidth(f.name))
	}
	labelW = min(labelW, max(8, w/3))

	var out []string
	for _, f := range fields {
		value := sanitizeKeepTabs(f.value)
		parts := strings.Split(value, "\n")
		prefix := fmt.Sprintf("  %-*s  ", labelW, ansi.Truncate(f.name, labelW, "…"))
		severityField := strings.EqualFold(f.name, "level") || strings.EqualFold(f.name, "severity")

		for i, part := range parts {
			linePrefix := prefix
			if i > 0 {
				linePrefix = strings.Repeat(" ", ansi.StringWidth(prefix))
			}

			// Render the severity value before wrapping rather than injecting ANSI
			// sequences into an already-wrapped line. Bubble Tea/Lip Gloss can then
			// carry the style through the final View without exposing the escape
			// sequence itself as literal text (e.g. "[32mINFO[m").
			valueText := part
			if severityField && part != "" {
				valueText = sevStyle(rec.Severity).Render(part)
			}

			line := linePrefix + valueText
			// Severity values are short and should never need wrapping; keep them
			// intact so their ANSI styling remains a single span. Other fields use
			// the normal ANSI-aware wrapper.
			if severityField {
				out = append(out, line)
				continue
			}
			out = append(out, strings.Split(ansi.Hardwrap(line, w, true), "\n")...)
		}
	}
	return out
}

func sanitizeKeepTabs(s string) string { return engine.SanitizeKeepTabs(s) }

func (m *Model) detailBody(h int) []string {
	if m.detailLines == nil || m.detailWidth != m.w {
		m.buildDetail()
	}
	maxScroll := max(0, len(m.detailLines)-h)
	m.detailScroll = min(m.detailScroll, maxScroll)
	out := append([]string(nil), m.detailLines[m.detailScroll:min(len(m.detailLines), m.detailScroll+h)]...)
	for len(out) < h {
		out = append(out, "")
	}
	if h > 0 {
		out[h-1] = stDim.Render(fmt.Sprintf("── esc close · ↑/↓ scroll · ←/→ prev/next record · y copy · %d/%d ──", min(m.detailScroll+h, len(m.detailLines)), len(m.detailLines)))
	}
	return out
}

func (m *Model) detailKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q", "enter", "i":
		m.ov = ovNone
	case "ctrl+c":
		return m.quit()
	case "down", "j":
		m.detailScroll++
	case "up", "k":
		m.detailScroll = max(0, m.detailScroll-1)
	case "pgdown", "space":
		m.detailScroll += m.bodyHeight() - 1
	case "pgup", "b":
		m.detailScroll = max(0, m.detailScroll-(m.bodyHeight()-1))
	case "home", "g":
		m.detailScroll = 0
	case "y":
		return m, m.copyRecord(m.detailID)
	case "right", "l", "]":
		m.stepDetail(1)
	case "left", "h", "[":
		m.stepDetail(-1)
	}
	return m, nil
}

func (m *Model) stepDetail(d int) {
	row := m.sess.RowOf(m.detailID) + d
	if id, ok := m.sess.IDAt(row); ok {
		m.detailID, m.detailScroll, m.detailLines = id, 0, nil
		m.cursorID = id
		m.ensureVisible()
	}
}

// ---- help & palette ---------------------------------------------------------

func (m *Model) helpBody(h int) []string {
	var l []string
	l = append(l, stOverlay.Render("Keys & commands")+stDim.Render("   ↑/↓ scroll · esc close"), "")

	// Both the key-combo and title columns are sized to the widest entry
	// actually in use (defaults or a config rebind), not a fixed guess: a
	// fixed width breaks alignment — and visually crowds the next column —
	// the moment any entry is longer than it (e.g. "pgdown / space / ctrl+d",
	// or the title "Pick file from directory"). The *Max constants guard
	// against a single pathological custom rebinding blowing out the layout.
	const keyColMax, titleColMax = 32, 28
	type helpRow struct{ keys, title, desc string }
	var rows []helpRow
	keyW, titleW := 0, 0
	for _, c := range m.cmds.All() {
		if len(c.Keys) == 0 {
			continue
		}
		k, title := strings.Join(c.Keys, " / "), c.Title
		if len(k) > keyColMax {
			k = k[:keyColMax-1] + "…"
		}
		if len(title) > titleColMax {
			title = title[:titleColMax-1] + "…"
		}
		keyW, titleW = max(keyW, len(k)), max(titleW, len(title))
		rows = append(rows, helpRow{k, title, c.Description})
	}
	for _, r := range rows {
		l = append(l, fmt.Sprintf("  %-*s %-*s %s", keyW, r.keys, titleW, r.title, stDim.Render(r.desc)))
	}
	l = append(l, "", stOverlay.Render("Log levels")+stDim.Render("  (case-insensitive; every spelling normalises to one of these)"))
	for _, sev := range engine.AllSeverities() {
		th := themeFor(sev)
		l = append(l, "  "+th.label.Render(sevLabel(sev))+" "+th.msg.Render(fmt.Sprintf("%-14s", "sample message"))+" "+stDim.Render(levelAliases[sev]))
	}
	l = append(l, "  "+stDim.Render("(no level found: shown uncoloured; single letters only as [E] (W) or glog E0920)"))
	l = append(l, "",
		stOverlay.Render("Filter syntax"),
		"  severity:error   severity:>=warn   severity:warn,error   source:name   message:\"text\"",
		"  key=value (structured field)   re:pattern (regex)   \"exact phrase\"   word   -negate",
		"  since:15m   until:14:30   since:\"2026-10-15 10:30\"   (time window; 15m = the last 15 minutes of the log)",
		"",
		stOverlay.Render("Columns"),
		"  h/l or ←/→ or Tab select a column (highlighted in the header) · x hides it · X shows all",
		"  palette: \"Toggle column: …\" restores or hides a specific column",
		"",
		stOverlay.Render("Directories"),
		"  logi -f dir/  follows the newest file and rolls over · --dir-mode all follows every file",
		"  logi dir/  opens the newest file · Ctrl+O lists the directory to pick another",
		"",
		stOverlay.Render("Search"),
		"  type to search live · Enter keep result · Esc restore position · n / N next / previous",
		"  Alt+R regex · Alt+C case-sensitive · Alt+F field search (key=value)",
		"",
		stDim.Render("ctrl+shift+p needs a terminal with the kitty keyboard protocol; ':' works everywhere."),
		stDim.Render("esc / ? closes this help"))
	maxScroll := max(0, len(l)-h)
	m.helpScroll = min(m.helpScroll, maxScroll)
	out := l[m.helpScroll:min(len(l), m.helpScroll+h)]
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

func (m *Model) paletteBody(h int) []string {
	items := m.paletteItems()
	m.palSel = max(0, min(m.palSel, len(items)-1))
	out := []string{stPrompt.Render("Command: ") + m.palQuery + "▏"}
	for i, c := range items {
		if len(out) >= h {
			break
		}
		keys := strings.Join(c.Keys, " / ")
		line := fmt.Sprintf("  %-20s %s", c.Title, stDim.Render(keys))
		if i == m.palSel {
			line = stSelected.Render(ansi.Truncate(fmt.Sprintf("▸ %-20s %s", c.Title, keys), m.w, ""))
		}
		out = append(out, line)
	}
	if len(items) == 0 {
		out = append(out, stDim.Render("  no matching command"))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

func (m *Model) pickerBody(h int) []string {
	items := m.filteredPick()
	m.pickSel = max(0, min(m.pickSel, len(items)-1))
	cur := m.sess.Status().Path
	out := []string{stPrompt.Render("Pick a file in "+m.pickDir+": ") + m.pickQuery + "▏" +
		stDim.Render(fmt.Sprintf("   %d files · ↑/↓ enter esc · type to filter", len(items)))}
	start := 0
	if m.pickSel >= h-1 {
		start = m.pickSel - (h - 2)
	}
	for i := start; i < len(items) && len(out) < h; i++ {
		it := items[i]
		mark := "  "
		if it.Path == cur {
			mark = "● "
		}
		line := fmt.Sprintf("%s%-*s %10s  %s", mark, max(10, m.w-36), ansi.Truncate(sanitize(it.Name), max(10, m.w-36), "…"),
			humanBytes(it.Size), it.ModTime.Format("2006-01-02 15:04:05"))
		if i == m.pickSel {
			line = stSelected.Render(ansi.Truncate(line, m.w, ""))
		}
		out = append(out, line)
	}
	if len(items) == 0 {
		out = append(out, stDim.Render("  no matching files"))
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

// positionText renders "row/total". In live mode the total and the cursor are
// read at slightly different instants while records keep arriving, so the total
// is never allowed to be smaller than the position shown.
func positionText(row, total int) string {
	if row < 0 {
		return "0/0"
	}
	return fmt.Sprintf("%s/%s", commas(int64(row+1)), commas(int64(max(total, row+1))))
}
