package tui

import (
	"fmt"
	"strings"

	"github.com/asyncpoint/logi/internal/parser"
)

// colKind separates the built-in columns from columns that show a record field
// produced by the format's parser (thread, logger, event, ...).
type colKind int

const (
	kID colKind = iota
	kTime
	kLevel
	kSource
	kMessage
	kField
)

// colSpec is one column of the record list. The set and order come from the
// detected log format (parser.Profile.Columns), so a tabular file such as an
// NCache log shows the columns of its own header.
type colSpec struct {
	name, title string
	kind        colKind
	width       int // fixed width for field columns
	hidden      bool
}

const (
	timeColW    = 13 // "15:04:05.000 "
	levelColW   = 6  // "ERROR "
	sourceColW  = 16
	fieldColW   = 16 // default for field columns
	fieldColMax = 40
)

func kindOf(name string) colKind {
	switch name {
	case "id":
		return kID
	case "time":
		return kTime
	case "level":
		return kLevel
	case "source":
		return kSource
	case "message":
		return kMessage
	}
	return kField
}

// initColumns builds the columns from the format's layout and applies, in
// order: the format's own defaults, the multi-file source rule, the config
// (--hide-columns / --show-columns) and finally the user's explicit toggles.
func (m *Model) initColumns() {
	m.visibleColsCache = nil
	keep := ""
	if m.curCol >= 0 && m.curCol < len(m.cols) {
		keep = m.cols[m.curCol].name
	}
	m.layoutName = m.sess.ProfileName()
	m.cols = []colSpec{{name: "id", title: "#", kind: kID}}
	for _, sp := range m.sess.Layout() {
		c := colSpec{name: sp.Name, title: sp.Title, kind: kindOf(sp.Name), width: sp.Width, hidden: sp.Hidden}
		if c.title == "" {
			c.title = c.name
		}
		if c.kind == kField {
			if c.width <= 0 {
				c.width = fieldColW
			}
			c.width = min(c.width, fieldColMax)
		}
		if c.kind == kSource {
			// Hidden by default: in a multi-file view the header names the cursor's
			// file and the gutter bar shows each row's file colour, so the column's
			// width goes to the message. --show-columns source, X (show all) or the
			// column toggle brings it back.
			c.hidden = true
		}
		m.cols = append(m.cols, c)
	}
	var unknown []string
	apply := func(names []string, hidden bool) {
		for _, n := range names {
			if i := m.colIndex(n); i >= 0 {
				m.cols[i].hidden = hidden
			} else if n != "" {
				unknown = append(unknown, n)
			}
		}
	}
	apply(m.cfg.HideColumns, true)
	apply(m.cfg.ShowColumns, false)
	m.baseHidden = map[string]bool{}
	for _, c := range m.cols {
		m.baseHidden[c.name] = c.hidden
	}
	for name, h := range m.userHidden { // the user's own toggles win and survive rebuilds
		if i := m.colIndex(name); i >= 0 {
			m.cols[i].hidden = h
		}
	}
	if len(m.visibleCols()) == 0 {
		if i := m.colIndex("message"); i >= 0 {
			m.cols[i].hidden = false
		} else {
			m.cols[0].hidden = false
		}
		m.visibleColsCache = nil
	}
	m.curCol = m.visibleCols()[0]
	if i := m.colIndex(keep); i >= 0 && !m.cols[i].hidden {
		m.curCol = i
	}
	if len(unknown) > 0 && m.layoutName != "detecting…" {
		m.msg = fmt.Sprintf("no such column %q for this log format (columns: %s)", unknown[0], m.columnNames())
	}
}

func (m *Model) columnNames() string {
	names := make([]string, len(m.cols))
	for i, c := range m.cols {
		names[i] = c.name
	}
	return strings.Join(names, ", ")
}

func (m *Model) colIndex(name string) int {
	for i, c := range m.cols {
		if c.name == name {
			return i
		}
	}
	return -1
}

func (m *Model) isHidden(name string) bool {
	i := m.colIndex(name)
	return i >= 0 && m.cols[i].hidden
}

// visibleCols returns the indices of the visible columns, in display order.
func (m *Model) visibleCols() []int {
	if m.visibleColsCache != nil {
		return m.visibleColsCache
	}
	out := make([]int, 0, len(m.cols))
	for i, c := range m.cols {
		if !c.hidden {
			out = append(out, i)
		}
	}
	m.visibleColsCache = out
	return out
}

// hiddenByUser counts columns hidden since startup (the format's defaults and
// the configured layout are not "hidden by the user" and must not nag).
func (m *Model) hiddenByUser() int {
	n := 0
	for _, c := range m.cols {
		if c.hidden && !m.baseHidden[c.name] {
			n++
		}
	}
	return n
}

