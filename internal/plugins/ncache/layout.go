package ncache

import (
	"bytes"
	"sort"
)

// layout describes a fixed-width table: the friendly field name and start offset
// of each column, from TIMESTAMP to MESSAGE. splitEvent is set for the cache log,
// whose MESSAGE column holds an event name followed by the text.
type layout struct {
	names      []string
	off        []int
	splitEvent bool
}

func (l layout) levelIdx() int {
	for i, n := range l.names {
		if n == "level" {
			return i
		}
	}
	return -1
}

// cacheLogLayout is the NCache cache log:
//
//	TIMESTAMP  APPDOMAIN  LOGGERNAME  THREADNAME  LEVEL  MESSAGE(event + text)
var cacheLogLayout = layout{
	names:      []string{"timestamp", "appdomain", "logger", "thread", "level", "message"},
	off:        []int{0, 32, 80, 128, 176, 192},
	splitEvent: true,
}

// eventLogLayout is the NCache event log. The offsets are those of the data rows,
// which in real files sit a few columns to the right of the header's.
//
//	TIMESTAMP  SOURCE  EVENTID  LEVEL  MESSAGE
var eventLogLayout = layout{
	names: []string{"timestamp", "component", "eventid", "level", "message"},
	off:   []int{0, 48, 72, 96, 120},
}

// friendlyName renames header words that would otherwise collide with a
// built-in column: the event log's own SOURCE column (which file, service or
// component logged the event) is not the same thing as logi's built-in
// "source" column (which FILE the record was read from in a merged
// directory view), so it becomes "component" instead.
var friendlyName = map[string]string{
	"TIMESTAMP": "timestamp", "LOGGERNAME": "logger", "THREADNAME": "thread", "SOURCE": "component",
}

func fieldName(header string) string {
	if n, ok := friendlyName[header]; ok {
		return n
	}
	return string(bytes.ToLower([]byte(header)))
}

type word struct {
	text string
	at   int
}

// headerWords splits a header line into its upper-case column names.
func headerWords(line []byte) []word {
	var out []word
	for i := 0; i < len(line); {
		if line[i] == ' ' || line[i] == '\t' {
			i++
			continue
		}
		j := i
		for j < len(line) && line[j] != ' ' && line[j] != '\t' {
			j++
		}
		out = append(out, word{string(line[i:j]), i})
		i = j
	}
	return out
}

func isUpperWord(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// layoutFromSample reads the column layout from the header line in sample and
// then corrects the offsets against the data rows that follow it. Real files do
// not always align the header with the rows (the event log's rows are shifted a
// few columns right of its header), so header positions alone are not trusted.
// Without a usable header, fallback is returned.
func layoutFromSample(sample []byte, fallback layout) layout {
	lines := bytes.Split(sample, []byte("\n"))
	if len(lines) > 80 {
		lines = lines[:80]
	}
	for hi, line := range lines {
		line = bytes.TrimRight(line, "\r")
		if !isHeader(line) {
			continue
		}
		words := headerWords(line)
		if len(words) < 3 || words[0].text != "TIMESTAMP" || words[len(words)-1].text != "MESSAGE" {
			return fallback
		}
		lay := layout{}
		for _, w := range words {
			if !isUpperWord(w.text) {
				return fallback
			}
			lay.names = append(lay.names, fieldName(w.text))
			lay.off = append(lay.off, w.at)
		}
		if lay.levelIdx() < 1 {
			return fallback
		}
		for _, n := range lay.names {
			if n == "logger" {
				lay.splitEvent = true
			}
		}
		var rows []string
		for _, l := range lines[hi+1:] {
			l = bytes.TrimRight(l, "\r")
			if isStart(l) {
				rows = append(rows, string(l))
				if len(rows) == 8 {
					break
				}
			}
		}
		alignToRows(&lay, rows)
		return lay
	}
	return fallback
}

// alignToRows moves each column start to where the data rows actually begin the
// field, searching a small window around the header position. The most common
// position over the sampled rows wins; columns without evidence keep the header's.
func alignToRows(lay *layout, rows []string) {
	n := len(lay.off)
	votes := make([]map[int]int, n)
	for i := range votes {
		votes[i] = map[int]int{}
	}
	for _, row := range rows {
		prev := 0
		for i := 1; i < n; i++ {
			lo := max(lay.off[i]-8, prev+1)
			hi := min(lay.off[i]+12, len(row)-1)
			for p := lo; p <= hi; p++ {
				// A real column boundary is padded with a genuine gap (2+ spaces or
				// a tab, the same convention gap() uses elsewhere), not a single
				// space — which also occurs *inside* a timestamp ("date time").
				if row[p] == ' ' || row[p] == '\t' || p < 2 {
					continue
				}
				gapBefore := row[p-1] == '\t' || (row[p-1] == ' ' && (row[p-2] == ' ' || row[p-2] == '\t'))
				if gapBefore {
					votes[i][p]++
					prev = p
					break
				}
			}
		}
	}
	aligned := append([]int(nil), lay.off...)
	for i := 1; i < n; i++ {
		best, bestN := 0, 0
		keys := make([]int, 0, len(votes[i]))
		for p := range votes[i] {
			keys = append(keys, p)
		}
		sort.Ints(keys)
		for _, p := range keys {
			if votes[i][p] > bestN {
				best, bestN = p, votes[i][p]
			}
		}
		if bestN > 0 {
			aligned[i] = best
		}
	}
	for i := 1; i < n; i++ { // keep the result only if it is a sane, increasing layout
		if aligned[i] <= aligned[i-1] {
			return
		}
	}
	lay.off = aligned
}
