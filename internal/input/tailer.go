package input

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/framing"
	"github.com/asyncpoint/logi/internal/parser"
	"github.com/asyncpoint/logi/internal/platform"
)

// TailOptions configures a Tailer.
type TailOptions struct {
	Path     string
	SourceID string
	Registry *parser.Registry
	Parser   string // explicit profile name; "" or "auto" => detect
	Limits   engine.Limits
	Store    *engine.RingStore
	Sink     Sink

	// Backlog is how many bytes before the current end of file to read on
	// start so the view is not empty. 0 = only new data, <0 = whole file.
	Backlog int64

	// BacklogLines, when UseBacklogLines is set, replaces Backlog: start with only
	// the last N lines of each file, worked out when the file is first opened.
	BacklogLines    int
	UseBacklogLines bool

	// Reader, when set, is ingested instead of a file (standard input). There is no
	// rotation or seeking: bytes are consumed in order through the same framing,
	// parsing and storage pipeline, and the view stays open when the reader ends.
	Reader io.Reader

	ChunkSize      int             // bytes per read (default 256 KiB)
	IdleFlush      time.Duration   // hold-open time for multiline records (default 300ms)
	Coalesce       time.Duration   // minimum spacing between reads under load (default 10ms)
	SafetyInterval time.Duration   // slow safety check when notifications work (default 5s)
	PollInterval   time.Duration   // polling period when notifications are unavailable (default 500ms)
	ForcePoll      bool            // never use filesystem notifications
	LineFraming    bool            // one record per line, no multiline grouping
	Wake           <-chan struct{} // optional external wake-ups (directory follower); disables the per-file watcher
}

type rawRec struct {
	text      string
	off, len  int64
	line      int64
	lines     int
	truncated bool
	src       string           // source from container decoration; "" = the tailer's own
	hint      engine.Timestamp // time stamped by the tooling, used if the text has none
}

// Tailer follows a growing file using offset-based incremental reads. State is
// (path, file identity, offset); the offset is always the first unconsumed byte.
// Two bounded stages — read+frame, then parse+store — are joined
// by a bounded queue so memory stays bounded and the UI is never the
// back-pressure mechanism.
type Tailer struct {
	o TailOptions

	f      *os.File
	id     platform.FileIdentity
	offset int64

	profile   *parser.Profile
	pub       atomic.Pointer[parser.Profile] // profile published for other goroutines
	framer    framing.Framer
	sample    []byte // detection sample retained for re-creating framers
	head      []byte // start of the file, so a column header is known even when we start mid-file
	detect    []byte // bytes buffered until enough exist to detect a format
	detectOff int64

	batch      []rawRec
	batches    chan []rawRec
	fp         []byte        // fingerprint: first bytes of the current file
	dec        *decorator    // stream input with container decoration (see decor.go)
	srcCount   atomic.Int64  // distinct sources seen by dec
	yearRef    time.Time     // file mtime at pipeline start; resolves year-less timestamps
	free       chan []rawRec // recycled batch buffers (ingest -> collector)
	wg         sync.WaitGroup
	buf        []byte
	missing    bool
	finish     chan struct{}
	idleArmed  bool
	finishOnce sync.Once
	ctx        context.Context // set by Run; lets long reads stop promptly
}

// NewTailer validates options and applies defaults.
func NewTailer(o TailOptions) (*Tailer, error) {
	if o.Store == nil || o.Registry == nil || o.Sink == nil {
		return nil, errors.New("input: tailer requires Store, Registry and Sink")
	}
	if o.ChunkSize <= 0 {
		o.ChunkSize = 256 << 10
	}
	if o.IdleFlush <= 0 {
		o.IdleFlush = 300 * time.Millisecond
	}
	if o.Coalesce <= 0 {
		o.Coalesce = 10 * time.Millisecond
	}
	if o.SafetyInterval <= 0 {
		o.SafetyInterval = 5 * time.Second
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 500 * time.Millisecond
	}
	if o.Limits.MaxRecordBytes <= 0 {
		o.Limits = engine.DefaultLimits()
	}
	t := &Tailer{o: o, buf: make([]byte, o.ChunkSize), batches: make(chan []rawRec, 4), free: make(chan []rawRec, 4), finish: make(chan struct{})}
	if o.Parser != "" && o.Parser != "auto" {
		p, ok := o.Registry.Get(o.Parser)
		if !ok {
			return nil, errors.New("input: unknown parser " + o.Parser)
		}
		t.profile = p
		t.pub.Store(p)
	}
	return t, nil
}

