package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/input"
	"github.com/asyncpoint/logi/internal/search"
)

func (m *Model) onKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	m.msg = ""
	switch m.ov {
	case ovPrompt:
		return m.promptKey(k, key)
	case ovPalette:
		return m.paletteKey(k, key)
	case ovPicker:
		return m.pickerKey(k, key)
	case ovDetail:
		return m.detailKey(key)
	case ovHelp:
		switch key {
		case "esc", "q", "?", "enter":
			m.ov = ovNone
		case "down", "j", "pgdown", "space":
			m.helpScroll += 3
		case "up", "k", "pgup":
			m.helpScroll = max(0, m.helpScroll-3)
		case "ctrl+c":
			return m.quit()
		}
		return m, nil
	}
	if act, ok := m.cmds.Lookup(key); ok {
		return m.run(act)
	}
	return m, nil
}

// run executes an Action. Both key bindings and the palette land here.
func (m *Model) run(a app.Action) (tea.Model, tea.Cmd) {
	if name, ok := strings.CutPrefix(string(a), app.ToggleColumnPrefix); ok {
		m.toggleColumn(name)
		return m, nil
	}
	switch a {
	case app.ActionColumnPrev:
		m.moveCol(-1)
	case app.ActionColumnNext:
		m.moveCol(1)
	case app.ActionHideColumn:
		m.hideCurrent()
	case app.ActionShowColumns:
		m.showAllColumns()
	case app.ActionPickFile:
		if m.pickDir == "" {
			m.msg = "not browsing a directory (open one with ctrl+p)"
		} else {
			m.openPicker()
		}
	case app.ActionDown:
		m.move(1)
	case app.ActionUp:
		m.move(-1)
	case app.ActionPageDown:
		m.move(m.bodyHeight() - 1)
	case app.ActionPageUp:
		m.move(-(m.bodyHeight() - 1))
	case app.ActionTop:
		m.goTop()
	case app.ActionBottom:
		m.goBottom()
	case app.ActionSearch:
		m.openPrompt(pSearch, m.searchReq.Query)
	case app.ActionSearchNext:
		m.repeatSearch(app.Forward)
	case app.ActionSearchPrev:
		m.repeatSearch(app.Backward)
	case app.ActionNextError:
		return m, m.navigateSeverity(engine.SevError, app.Forward, "next error")
	case app.ActionPrevWarning:
		return m, m.navigateSeverity(engine.SevWarn, app.Backward, "previous warning")
	case app.ActionJumpRecord:
		m.openPrompt(pJumpRecord, "")
	case app.ActionToggleBookmark:
		m.toggleBookmark()
	case app.ActionNextBookmark:
		m.navigateBookmark(1)
	case app.ActionPrevBookmark:
		m.navigateBookmark(-1)
	case app.ActionSelectRange:
		if m.cursorID == 0 {
			m.msg = "no record selected"
		} else if m.selectionStart == 0 {
			m.selectionStart, m.selectionEnd = m.cursorID, m.cursorID
			m.msg = "selection started"
		} else {
			m.selectionEnd = m.cursorID
			m.msg = fmt.Sprintf("selected records %d–%d", min(m.selectionStart, m.selectionEnd), max(m.selectionStart, m.selectionEnd))
		}
	case app.ActionClearSelection:
		m.selectionStart, m.selectionEnd = 0, 0
		m.msg = "selection cleared"
	case app.ActionCopy:
		return m, m.copyCurrent()
	case app.ActionFilter:
		m.openPrompt(pFilter, m.sess.Status().Filter)
	case app.ActionClearFilter:
		m.clearFilter()
	case app.ActionInspect:
		m.openDetail(m.cursorID)
	case app.ActionFollow:
		if m.sess.Mode() != app.ModeLive {
			m.msg = "follow is only available in live mode (-f)"
		} else if m.follow = !m.follow; m.follow {
			m.setCursorRow(m.sess.ViewLen() - 1)
			m.msg = "following"
		} else {
			m.msg = "paused"
		}
	case app.ActionQuickOpen:
		m.openPrompt(pOpen, "")
	case app.ActionPalette:
		m.ov, m.palQuery, m.palSel = ovPalette, "", 0
	case app.ActionHelp:
		m.ov, m.helpScroll = ovHelp, 0
	case app.ActionQuit:
		return m.quit()
	}
	return m, nil
}

func (m *Model) repeatSearch(dir app.Direction) {
	if m.searchReq.Query == "" {
		m.msg = "no active search (press / to search)"
		return
	}
	m.startSearch(m.cursorID, dir, false)
}