func (m *Model) setHidden(i int, hidden bool) {
	m.cols[i].hidden = hidden
	m.visibleColsCache = nil
	if m.userHidden == nil {
		m.userHidden = map[string]bool{}
	}
	m.userHidden[m.cols[i].name] = hidden
}

// moveCol moves the current-column focus across the visible columns.
func (m *Model) moveCol(d int) {
	vis := m.visibleCols()
	pos := 0
	for i, c := range vis {
		if c == m.curCol {
			pos = i
		}
	}
	m.curCol = vis[((pos+d)%len(vis)+len(vis))%len(vis)]
	m.msg = "column: " + m.cols[m.curCol].name
}

// hideCurrent hides the focused column and moves the focus to a neighbour.
func (m *Model) hideCurrent() {
	vis := m.visibleCols()
	if len(vis) <= 1 {
		m.msg = "at least one column must stay visible"
		return
	}
	hidden, pos := m.curCol, 0
	for i, c := range vis {
		if c == hidden {
			pos = i
		}
	}
	m.setHidden(hidden, true)
	vis = m.visibleCols()
	m.curCol = vis[min(pos, len(vis)-1)]
	m.msg = fmt.Sprintf("hid column %q — x hides another, X shows all", m.cols[hidden].name)
}

func (m *Model) showAllColumns() {
	for i := range m.cols {
		m.setHidden(i, false)
	}
	m.msg = "all columns visible"
}

func (m *Model) toggleColumn(name string) {
	i := m.colIndex(name)
	if i < 0 {
		return
	}
	if !m.cols[i].hidden {
		if len(m.visibleCols()) <= 1 {
			m.msg = "at least one column must stay visible"
			return
		}
		m.setHidden(i, true)
		if m.curCol == i {
			m.curCol = m.visibleCols()[0]
		}
		m.msg = fmt.Sprintf("hid column %q", name)
		return
	}
	m.setHidden(i, false)
	m.curCol = i
	m.msg = fmt.Sprintf("showing column %q", name)
}

// refreshLayout rebuilds the columns when the detected format or its declared
// layout changes. Checking the layout as well as the profile name matters for
// live sources: the status/profile publication and the first UI update can be
// observed at slightly different times, and the profile name alone is not a
// sufficient guard against a stale column set.
func (m *Model) refreshLayout() {
	layout := m.sess.Layout()
	if m.sess.ProfileName() != m.layoutName || !m.matchesLayout(layout) {
		m.initColumns()
	}
}

// matchesLayout compares the parser-declared column shape with the current
// columns. Visibility is intentionally excluded because config and user
// toggles are layered on top of the parser layout.
func (m *Model) matchesLayout(layout []parser.ColumnSpec) bool {
	if len(m.cols) != len(layout)+1 || len(m.cols) == 0 || m.cols[0].kind != kID {
		return false
	}
	for i, spec := range layout {
		c := m.cols[i+1]
		title := spec.Title
		if title == "" {
			title = spec.Name
		}
		if c.name != spec.Name || c.title != title || c.kind != kindOf(spec.Name) {
			return false
		}
		if c.kind == kField {
			width := spec.Width
			if width <= 0 {
				width = fieldColW
			}
			if c.width != min(width, fieldColMax) {
				return false
			}
		}
	}
	return true
}

// idWidth is the width of the id column including its trailing space.
func (m *Model) idWidth() int {
	return len(fmt.Sprint(m.sess.Status().Records)) + 2
}

// colWidth returns the fixed width of a column; the message column takes the rest.
func (m *Model) colWidth(i, idw int) int {
	switch c := m.cols[i]; c.kind {
	case kID:
		return idw
	case kTime:
		return timeColW
	case kLevel:
		return levelColW
	case kSource:
		return sourceColW
	case kField:
		return c.width + 1
	}
	return 0
}

// fixedWidth is the total width of the visible non-message columns.
func (m *Model) fixedWidth(idw int) int {
	w := 0
	for _, i := range m.visibleCols() {
		w += m.colWidth(i, idw)
	}
	return w
}

// colAt maps a screen x position to a visible column (for header clicks).
func (m *Model) colAt(x int) (int, bool) {
	idw := m.idWidth()
	pos := 0
	for _, i := range m.visibleCols() {
		w := m.colWidth(i, idw)
		if m.cols[i].kind == kMessage {
			w = max(1, m.w-pos)
		}
		if x >= pos && x < pos+w {
			return i, true
		}
		pos += w
	}
	return 0, false
}

var _ = parser.DefaultColumns