// Profile returns the parser profile in use (nil until the format is known).
// It is safe to call from any goroutine.
func (t *Tailer) Profile() *parser.Profile { return t.pub.Load() }

// Finish asks the tailer to read everything the file currently holds, flush any
// open record (including an unterminated last line), and stop. Run returns nil
// once that is done. Safe to call more than once.
func (t *Tailer) Finish() { t.finishOnce.Do(func() { close(t.finish) }) }

// Run follows the file until ctx is cancelled or Finish is called. It owns every goroutine it
// starts and waits for them before returning.
func (t *Tailer) Run(ctx context.Context) error {
	t.ctx = ctx
	t.wg.Add(1)
	go t.ingest()
	defer func() {
		close(t.batches)
		t.wg.Wait()
		if t.f != nil {
			t.f.Close()
		}
	}()

	if t.o.Reader != nil {
		return t.runStream(ctx)
	}

	var wake <-chan struct{}
	interval := t.o.PollInterval
	if t.o.Wake != nil {
		wake = t.o.Wake
		interval = t.o.SafetyInterval
	} else if !t.o.ForcePoll {
		if w, err := NewWatcher(t.o.Path); err == nil {
			defer w.Close()
			wake = w.Wake()
			interval = t.o.SafetyInterval
		}
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	coalesce := time.NewTimer(time.Hour)
	t.stopTimer(coalesce)
	defer coalesce.Stop()
	idle := time.NewTimer(time.Hour)
	if !idle.Stop() {
		<-idle.C
	}
	defer idle.Stop()

	t.check(true)
	lastOff := t.offset
	t.armIdle(idle, true)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.finish:
			t.check(false)
			t.finishFile()
			return nil
		case <-wake:
		case <-tick.C:
		case <-idle.C:
			t.idleArmed = false
			if t.framer != nil {
				t.flush(false)
				t.sendBatch()
			}
			continue
		}
		t.check(false)
		t.armIdle(idle, t.offset != lastOff)
		lastOff = t.offset
		// Coalesce bursts of events into one read; stays cancelable.
		t.stopTimer(coalesce)
		coalesce.Reset(t.o.Coalesce)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-coalesce.C:
		}
	}
}

