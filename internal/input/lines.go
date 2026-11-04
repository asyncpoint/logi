package input

import (
	"bytes"
	"io"
	"os"
	"time"

	"github.com/asyncpoint/logi/internal/parser"
	"github.com/asyncpoint/logi/internal/platform"
)

// backlogForLines returns how many bytes before the end of f (size bytes long)
// hold its last n lines, or size if the file has fewer. The newline that ends the
// last line is not counted as starting another one.
func backlogForLines(f io.ReaderAt, size int64, n int) int64 {
	if n <= 0 || size <= 0 {
		return 0
	}
	const block = 64 << 10
	buf := make([]byte, block)
	seen := 0
	for pos := size; pos > 0; {
		step := int64(block)
		if pos < step {
			step = pos
		}
		pos -= step
		m, _ := f.ReadAt(buf[:step], pos)
		for i := m - 1; i >= 0; i-- {
			if buf[i] != '\n' || pos+int64(i) == size-1 {
				continue
			}
			if seen++; seen == n {
				return size - (pos + int64(i) + 1)
			}
		}
	}
	return size
}

// BacklogForLines is backlogForLines for a file on disk (0 if it cannot be read).
func BacklogForLines(path string, n int) int64 {
	f, err := platform.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	return backlogForLines(f, fi.Size(), n)
}

// LastWallNano finds the wall-clock reading (see engine.Timestamp.WallNano) of the
// newest timestamped record near the end of the file, so that relative times such
// as "since 15m" can be measured from the log's own clock instead of the machine's
// (which is wrong for old files and for logs written without a time zone).
// parserName is a profile name, or "" / "auto" to detect it.
func LastWallNano(path string, reg *parser.Registry, parserName string) (int64, bool) {
	f, err := platform.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return 0, false
	}
	head := make([]byte, 64<<10)
	hn, _ := f.ReadAt(head, 0)
	head = head[:hn]
	var prof *parser.Profile
	if parserName != "" && parserName != "auto" {
		prof, _ = reg.Get(parserName)
	}
	if prof == nil {
		prof = reg.Detect(head)
	}
	if prof == nil {
		return 0, false
	}
	p := parser.WithYearRef(prof.ParserFor(head), modTime(fi))

	const tailSize = 256 << 10
	start := fi.Size() - tailSize
	if start < 0 {
		start = 0
	}
	tail := make([]byte, fi.Size()-start)
	tn, _ := f.ReadAt(tail, start)
	tail = tail[:tn]
	if start > 0 { // we began mid-line
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
	}
	for end := len(tail); end > 0; {
		i := bytes.LastIndexByte(tail[:end], '\n')
		line := bytes.TrimRight(tail[i+1:end], "\r")
		end = i
		if len(line) == 0 {
			continue
		}
		if rec, _ := p.Parse(string(line)); !rec.Timestamp.IsZero() {
			return rec.Timestamp.WallNano(), true
		}
	}
	return 0, false
}

func modTime(fi os.FileInfo) time.Time { return fi.ModTime() }
