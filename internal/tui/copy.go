package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/asyncpoint/logi/internal/app"
	"github.com/asyncpoint/logi/internal/engine"
)

// maxCopyBytes bounds one clipboard payload. Text reaches the clipboard through
// an OSC 52 escape sequence, and many terminals silently drop larger ones, so a
// selection is cut at a record boundary below this size and the status line
// says so rather than copying something half-delivered.
const maxCopyBytes = 512 << 10

// copyMsg carries text collected off the UI loop to Update, which hands it to
// the terminal.
type copyMsg struct {
	text      string
	records   int
	missing   int  // records that left the store before they could be read
	truncated bool // stopped at maxCopyBytes
	err       error
}

// copyTarget describes what a copy covers: the rows of the selection, or the
// single record under the cursor (or open in the detail view).
type copyTarget struct {
	first, last int    // view rows, inclusive; used when id == 0
	id          uint64 // a single record
}

// copyCurrent copies the selected range if there is one, otherwise the record
// under the cursor.
func (m *Model) copyCurrent() tea.Cmd {
	if m.selectionStart != 0 && m.selectionEnd != 0 {
		a, b := m.exactViewRow(m.selectionStart), m.exactViewRow(m.selectionEnd)
		if a < 0 || b < 0 {
			m.msg = "selection is not in the current view"
			return nil
		}
		return m.startCopy(copyTarget{first: min(a, b), last: max(a, b)})
	}
	if m.cursorID == 0 {
		m.msg = "no record to copy"
		return nil
	}
	return m.startCopy(copyTarget{id: m.cursorID})
}

// copyRecord copies one record by ID (the detail view).
func (m *Model) copyRecord(id uint64) tea.Cmd {
	if id == 0 {
		m.msg = "no record to copy"
		return nil
	}
	return m.startCopy(copyTarget{id: id})
}

func (m *Model) startCopy(t copyTarget) tea.Cmd {
	sess := m.sess
	return func() tea.Msg { return collectCopy(sess, t) }
}

// collectCopy reads the raw text of the target records. It runs outside the UI
// loop because a static file is read from disk and a selection can be long.
func collectCopy(sess *app.Session, t copyTarget) copyMsg {
	var (
		out strings.Builder
		res copyMsg
	)
	add := func(id uint64) bool {
		rec, ok := sess.Record(id)
		if !ok {
			res.missing++
			return true
		}
		raw, err := sess.Raw(&rec)
		if err != nil {
			res.err = err
			return false
		}
		// The clipboard gets the text as written, minus terminal colour codes.
		text := strings.TrimRight(engine.StripANSI(raw), "\r\n")
		if res.records > 0 && out.Len()+1+len(text) > maxCopyBytes {
			res.truncated = true
			return false
		}
		if res.records > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(text)
		res.records++
		return true
	}
	if t.id != 0 {
		add(t.id)
	} else {
		for row := t.first; row <= t.last; row++ {
			id, ok := sess.IDAt(row)
			if !ok {
				res.missing++
				continue
			}
			if !add(id) {
				break
			}
		}
	}
	res.text = out.String()
	if len(res.text) > maxCopyBytes { // a single record can exceed the cap
		res.text, res.truncated = res.text[:maxCopyBytes], true
	}
	return res
}

// onCopy completes a copy: it sends the text to the terminal clipboard and
// reports what happened.
func (m *Model) onCopy(c copyMsg) tea.Cmd {
	switch {
	case c.err != nil:
		m.msg = "copy failed: " + c.err.Error()
		return nil
	case c.records == 0:
		m.msg = "nothing to copy (the selected records are no longer available)"
		return nil
	}
	m.selectionStart, m.selectionEnd = 0, 0
	what := "record"
	if c.records != 1 {
		what = "records"
	}
	m.msg = fmt.Sprintf("copied %d %s (%s)", c.records, what, byteSize(len(c.text)))
	if c.truncated {
		m.msg += fmt.Sprintf(" — stopped at the %s clipboard limit", byteSize(maxCopyBytes))
	}
	if c.missing > 0 {
		m.msg += fmt.Sprintf(" — %d no longer available", c.missing)
	}
	return tea.SetClipboard(c.text)
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