// armIdle keeps the idle-flush timer honest: it is (re)started only when new
// bytes arrived, so frequent poll ticks can never postpone a flush forever.
// runStream ingests t.o.Reader (standard input). A goroutine does the blocking
// reads; this loop frames and stores what arrives, flushes a held-open multiline
// record after IdleFlush, and, when the stream ends, flushes everything and
// stays open so the view remains browsable.
func (t *Tailer) runStream(ctx context.Context) error {
	type chunk struct {
		buf  []byte // full-size buffer to recycle
		data []byte
		err  error
	}
	ch := make(chan chunk, 4)
	// A fixed set of buffers circulates between the reader goroutine and this
	// loop. Allocating ChunkSize per Read amplified small pipe reads (a few KiB
	// each) into 256 KiB allocations. consume copies what it keeps, exactly as
	// for the file path, which already reuses t.buf the same way.
	free := make(chan []byte, cap(ch)+2)
	for i := 0; i < cap(free); i++ {
		free <- make([]byte, t.o.ChunkSize)
	}
	go func() {
		for {
			var buf []byte
			select {
			case buf = <-free:
			case <-ctx.Done():
				return
			}
			n, err := t.o.Reader.Read(buf)
			if n > 0 {
				select {
				case ch <- chunk{buf: buf, data: buf[:n]}:
				case <-ctx.Done():
					return
				}
			} else {
				free <- buf // nothing to hand over: keep the buffer in circulation
			}
			if err != nil {
				select {
				case ch <- chunk{err: err}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	idle := time.NewTimer(time.Hour)
	if !idle.Stop() {
		<-idle.C
	}
	defer idle.Stop()
	t.o.Sink.State(StateRunning, "")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.finish:
			t.finishFile()
			return nil
		case <-idle.C:
			t.idleArmed = false
			if t.framer != nil {
				t.flush(false)
				t.sendBatch()
			}
		case c := <-ch:
			if len(c.data) > 0 {
				t.consume(c.data)
				t.offset += int64(len(c.data))
				t.sendBatch()
				t.armIdle(idle, true)
				free <- c.buf // consume has copied everything it keeps
			}
			if c.err != nil {
				t.finishFile()
				if errors.Is(c.err, io.EOF) {
					t.o.Sink.State(StateEnded, "end of input")
				} else {
					t.o.Sink.State(StateError, c.err.Error())
				}
				ch = nil // nothing more will arrive; wait for the caller to stop us
			}
		}
	}
}

func (t *Tailer) armIdle(tm *time.Timer, progressed bool) {
	if !t.pending() {
		t.stopTimer(tm)
		t.idleArmed = false
		return
	}
	if t.idleArmed && !progressed {
		return
	}
	t.stopTimer(tm)
	tm.Reset(t.o.IdleFlush)
	t.idleArmed = true
}

func (t *Tailer) stopTimer(tm *time.Timer) {
	if !tm.Stop() {
		select {
		case <-tm.C:
		default:
		}
	}
}

// check inspects the real file state; notifications only trigger it.
func (t *Tailer) check(first bool) {
	info, err := platform.StatPath(t.o.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if t.f != nil {
				t.readAvailable() // drain what the old file still holds
			}
			if !t.missing {
				t.missing = true
				t.o.Sink.State(StateWaiting, "waiting for "+t.o.Path)
			}
			return
		}
		t.o.Sink.State(StateError, err.Error())
		return
	}
	t.missing = false
	switch {
	case t.f == nil:
		t.open(info, first)
	case info.ID != t.id: // same path, different file: rotation/replacement
		t.readAvailable()
		t.finishFile()
		t.f.Close()
		t.f = nil
		t.o.Sink.State(StateRotated, "log rotated; following new file")
		t.open(info, false)
	case info.Size < t.offset: // same file, smaller: truncated in place
		t.restart("file truncated; restarting from the beginning")
	case info.Size > t.offset && t.headChanged():
		// Same inode and larger, but the start of the file differs from what we
		// read: it was truncated and regrown past our offset between checks
		// (copytruncate). Without this the new lines before our offset are lost.
		t.restart("file replaced in place; restarting from the beginning")
	default:
		if info.Size > t.offset {
			t.readAvailable()
		}
	}
}

// restart rereads the current file from the top after an in-place truncation.
func (t *Tailer) restart(msg string) {
	t.finishFile()
	t.offset = 0
	t.fp = nil
	if t.framer != nil {
		t.framer.Reset(0, 1)
	}
	t.detect = t.detect[:0]
	t.o.Sink.State(StateTruncated, msg)
	t.readAvailable()
}

const fingerprintLen = 256

// refreshFP remembers the first bytes of the file (once we have read that far)
// so an in-place rewrite can be told apart from ordinary appends.
func (t *Tailer) refreshFP() {
	if t.f == nil || len(t.fp) >= fingerprintLen || t.offset <= int64(len(t.fp)) {
		return
	}
	buf := make([]byte, min(t.offset, fingerprintLen))
	if n, _ := t.f.ReadAt(buf, 0); n == len(buf) {
		t.fp = buf
	}
}

// headChanged reports whether the file's first bytes no longer match the
// fingerprint taken while reading it.
func (t *Tailer) headChanged() bool {
	if t.f == nil || len(t.fp) == 0 {
		return false
	}
	buf := make([]byte, len(t.fp))
	n, _ := t.f.ReadAt(buf, 0)
	return n < len(buf) || !bytes.Equal(buf, t.fp)
}

func (t *Tailer) open(info platform.PathInfo, first bool) {
	f, err := platform.Open(t.o.Path)
	if err != nil {
		t.o.Sink.State(StateError, err.Error())
		return
	}
	fi, err := platform.IdentityOfFile(f)
	if err != nil {
		f.Close()
		t.o.Sink.State(StateError, err.Error())
		return
	}
	t.f, t.id = f, fi.ID
	t.fp = nil
	start := int64(0)
	if first {
		backlog := t.o.Backlog
		if t.o.UseBacklogLines {
			backlog = backlogForLines(f, fi.Size, t.o.BacklogLines)
		}
		if backlog >= 0 {
			if start = fi.Size - backlog; start < 0 {
				start = 0
			}
			if !t.o.UseBacklogLines { // a byte count can land mid-line; a line count is exact
				start = t.alignToLine(start)
			}
		}
	}
	t.offset = start
	if t.framer != nil {
		t.framer.Reset(start, 1)
		t.detect = t.detect[:0]
	}
	t.o.Sink.State(StateRunning, "")
	t.readAvailable()
}

// alignToLine moves start forward to the beginning of the next full line.
func (t *Tailer) alignToLine(start int64) int64 {
	if start == 0 {
		return 0
	}
	var b [4096]byte
	for {
		n, err := t.f.ReadAt(b[:], start)
		if i := bytes.IndexByte(b[:n], '\n'); i >= 0 {
			return start + int64(i) + 1
		}
		start += int64(n)
		if err != nil || n == 0 {
			return start
		}
	}
}

// readAvailable reads from offset until EOF, framing what it gets.
func (t *Tailer) readAvailable() {
	if t.f == nil {
		return
	}
	for {
		// A huge backlog (--backlog -1 on a multi-gigabyte file) is read in one
		// loop; check for cancellation per chunk so quitting stays responsive.
		if t.ctx != nil && t.ctx.Err() != nil {
			break
		}
		n, err := t.f.ReadAt(t.buf, t.offset)
		if n > 0 {
			t.consume(t.buf[:n])
			t.offset += int64(n)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.o.Sink.State(StateError, err.Error())
			}
			break
		}
		if n == 0 {
			break
		}
	}
	t.refreshFP()
	t.sendBatch()
}