func (m *Model) clearFilter() {
	_ = m.sess.SetFilter("")
	m.filterStatus.Query = ""
	m.msg = "filter cleared"
	if m.cursorID != 0 {
		m.ensureVisible()
	}
}

func (m *Model) openPrompt(k promptKind, initial string) {
	m.ov, m.pk, m.input, m.perr = ovPrompt, k, initial, ""
	m.replace = k == pSearch && initial != "" // like a find box: typing replaces the old query
	m.anchorID = m.cursorID
	if k == pSearch {
		m.sstate = ""
	}
}

// textField returns the text being edited by the active overlay.
func (m *Model) textField() *string {
	switch m.ov {
	case ovPalette:
		return &m.palQuery
	case ovPicker:
		return &m.pickQuery
	}
	return &m.input
}

func (m *Model) resetSelection() {
	switch m.ov {
	case ovPalette:
		m.palSel = 0
	case ovPicker:
		m.pickSel = 0
	}
}

func (m *Model) typeText(s string) {
	if m.ov == ovPrompt && m.replace {
		m.input, m.replace = "", false
	}
	f := m.textField()
	*f += s
	m.resetSelection()
	if m.ov == ovPrompt {
		m.perr = ""
		if m.pk == pSearch {
			m.searchReq.Query = m.input
			m.startSearch(m.anchorID, app.Forward, true) // search-as-you-type from the anchor
		}
	}
}

func (m *Model) backspace() {
	m.replace = false
	f := m.textField()
	_, n := utf8.DecodeLastRuneInString(*f)
	*f = (*f)[:len(*f)-n]
	m.resetSelection()
	if m.ov == ovPrompt {
		m.perr = ""
		if m.pk == pSearch {
			m.searchReq.Query = m.input
			m.startSearch(m.anchorID, app.Forward, true)
		}
	}
}

// editKey handles the common line-editing keys; it reports whether it consumed k.
func (m *Model) editKey(k tea.KeyPressMsg, key string) bool {
	f := m.textField()
	switch key {
	case "backspace":
		m.backspace()
	case "ctrl+u":
		*f = ""
		m.perr = ""
		m.resetSelection()
	case "ctrl+w":
		t := strings.TrimRight(*f, " ")
		if i := strings.LastIndexByte(t, ' '); i >= 0 {
			*f = t[:i+1]
		} else {
			*f = ""
		}
		m.resetSelection()
	default:
		if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			m.typeText(k.Text)
			return true
		}
		return false
	}
	return true
}

func (m *Model) promptKey(k tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		if m.pk == pSearch && m.anchorID != 0 {
			m.cursorID = m.anchorID
			m.ensureVisible()
		}
		m.ov = ovNone
		return m, nil
	case "enter":
		return m.submitPrompt()
	case "alt+r":
		if m.pk == pSearch {
			m.toggleMode(search.Regex)
		}
		return m, nil
	case "alt+f":
		if m.pk == pSearch {
			m.toggleMode(search.Field)
		}
		return m, nil
	case "alt+c":
		if m.pk == pSearch {
			m.searchReq.CaseSensitive = !m.searchReq.CaseSensitive
			m.startSearch(m.anchorID, app.Forward, true)
		}
		return m, nil
	}
	m.editKey(k, key)
	return m, nil
}

func (m *Model) toggleMode(mode search.Mode) {
	if m.searchReq.Mode == mode {
		m.searchReq.Mode = search.Literal
	} else {
		m.searchReq.Mode = mode
	}
	m.startSearch(m.anchorID, app.Forward, true)
}

func (m *Model) submitPrompt() (tea.Model, tea.Cmd) {
	switch m.pk {
	case pSearch:
		m.ov = ovNone // the match found while typing stays selected
	case pFilter:
		if err := m.sess.SetFilter(strings.TrimSpace(m.input)); err != nil {
			m.perr = err.Error()
			return m, nil
		}
		m.ov = ovNone
		m.topID = 0
		m.cursorID = 0
		m.msg = "filter applied"
	case pJumpRecord:
		id, err := strconv.ParseUint(strings.TrimSpace(m.input), 10, 64)
		if err != nil || id == 0 {
			m.perr = "enter a positive record ID"
			return m, nil
		}
		if _, ok := m.sess.Record(id); !ok {
			m.perr = fmt.Sprintf("record ID %d is unavailable", id)
			return m, nil
		}
		row := m.sess.RowOf(id)
		actual, ok := m.sess.IDAt(row)
		if !ok || actual != id {
			m.perr = fmt.Sprintf("record ID %d is not in the current view", id)
			return m, nil
		}
		m.cursorID, m.topID, m.ov = id, 0, ovNone
		m.follow = false
		m.ensureVisible()
		m.msg = fmt.Sprintf("jumped to record %d", id)
	case pOpen:
		path := strings.TrimSpace(m.input)
		if path == "" {
			m.ov = ovNone
			return m, nil
		}
		return m.openFile(path)
	}
	return m, nil
}

