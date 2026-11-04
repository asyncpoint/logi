package app

import "sort"

// Action is a named UI capability. Keys, the command palette and the help
// overlay all resolve to Actions.
type Action string

const (
	ActionDown           Action = "down"
	ActionUp             Action = "up"
	ActionPageDown       Action = "page_down"
	ActionPageUp         Action = "page_up"
	ActionTop            Action = "top"
	ActionBottom         Action = "bottom"
	ActionSearch         Action = "search"
	ActionSearchNext     Action = "search_next"
	ActionSearchPrev     Action = "search_prev"
	ActionNextError      Action = "next_error"
	ActionPrevError      Action = "previous_error"
	ActionNextWarning    Action = "next_warning"
	ActionPrevWarning    Action = "previous_warning"
	ActionJumpRecord     Action = "jump_record"
	ActionToggleBookmark Action = "toggle_bookmark"
	ActionNextBookmark   Action = "next_bookmark"
	ActionPrevBookmark   Action = "previous_bookmark"
	ActionSelectRange    Action = "select_range"
	ActionClearSelection Action = "clear_selection"
	ActionCopy           Action = "copy"
	ActionFilter         Action = "filter"
	ActionClearFilter    Action = "clear_filter"
	ActionInspect        Action = "inspect"
	ActionFollow         Action = "follow"
	ActionQuickOpen      Action = "quick_open"
	ActionPalette        Action = "palette"
	ActionHelp           Action = "help"
	ActionClose          Action = "close"
	ActionQuit           Action = "quit"

	ActionColumnPrev  Action = "column_prev"
	ActionColumnNext  Action = "column_next"
	ActionHideColumn  Action = "hide_column"
	ActionShowColumns Action = "show_columns"
	ActionPickFile    Action = "pick_file"
)

// ToggleColumnPrefix prefixes the per-column toggle actions ("toggle_column:time").
const ToggleColumnPrefix = "toggle_column:"

// Command describes an Action for the palette, help and key binding.
type Command struct {
	Action      Action
	Title       string
	Description string
	Keys        []string
	InPalette   bool
}

// Commands is an ordered, rebindable command registry.
type Commands struct {
	list  []Command
	byKey map[string]Action
}