// consume routes bytes through format detection (first data only) and the framer.
func (t *Tailer) consume(data []byte) {
	if t.framer == nil {
		if len(t.detect) == 0 {
			t.detectOff = t.offset
		}
		t.detect = append(t.detect, data...)
		if bytes.IndexByte(t.detect, '\n') < 0 && len(t.detect) < 4096 {
			return // not enough to judge yet
		}
		buf := t.detect
		t.detect = nil
		t.startPipeline(buf, t.detectOff)
		return
	}
	t.push(data)
}

// startPipeline initialises detection, parsing and framing from the first data.
// For piped input it first recognises container tooling's line decoration (see
// decor.go) and detects the format from the application's own text.
func (t *Tailer) startPipeline(data []byte, off int64) {
	sample := data
	if t.o.Reader != nil {
		if st := detectStyle(data); st.active() {
			sample = st.stripSample(data)
			t.dec = newDecorator(st, t)
		}
	}
	t.initPipeline(sample, off)
	t.push(data)
}

// push, flush and pending route to the per-source framers when the stream is
// decorated, otherwise to the single framer.
func (t *Tailer) push(data []byte) {
	if t.dec != nil {
		t.dec.push(data)
		return
	}
	t.framer.Push(data, t.collect)
}

func (t *Tailer) flush(force bool) {
	if t.dec != nil {
		t.dec.flushAll(force)
	} else if t.framer != nil {
		t.framer.Flush(force, t.collect)
	}
}

func (t *Tailer) pending() bool {
	if t.dec != nil {
		return t.dec.pending()
	}
	return t.framer != nil && t.framer.Pending()
}

// Sources reports how many distinct sources (compose services, pods) a decorated
// stream has shown so far; 0 for ordinary input.
func (t *Tailer) Sources() int { return int(t.srcCount.Load()) }