// openFile swaps the session for a new file.
func (m *Model) openFile(path string) (tea.Model, tea.Cmd) {
	cfg := m.cfg
	cfg.Path, cfg.Filter = path, ""
	if fromPicker := m.ov == ovPicker; fromPicker {
		cfg.Glob = m.cfg.Glob
	}
	ns, err := app.NewSession(cfg)
	if err != nil {
		m.perr = err.Error()
		return m, nil
	}
	ns.Start(m.ctx)
	old := m.sess
	old.Close()
	m.sess, m.cfg = ns, cfg
	m.bookmarks = make(map[uint64]struct{})
	m.selectionStart, m.selectionEnd = 0, 0
	m.lastRecordCount = 0
	m.navGeneration++ // discard any in-flight severity-navigation result
	m.sgen, m.sPending, m.sfoundID = 0, false, 0
	m.ov, m.cursorID, m.topID = ovNone, 0, 0
	m.follow = ns.Mode() == app.ModeLive
	m.initColumns()
	if d := ns.PickerDir(); d != "" { // a directory was opened: keep it for Ctrl+O and show the picker
		m.pickDir = d
		m.openPicker()
	}
	m.sstate, m.searchReq.Query, m.filterStatus = "", "", app.FilterStatus{}
	m.msg = "opened " + path
	return m, waitUpdate(ns, m.ctx)
}

func (m *Model) paletteKey(k tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	items := m.paletteItems()
	switch key {
	case "esc", "ctrl+c":
		m.ov = ovNone
	case "up", "ctrl+p":
		m.palSel = max(0, m.palSel-1)
	case "down", "ctrl+n":
		m.palSel = min(len(items)-1, m.palSel+1)
	case "enter":
		m.ov = ovNone
		if m.palSel >= 0 && m.palSel < len(items) {
			return m.run(items[m.palSel].Action)
		}
	default:
		m.editKey(k, key)
	}
	return m, nil
}

func (m *Model) paletteItems() []app.Command {
	q := strings.ToLower(strings.TrimSpace(m.palQuery))
	var out []app.Command
	all := m.cmds.Palette()
	for _, c := range m.cols { // one toggle per column of the current log format
		all = append(all, app.Command{Action: app.Action(app.ToggleColumnPrefix + c.name),
			Title: "Toggle column: " + c.title, Description: "Hide or show the " + c.name + " column", InPalette: true})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Title < all[j].Title })
	for _, c := range all {
		if q == "" || strings.Contains(strings.ToLower(c.Title+" "+c.Description), q) {
			out = append(out, c)
		}
	}
	return out
}

// ---- file picker ------------------------------------------------------------

func (m *Model) openPicker() {
	items, err := input.ListFiles(m.pickDir, m.pickerFilter())
	if err != nil {
		m.msg = "cannot list " + m.pickDir + ": " + err.Error()
		return
	}
	m.pickItems, m.pickQuery, m.pickSel, m.ov = items, "", 0, ovPicker
}

func (m *Model) pickerFilter() *input.FileFilter {
	f, _ := input.NewFileFilter(m.cfg.Glob)
	return f
}

func (m *Model) filteredPick() []input.FileEntry {
	q := strings.ToLower(strings.TrimSpace(m.pickQuery))
	if q == "" {
		return m.pickItems
	}
	var out []input.FileEntry
	for _, it := range m.pickItems {
		if strings.Contains(strings.ToLower(it.Name), q) {
			out = append(out, it)
		}
	}
	return out
}

func (m *Model) pickerKey(k tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	items := m.filteredPick()
	switch key {
	case "esc", "ctrl+c":
		m.ov = ovNone
	case "up", "ctrl+p":
		m.pickSel = max(0, m.pickSel-1)
	case "down", "ctrl+n":
		m.pickSel = min(len(items)-1, m.pickSel+1)
	case "pgdown":
		m.pickSel = min(len(items)-1, m.pickSel+m.bodyHeight()-2)
	case "pgup":
		m.pickSel = max(0, m.pickSel-(m.bodyHeight()-2))
	case "enter":
		if m.pickSel >= 0 && m.pickSel < len(items) {
			return m.openFile(items[m.pickSel].Path)
		}
	default:
		m.editKey(k, key)
	}
	return m, nil
}