// DefaultCommands returns the built-in commands with default key bindings.
func DefaultCommands() *Commands {
	c := &Commands{}
	add := func(a Action, title, desc string, palette bool, keys ...string) {
		c.list = append(c.list, Command{a, title, desc, keys, palette})
	}
	add(ActionDown, "Down", "Move to the next record", false, "down", "j")
	add(ActionUp, "Up", "Move to the previous record", false, "up", "k")
	add(ActionPageDown, "Page down", "Scroll one page down", false, "pgdown", "space", "ctrl+d")
	add(ActionPageUp, "Page up", "Scroll one page up", false, "pgup", "b", "ctrl+u")
	add(ActionTop, "Go to top", "Jump to the first record", true, "home", "g")
	add(ActionBottom, "Go to bottom", "Jump to the last record", true, "end", "G")
	add(ActionSearch, "Search", "Search the current view", true, "ctrl+f", "/")
	add(ActionSearchNext, "Next match", "Jump to the next search match", true, "n", "f3")
	add(ActionSearchPrev, "Previous match", "Jump to the previous search match", true, "N", "shift+f3")
	add(ActionNextError, "Next error", "Jump to the next error, critical, or fatal record", true, "alt+e")
	add(ActionPrevError, "Previous error", "Jump to the previous error, critical, or fatal record", true, "alt+E", "alt+shift+e")
	add(ActionNextWarning, "Next warning", "Jump to the next warning or more severe record", true, "alt+W", "alt+shift+w")
	add(ActionPrevWarning, "Previous warning", "Jump to the previous warning or more severe record", true, "alt+w")
	add(ActionJumpRecord, "Jump to record ID", "Jump to a record by its stable ID", true, "ctrl+j")
	add(ActionToggleBookmark, "Toggle bookmark", "Bookmark or unbookmark the current record", true, "m")
	add(ActionNextBookmark, "Next bookmark", "Jump to the next visible bookmark", true, "]")
	add(ActionPrevBookmark, "Previous bookmark", "Jump to the previous visible bookmark", true, "[")
	add(ActionSelectRange, "Select records", "Start or extend a record range selection", true, "v")
	add(ActionClearSelection, "Clear selection", "Clear the current record range selection", true, "V")
	add(ActionCopy, "Copy records", "Copy the selected records, or the current record, to the clipboard (needs OSC 52 support in the terminal)", true, "y")
	add(ActionFilter, "Filter", "Show only matching records, e.g. severity:>=warn", true, "ctrl+l", "f")
	add(ActionClearFilter, "Clear filter", "Show all records again", true, "ctrl+k", "c")
	add(ActionInspect, "Inspect record", "Open the detail overlay", true, "enter", "i")
	add(ActionFollow, "Toggle follow", "Pause or resume following new records (live mode)", true, "ctrl+t", "p")
	add(ActionColumnPrev, "Previous column", "Select the previous column (highlighted in the header)", true, "h", "left", "shift+tab")
	add(ActionColumnNext, "Next column", "Select the next column (highlighted in the header)", true, "l", "right", "tab")
	add(ActionHideColumn, "Hide current column", "Hide the highlighted column", true, "x")
	add(ActionShowColumns, "Show all columns", "Bring every hidden column back", true, "X")
	add(ActionPickFile, "Pick file from directory", "List the directory's log files and open one", true, "ctrl+o")
	add(ActionQuickOpen, "Open file", "Open another log file", true, "ctrl+p", "o")
	add(ActionPalette, "Command palette", "Run any command by name", false, "ctrl+shift+p", ":", "f1")
	add(ActionHelp, "Help", "Show keys and commands", true, "?")
	add(ActionClose, "Close / cancel", "Close the current overlay", false, "esc")
	add(ActionQuit, "Quit", "Exit logi", true, "q", "ctrl+c")
	c.reindex()
	return c
}

func (c *Commands) reindex() {
	c.byKey = map[string]Action{}
	for _, cmd := range c.list {
		for _, k := range cmd.Keys {
			c.byKey[k] = cmd.Action
		}
	}
}

// Rebind replaces the keys of an action (config "keys:" section). Keys taken
// from other actions are moved, so the last binding wins deterministically.
func (c *Commands) Rebind(a Action, keys []string) bool {
	found := false
	for i := range c.list {
		if c.list[i].Action == a {
			c.list[i].Keys, found = append([]string(nil), keys...), true
		}
	}
	if !found {
		return false
	}
	for i := range c.list {
		if c.list[i].Action == a {
			continue
		}
		kept := c.list[i].Keys[:0:0]
		for _, k := range c.list[i].Keys {
			taken := false
			for _, nk := range keys {
				taken = taken || nk == k
			}
			if !taken {
				kept = append(kept, k)
			}
		}
		c.list[i].Keys = kept
	}
	c.reindex()
	return true
}

// Lookup resolves a key string (as reported by Bubble Tea) to an Action.
func (c *Commands) Lookup(key string) (Action, bool) { a, ok := c.byKey[key]; return a, ok }

// All returns every command in display order.
func (c *Commands) All() []Command { return append([]Command(nil), c.list...) }

// Palette returns palette commands sorted by title.
func (c *Commands) Palette() []Command {
	var out []Command
	for _, cmd := range c.list {
		if cmd.InPalette {
			out = append(out, cmd)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// Get returns the command for an action.
func (c *Commands) Get(a Action) (Command, bool) {
	for _, cmd := range c.list {
		if cmd.Action == a {
			return cmd, true
		}
	}
	return Command{}, false
}