func (t *Tailer) initPipeline(sample []byte, off int64) {
	if len(sample) > 32<<10 {
		sample = sample[:32<<10]
	}
	t.sample = append([]byte(nil), sample...)
	if t.f != nil {
		if fi, err := t.f.Stat(); err == nil {
			t.yearRef = fi.ModTime()
		}
	}
	if off > 0 && t.f != nil { // started mid-file (backlog): the header, if any, is at the top
		buf := make([]byte, 8<<10)
		n, _ := t.f.ReadAt(buf, 0)
		t.head = buf[:n]
	}
	if t.profile == nil {
		t.profile = t.o.Registry.Detect(t.sample)
		t.pub.Store(t.profile)
	}
	if t.o.LineFraming {
		t.framer = framing.NewLineFramer(t.o.Limits, off)
		return
	}
	t.framer = t.profile.Framer(t.sample, t.o.Limits, off)
}

// finishFile flushes everything the finished file still holds, including an
// unterminated final line.
func (t *Tailer) finishFile() {
	if t.framer == nil && len(t.detect) > 0 {
		buf := t.detect
		t.detect = nil
		t.startPipeline(buf, t.detectOff)
	}
	if t.framer != nil {
		t.flush(true)
	}
	t.sendBatch()
}

func (t *Tailer) collect(f *framing.Frame) { t.collectWith(f, "", engine.Timestamp{}) }

func (t *Tailer) collectWith(f *framing.Frame, src string, hint engine.Timestamp) {
	if t.batch == nil {
		select {
		case t.batch = <-t.free:
		default:
			t.batch = make([]rawRec, 0, 16)
		}
	}
	t.batch = append(t.batch, rawRec{text: engine.StripANSI(string(f.Data)), off: f.Offset, len: f.Length,
		line: f.Line, lines: f.Lines, truncated: f.Truncated, src: src, hint: hint})
	if len(t.batch) >= 2048 {
		t.sendBatch()
	}
}

// sendBatch hands the batch to the parse/store stage. The bounded channel makes
// this the (only) back-pressure point of the pipeline.
func (t *Tailer) sendBatch() {
	if len(t.batch) == 0 {
		return
	}
	b := t.batch
	t.batch = nil
	select {
	case t.batch = <-t.free:
	default: // quiet source: start small, append grows it only under load
	}
	t.batches <- b
}

// ingest is stage two: parse, store, notify — once per record.
func (t *Tailer) ingest() {
	defer t.wg.Done()
	var p parser.Parser
	var recs []engine.LogRecord
	for b := range t.batches {
		if p == nil {
			if t.profile == nil { // cannot happen: batches only exist after detection
				continue
			}
			sample := make([]byte, len(t.head)+len(t.sample))
			copy(sample, t.head)
			copy(sample[len(t.head):], t.sample)
			p = parser.WithYearRef(t.profile.ParserFor(sample), t.yearRef)
			// The framer and parser have consumed their detection samples. Release
			// the tailer's references instead of retaining up to 40 KiB per source.
			t.head, t.sample = nil, nil
		}
		recs = recs[:0]
		for i := range b {
			r := &b[i]
			rec, err := p.Parse(r.text)
			rec.Raw = engine.RawRecord{Offset: r.off, Length: r.len, Data: r.text}
			rec.SourceID = t.o.SourceID
			if r.src != "" {
				rec.SourceID = r.src
			}
			if rec.Timestamp.IsZero() {
				rec.Timestamp = r.hint
			}
			rec.LineStart, rec.LineEnd = r.line, r.line+int64(r.lines)-1
			rec.Multiline = rec.Multiline || r.lines > 1
			rec.Truncated = r.truncated
			rec.ParseErr = rec.ParseErr || err != nil
			recs = append(recs, rec)
		}
		first, last, ev := t.o.Store.AppendBatch(recs)
		t.o.Sink.Added(first, last, ev)
		// AppendBatch copies each record into the store. Clear the reusable slice
		// so its backing array does not keep evicted payload strings alive.
		clear(recs)
		// Hand the buffer back for reuse (strings cleared so they can be collected).
		clear(b)
		if cap(b) <= 4096 {
			select {
			case t.free <- b[:0]:
			default:
			}
		}
	}
}
