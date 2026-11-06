// Package tui is the Bubble Tea front end. It renders only the visible rows,
// pulling records from app.Session on demand.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/config"
	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/input"
	"github.com/asyncpoint/logi/internal/search"
)

type overlay int

const (
	ovNone overlay = iota
	ovDetail
	ovHelp
	ovPalette
	ovPrompt
	ovPicker
)

type promptKind int

const (
	pSearch promptKind = iota
	pFilter
	pOpen
	pJumpRecord
)

type updateMsg app.Update

type severityNavMsg struct {
	id         uint64
	generation uint64
	label      string
}

// Model is the root Bubble Tea model.
type Model struct {
	ctx  context.Context
	sess *app.Session
	cmds *app.Commands
	cfg  config.Config

	w, h int

	cursorID       uint64
	topID          uint64
	follow         bool
	bookmarks      map[uint64]struct{}
	selectionStart uint64
	selectionEnd   uint64

	ov overlay

	// prompt (search / filter / open)
	pk        promptKind
	input     string
	replace   bool // next typed text replaces the pre-filled search query
	perr      string
	anchorID  uint64 // where the cursor was when the search prompt opened
	searchReq search.Request

	// search state
	sgen          uint64
	sstate        string
	sfoundID      uint64
	sPending      bool
	navGeneration uint64

	// detail
	detailID     uint64
	detailScroll int
	detailLines  []string
	detailWidth  int

	// palette
	palQuery string
	palSel   int

	// columns (dynamic: they come from the detected log format)
	cols             []colSpec
	visibleColsCache []int
	curCol           int
	layoutName       string
	baseHidden       map[string]bool // hidden state before the user's own toggles
	userHidden       map[string]bool // explicit user toggles; they survive layout rebuilds

	// file picker (directories)
	pickDir   string
	pickItems []input.FileEntry
	pickQuery string
	pickSel   int

	// help
	helpScroll int

	// status
	msg             string
	pendingBottom   bool
	filterStatus    app.FilterStatus
	lastRecordCount int
	fatal           error
	quitting        bool
}

// New builds the model for a started session.
func New(ctx context.Context, sess *app.Session, cfg config.Config) *Model {
	cmds := app.DefaultCommands()
	for name, keys := range cfg.Keys {
		cmds.Rebind(app.Action(name), keys)
	}
	m := &Model{
		ctx: ctx, sess: sess, cmds: cmds, cfg: cfg, w: 100, h: 30,
		follow:    sess.Mode() == app.ModeLive,
		bookmarks: make(map[uint64]struct{}),
		searchReq: search.Request{Mode: search.ParseMode(cfg.SearchMode)},
	}
	m.initColumns()
	if d := sess.PickerDir(); d != "" {
		m.pickDir = d
		m.openPicker() // static directory: let the user choose a file right away
	}
	return m
}

func waitUpdate(s *app.Session, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case u := <-s.Updates():
			return updateMsg(u)
		case <-ctx.Done():
			return nil
		}
	}
}

func (m *Model) Init() tea.Cmd { return waitUpdate(m.sess, m.ctx) }

// bodyHeight is the number of record rows on screen.
func (m *Model) bodyHeight() int {
	// The normal view no longer needs a separate shortcut/help line; those
	// hints live in the status bar. Prompts and transient messages still use
	// their own line when present.
	overhead := 3 // title, column header, status
	if m.promptLine() != "" {
		overhead++
	}
	if h := m.h - overhead; h > 1 {
		return h
	}
	return 1
}

// ---- navigation -----------------------------------------------------------

func (m *Model) cursorRow() int {
	if m.cursorID == 0 {
		return m.sess.RowOf(1)
	}
	return m.sess.RowOf(m.cursorID)
}

func (m *Model) setCursorRow(row int) {
	n := m.sess.ViewLen()
	if n == 0 {
		m.cursorID = 0
		return
	}
	row = max(0, min(row, n-1))
	if id, ok := m.sess.IDAt(row); ok {
		m.cursorID = id
	}
	m.ensureVisible()
}

func (m *Model) move(delta int) {
	row := m.cursorRow()
	if row < 0 {
		return
	}
	if delta < 0 {
		m.follow = false
	}
	m.setCursorRow(row + delta)
	if m.sess.Mode() == app.ModeLive && m.cursorRow() == m.sess.ViewLen()-1 && delta > 0 {
		m.follow = true // scrolled back to the end: resume following
	}
}

func (m *Model) ensureVisible() {
	n := m.sess.ViewLen()
	if n == 0 {
		return
	}
	h := m.bodyHeight()
	top := m.sess.RowOf(max(m.topID, 1))
	if m.topID == 0 || top < 0 {
		top = 0
	}
	row := m.cursorRow()
	switch {
	case row < top:
		top = row
	case row >= top+h:
		top = row - h + 1
	}
	if top > n-h {
		top = max(0, n-h)
	}
	if id, ok := m.sess.IDAt(top); ok {
		m.topID = id
	}
}

