package ncache

import (
	"bytes"

	"github.com/asyncpoint/logi/internal/framing"
)

// isStart reports whether a physical line starts a new NCache record: a full
// "yyyy-MM-dd HH:mm:ss" date-time prefix.
func isStart(l []byte) bool {
	return len(l) >= 19 && (l[10] == ' ' || l[10] == 'T') && framing.LooksLikeTimestamp(l)
}

// hasCommaMillis: cache-log timestamps carry milliseconds as "...:04,123".
func hasCommaMillis(l []byte) bool {
	return len(l) >= 23 && l[19] == ',' && l[20] >= '0' && l[20] <= '9'
}

// hasSpacedMillis: event-log timestamps carry them as "...:56, 110".
func hasSpacedMillis(l []byte) bool {
	return len(l) >= 24 && l[19] == ',' && l[20] == ' ' && l[21] >= '0' && l[21] <= '9' && l[22] >= '0' && l[22] <= '9' && l[23] >= '0' && l[23] <= '9'
}

// isHeader recognises the column header NCache writes at the top of each file.
func isHeader(l []byte) bool {
	l = bytes.TrimSpace(l)
	return bytes.HasPrefix(l, []byte("TIMESTAMP")) && bytes.Contains(l, []byte("LEVEL")) && bytes.Contains(l, []byte("MESSAGE"))
}

// isTerminator matches the "END" marker line the event log writes after some entries.
func isTerminator(l []byte) bool { return string(bytes.TrimSpace(l)) == "END" }

type sampleInfo struct {
	header  []byte
	mention bool
	starts  int // lines that start like a record with the wanted millisecond style
	total   int
}

func analyze(sample []byte, millis func([]byte) bool) sampleInfo {
	if len(sample) > 16<<10 {
		sample = sample[:16<<10]
	}
	lower := bytes.ToLower(sample)
	info := sampleInfo{mention: bytes.Contains(lower, []byte("ncache")) || bytes.Contains(lower, []byte("alachisoft"))}
	rest := sample
	for n := 0; n < 100 && len(rest) > 0; n++ {
		var line []byte
		if i := bytes.IndexByte(rest, '\n'); i < 0 {
			line, rest = rest, nil
		} else {
			line, rest = rest[:i], rest[i+1:]
		}
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 || isTerminator(line) {
			continue
		}
		if isHeader(line) {
			info.header = line
			continue
		}
		info.total++
		if isStart(line) && millis(line) {
			info.starts++
		}
	}
	return info
}

// score turns a sample into a confidence. A recognised header is decisive; without
// one the sample must mention NCache/Alachisoft so that generic log4net-style
// output is never claimed. --parser always forces a profile.
func (i sampleInfo) score(headerMarks bool) float64 {
	if headerMarks {
		return 0.98
	}
	if i.total == 0 || i.starts == 0 {
		return 0
	}
	ratio := float64(i.starts) / float64(i.total)
	if !i.mention {
		return 0.2 * ratio
	}
	return 0.6 + 0.35*ratio
}

// detectCache recognises the cache log (header with APPDOMAIN, comma milliseconds).
func detectCache(sample []byte) float64 {
	i := analyze(sample, hasCommaMillis)
	return i.score(i.header != nil && bytes.Contains(i.header, []byte("APPDOMAIN")))
}

// detectEvents recognises the event log (header with EVENTID, ", 110" milliseconds).
func detectEvents(sample []byte) float64 {
	i := analyze(sample, hasSpacedMillis)
	return i.score(i.header != nil && bytes.Contains(i.header, []byte("EVENTID")))
}