func (m *Model) goTop() {
	m.follow = false
	m.pendingBottom = false
	m.setCursorRow(0)
}

func (m *Model) goBottom() {
	st := m.sess.Status()
	if st.Mode == app.ModeStatic && !st.IndexDone {
		m.pendingBottom = true
		m.msg = "indexing… will jump to the end when done"
		return
	}
	m.pendingBottom = false
	if st.Mode == app.ModeLive {
		m.follow = true
	}
	m.setCursorRow(m.sess.ViewLen() - 1)
}

// navigateSeverity scans the current filtered view off the UI goroutine. It
// uses stable record IDs so changes in visible row positions cannot retarget it.
func (m *Model) navigateSeverity(min engine.Severity, dir app.Direction, label string) tea.Cmd {
	startID := m.cursorID
	m.navGeneration++
	gen := m.navGeneration
	sess, ctx := m.sess, m.ctx
	return func() tea.Msg {
		n := sess.ViewLen()
		row := -1
		if startID != 0 {
			row = sess.RowOf(startID)
			if row >= 0 {
				if actual, ok := sess.IDAt(row); !ok || actual != startID {
					row--
				}
			}
		}
		step := 1
		if dir == app.Backward {
			step = -1
		}
		if row < 0 {
			if dir == app.Forward {
				row = -1
			} else {
				row = n
			}
		}
		for i := row + step; i >= 0 && i < n; i += step {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			r, ok := sess.ViewAt(i)
			if !ok {
				continue
			}
			if r.Severity >= min {
				return severityNavMsg{id: r.ID, generation: gen, label: label}
			}
		}
		return severityNavMsg{generation: gen, label: label}
	}
}

// selected reports whether id belongs to the current ID-based selection.
func (m *Model) exactViewRow(id uint64) int {
	if id == 0 {
		return -1
	}
	row := m.sess.RowOf(id)
	if row < 0 {
		return -1
	}
	actual, ok := m.sess.IDAt(row)
	if !ok || actual != id {
		return -1
	}
	return row
}

func (m *Model) selected(id uint64) bool {
	if m.selectionStart == 0 || m.selectionEnd == 0 {
		return false
	}
	// Selection follows the current view's row order, not numeric ID ranges.
	// RowOf may return the nearest row for a missing ID, so verify exact IDs.
	row := m.exactViewRow(id)
	start, end := m.exactViewRow(m.selectionStart), m.exactViewRow(m.selectionEnd)
	if row < 0 || start < 0 || end < 0 {
		return false
	}
	if start > end {
		start, end = end, start
	}
	return row >= start && row <= end
}

// pruneNavigationState removes references to records that have left the store.
// It runs only when the held-record count shrinks, avoiding a per-event scan.
func (m *Model) pruneNavigationState() {
	count := m.sess.Status().Records
	if count >= m.lastRecordCount {
		m.lastRecordCount = count
		return
	}
	for id := range m.bookmarks {
		if _, ok := m.sess.Record(id); !ok {
			delete(m.bookmarks, id)
		}
	}
	if (m.selectionStart != 0 && func() bool { _, ok := m.sess.Record(m.selectionStart); return !ok }()) ||
		(m.selectionEnd != 0 && func() bool { _, ok := m.sess.Record(m.selectionEnd); return !ok }()) {
		m.selectionStart, m.selectionEnd = 0, 0
		m.msg = "selection cleared because a selected record was evicted"
	}
	if m.sfoundID != 0 {
		if _, ok := m.sess.Record(m.sfoundID); !ok {
			m.sfoundID = 0
			m.sstate = "search match expired from the live buffer"
		}
	}
	m.lastRecordCount = count
}

func (m *Model) toggleBookmark() {
	if m.cursorID == 0 {
		m.msg = "no record selected"
		return
	}
	if _, ok := m.bookmarks[m.cursorID]; ok {
		delete(m.bookmarks, m.cursorID)
		m.msg = fmt.Sprintf("removed bookmark %d", m.cursorID)
		return
	}
	if len(m.bookmarks) >= 10000 {
		m.msg = "bookmark limit reached (10,000)"
		return
	}
	m.bookmarks[m.cursorID] = struct{}{}
	m.msg = fmt.Sprintf("bookmarked record %d", m.cursorID)
}

func (m *Model) navigateBookmark(dir int) {
	n := m.sess.ViewLen()
	row := m.cursorRow()
	if row < 0 {
		row = -1
		if dir < 0 {
			row = n
		}
	}
	for i := row + dir; i >= 0 && i < n; i += dir {
		id, ok := m.sess.IDAt(i)
		if !ok {
			continue
		}
		if _, ok := m.bookmarks[id]; ok {
			m.setCursorRow(i)
			m.follow = false
			m.msg = fmt.Sprintf("bookmark %d", id)
			return
		}
	}
	m.msg = "no further bookmark in the current view"
}

// ---- search ---------------------------------------------------------------

func (m *Model) startSearch(from uint64, dir app.Direction, inclusive bool) {
	if m.searchReq.Query == "" {
		return
	}
	gen, err := m.sess.Search(m.searchReq, from, dir, inclusive)
	if err != nil {
		m.sstate, m.sPending = "invalid: "+err.Error(), false
		return
	}
	m.sgen, m.sPending, m.sstate = gen, true, "searching…"
}

func (m *Model) onSearchResult(r *app.SearchResult) {
	if !m.sPending || r.Gen != m.sgen {
		return // stale or belongs to a search that is no longer active
	}
	m.sPending = false
	if r.Err != nil {
		m.sstate = "search failed: " + r.Err.Error()
		return
	}
	if !r.Found {
		m.sfoundID = 0
		m.sstate = fmt.Sprintf("no match for %q", r.Query)
		return
	}
	id := r.ID
	if !r.Position.IsZero() {
		if _, ok := m.sess.ResolvePosition(r.Position); !ok {
			m.sstate = "match expired before it could be selected"
			return
		}
		id = r.Position.RecordID
	}
	if _, ok := m.sess.Record(id); !ok {
		m.sfoundID = 0
		m.sstate = "match expired before it could be selected"
		return
	}
	if m.exactViewRow(id) < 0 {
		m.sfoundID = 0
		m.sstate = fmt.Sprintf("match #%d is no longer in the current view", id)
		return
	}
	m.sfoundID = id
	m.sstate = fmt.Sprintf("match at #%d", id)
	if r.Wrapped {
		m.sstate += " (wrapped)"
	}
	m.follow = false
	m.cursorID = id
	m.topID = 0
	m.ensureVisible()
	// center-ish: keep some context above the match
	if row := m.cursorRow(); row >= 0 {
		if id, ok := m.sess.IDAt(max(0, row-m.bodyHeight()/3)); ok {
			m.topID = id
		}
		m.ensureVisible()
	}
}

// ---- update ---------------------------------------------------------------

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.detailLines = nil
		m.ensureVisible()
		return m, nil
	case severityNavMsg:
		if msg.generation == m.navGeneration {
			if msg.id == 0 {
				m.msg = "no " + msg.label + " found in the current view"
			} else {
				m.cursorID = msg.id
				m.follow = false
				m.ensureVisible()
				m.msg = "jumped to " + msg.label
			}
		}
		return m, nil
	case copyMsg:
		return m, m.onCopy(msg)
	case updateMsg:
		m.onUpdate(app.Update(msg))
		return m, waitUpdate(m.sess, m.ctx)
	case tea.KeyPressMsg:
		return m.onKey(msg)
	case tea.PasteMsg:
		if m.ov == ovPrompt || m.ov == ovPalette {
			m.typeText(strings.ReplaceAll(msg.Content, "\n", " "))
		}
		return m, nil
	case tea.MouseWheelMsg:
		if m.ov == ovNone {
			switch msg.Button {
			case tea.MouseWheelUp:
				m.move(-3)
			case tea.MouseWheelDown:
				m.move(3)
			}
		} else if m.ov == ovDetail {
			if msg.Button == tea.MouseWheelUp {
				m.detailScroll = max(0, m.detailScroll-3)
			} else {
				m.detailScroll += 3
			}
		}
		return m, nil
	case tea.MouseClickMsg:
		if m.ov != ovNone || msg.Button != tea.MouseLeft {
			return m, nil
		}
		switch {
		case msg.Y == 1: // column header: select that column
			if c, ok := m.colAt(msg.X); ok {
				m.curCol = c
			}
		case msg.Y >= 2 && msg.Y <= m.bodyHeight()+1:
			top := m.sess.RowOf(max(m.topID, 1))
			m.follow = false
			m.setCursorRow(top + msg.Y - 2)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) onUpdate(u app.Update) {
	m.pruneNavigationState()
	if u.Fatal != nil {
		m.fatal = u.Fatal
	}
	if u.Filter != nil {
		m.filterStatus = *u.Filter
	}
	if u.Search != nil {
		m.onSearchResult(u.Search)
	}
	if u.State != nil && u.State.Msg != "" {
		m.msg = u.State.Msg
	}
	if m.pendingBottom && u.Index != nil && u.Index.Done {
		m.goBottom()
	}
	m.refreshLayout()
	if m.cursorID == 0 && m.sess.ViewLen() > 0 {
		m.setCursorRow(0)
		if m.follow {
			m.setCursorRow(m.sess.ViewLen() - 1)
		}
	}
	if m.follow && m.sess.Mode() == app.ModeLive {
		m.setCursorRow(m.sess.ViewLen() - 1)
	} else if m.cursorID != 0 {
		// eviction or a rebuilt filter may have removed the cursor row
		if row := m.cursorRow(); row >= 0 {
			if id, ok := m.sess.IDAt(row); ok {
				m.cursorID = id
			}
		}
		m.ensureVisible()
	}
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	return m, tea.Quit
}

// Session returns the session currently shown (it changes when a file is opened
// from inside the UI), so the caller can release it on exit.
func (m *Model) Session() *app.Session { return m.sess }
