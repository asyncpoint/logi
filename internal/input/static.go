package input

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/framing"
	"github.com/asyncpoint/logi/internal/parser"
	"github.com/asyncpoint/logi/internal/platform"
)

const (
	defaultBlockRecords = 128      // records per navigation checkpoint
	defaultCacheBlocks  = 256      // maximum parsed blocks kept in RAM (also bounded by defaultCacheBytes)
	defaultCacheBytes   = 16 << 20 // maximum estimated retained bytes in parsed cache
	scanChunk           = 1 << 20  // indexing read size
	rangeSegmentBytes   = 2 << 20  // target bytes per sequential Range read
	maxRawBytes         = 4 << 20  // cap on one record read for the detail view (UI goroutine)
)

type checkpoint struct{ off, line int64 }

// StaticOptions configures a StaticStore.
type StaticOptions struct {
	Path string
	// File is an optional, already-open handle on Path (from platform.Open). The
	// caller keeps ownership if OpenStatic returns an error; on success the store
	// owns it and closes it in Close. Passing it avoids opening the file a second
	// time and guarantees the sample and the index come from the same file even
	// if it is rotated in between.
	File        *os.File
	SourceID    string
	Profile     *parser.Profile
	Sample      []byte // initial bytes, used for framer detection
	Limits      engine.Limits
	BlockSize   int
	CacheBlocks int
	CacheBytes  int64  // maximum estimated retained bytes in the parsed-block cache
	Notify      func() // called (from the indexer goroutine) as the index grows
	ScanWorkers int    // goroutines used by Range over large spans; 0 = min(GOMAXPROCS, 8)
}

// StaticStore gives lazy, offset-based access to a file that is not expected to
// grow. RAM holds one 16-byte checkpoint per BlockSize records plus a small
// cache of parsed blocks; record payloads remain on disk until a block is read.
// Records are numbered 1..N in file order. The index is built progressively in
// the background by framing only (no parsing), reading the source file to record
// logical-record offsets.
type StaticStore struct {
	opts   StaticOptions
	f      *os.File
	size   int64
	parser parser.Parser
	block  int

	mu      sync.RWMutex
	cps     []checkpoint
	count   uint64
	spanEnd int64
	done    bool
	scanned atomic.Int64

	cmu           sync.Mutex
	cache         map[uint64]*cachedBlock
	order         []uint64 // LRU: oldest first
	cacheBytes    int64
	maxCacheBytes int64
	scratchPool   sync.Pool // reusable bounded read buffers for range scans and cache misses
}

type cachedBlock struct {
	recs  []engine.LogRecord
	bytes int64
}

// OpenStatic opens path and prepares the store. Call Index to build the index.
func OpenStatic(o StaticOptions) (*StaticStore, error) {
	f, owned := o.File, false
	if f == nil {
		var err error
		if f, err = platform.Open(o.Path); err != nil {
			return nil, err
		}
		owned = true // opened here, so closed here if anything below fails
	}
	info, err := platform.IdentityOfFile(f)
	if err != nil {
		if owned {
			f.Close()
		}
		return nil, err
	}
	if o.BlockSize <= 0 {
		o.BlockSize = defaultBlockRecords
	}
	if o.CacheBlocks <= 0 {
		o.CacheBlocks = defaultCacheBlocks
	}
	if o.CacheBytes <= 0 {
		o.CacheBytes = defaultCacheBytes
	}
	if o.Limits.MaxRecordBytes <= 0 {
		o.Limits = engine.DefaultLimits()
	}
	prs := o.Profile.ParserFor(o.Sample)
	if fi, err := f.Stat(); err == nil {
		prs = parser.WithYearRef(prs, fi.ModTime())
	}
	return &StaticStore{opts: o, f: f, size: info.Size, parser: prs,
		block: o.BlockSize, cache: map[uint64]*cachedBlock{}, maxCacheBytes: o.CacheBytes}, nil
}

// Close releases the file handle.
func (s *StaticStore) Close() error { return s.f.Close() }

// Size is the file size captured at open time.
func (s *StaticStore) Size() int64 { return s.size }

// Progress reports how many bytes have been indexed and whether indexing ended.
func (s *StaticStore) Progress() (scanned, size int64, done bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scanned.Load(), s.size, s.done
}

// Index frames the whole file once, recording a checkpoint every BlockSize
// records. It is cancelable and reads with one reusable buffer.
func (s *StaticStore) Index(ctx context.Context) error {
	fr := s.opts.Profile.Framer(s.opts.Sample, s.opts.Limits, 0)
	buf := make([]byte, scanChunk)
	var (
		off     int64
		pending []checkpoint
		n       uint64 // local record counter
		end     int64
	)
	emit := func(f *framing.Frame) {
		if n%uint64(s.block) == 0 {
			pending = append(pending, checkpoint{f.Offset, f.Line})
		}
		n++
		end = f.Offset + f.Length
	}
	publish := func(done bool) {
		s.mu.Lock()
		s.cps = append(s.cps, pending...)
		s.count, s.spanEnd, s.done = n, end, done
		s.scanned.Store(off)
		s.mu.Unlock()
		pending = pending[:0]
		if s.opts.Notify != nil {
			s.opts.Notify()
		}
	}
	for off < s.size {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := int64(len(buf))
		if s.size-off < want {
			want = s.size - off
		}
		m, err := s.f.ReadAt(buf[:want], off)
		if m > 0 {
			fr.Push(buf[:m], emit)
			off += int64(m)
			publish(false)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	fr.Flush(true, emit)
	off = s.size
	publish(true)
	return nil
}

func (s *StaticStore) snapshot() (cps []checkpoint, count uint64, spanEnd int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cps, s.count, s.spanEnd
}

func (s *StaticStore) Len() int { _, c, _ := s.snapshot(); return int(c) }

func (s *StaticStore) OldestID() uint64 {
	if _, c, _ := s.snapshot(); c > 0 {
		return 1
	}
	return 0
}

func (s *StaticStore) NewestID() uint64 { _, c, _ := s.snapshot(); return c }

// indexView is one consistent snapshot of the checkpoint index: the start of
// every block and where the indexed span ends. The index is append-only, so a
// snapshot stays valid while the indexer keeps growing it.
type indexView struct {
	cps     []checkpoint
	spanEnd int64
}

// segment is a half-open run of checkpoint blocks [b0, b1) that is read and
// framed in one pass.
type segment struct{ b0, b1 uint64 }

// scanSpec describes what one scan looks for. It is shared, read-only, by every
// segment of the scan, so the functions that read segments take it as a single
// argument instead of a growing list of loose predicates and bounds.
type scanSpec struct {
	from, to uint64                       // inclusive record-ID window; 0 = unbounded
	keep     func(string) bool            // optional: reject a record on its raw text before parsing
	accept   func(*engine.LogRecord) bool // optional: the full predicate, on the parsed record
}

// sparse reports whether the scan discards records. Only then does reading on
// several goroutines pay off; a scan that delivers everything stays sequential.
func (sp *scanSpec) sparse() bool { return sp.keep != nil || sp.accept != nil }

// classify decides what to do with a parsed record: deliver it to the caller,
// and whether the scan should go on after it.
func (sp *scanSpec) classify(r *engine.LogRecord) (deliver, more bool) {
	if r.ID < sp.from {
		return false, true
	}
	if sp.to != 0 && r.ID > sp.to {
		return false, false
	}
	if sp.accept != nil && !sp.accept(r) {
		return false, true
	}
	return true, true
}

// readSegment frames blocks [b0, b1) and calls fn for every record. buf is
// reused across calls when large enough. Records retain Raw.Data only for the
// duration of the callback; callers that cache records must detach them first.
// sp.keep (optional) rejects a record on its raw bytes before it is copied and
// parsed; sp.to (0 = none) stops the scan once a rejected record passes it.
func (s *StaticStore) readSegment(ctx context.Context, ix indexView, sg segment, buf *[]byte, sp *scanSpec, fn func(*engine.LogRecord) bool) bool {
	cps, b0, b1 := ix.cps, sg.b0, sg.b1
	keep, maxID := sp.keep, sp.to
	start := cps[b0].off
	end := ix.spanEnd
	if b1 < uint64(len(cps)) {
		end = cps[b1].off
	}
	if end <= start {
		return true
	}
	// A checkpoint block can contain far more than rangeSegmentBytes (for
	// example, many large records). Stream it through the framer in bounded
	// chunks rather than allocating one buffer for the entire block.
	chunkSize := min(int64(rangeSegmentBytes), end-start)
	if chunkSize < 1 {
		return true
	}
	if int64(cap(*buf)) < chunkSize {
		*buf = make([]byte, int(chunkSize))
	}
	fr := s.opts.Profile.Framer(s.opts.Sample, s.opts.Limits, start)
	fr.Reset(start, cps[b0].line)
	id := b0*uint64(s.block) + 1
	cont := true
	var rec engine.LogRecord
	seen := 0
	emit := func(f *framing.Frame) {
		if !cont {
			return
		}
		// Rejected records never reach fn, so cancellation must be noticed here.
		if seen++; seen&63 == 0 && ctx.Err() != nil {
			cont = false
			return
		}
		// Colour codes would hide timestamps and levels from the parser, so records
		// are stripped of terminal escapes before anything looks at them. The
		// prefilter must see that same text, or it could reject real matches.
		var text string
		hasESC := bytes.IndexByte(f.Data, 0x1b) >= 0
		if hasESC {
			text = engine.StripANSI(string(f.Data))
		}
		if keep != nil && len(f.Data) > 0 {
			raw := text
			if !hasESC {
				raw = unsafe.String(unsafe.SliceData(f.Data), len(f.Data))
			}
			if !keep(raw) {
				if id++; maxID != 0 && id > maxID {
					cont = false
				}
				return
			}
		}
		if !hasESC {
			text = string(f.Data)
		}
		rec, _ = s.parser.Parse(text)
		rec.ID = id
		id++
		rec.Raw = engine.RawRecord{Offset: f.Offset, Length: f.Length, Data: text}
		rec.SourceID = s.opts.SourceID
		rec.LineStart, rec.LineEnd = f.Line, f.Line+int64(f.Lines)-1
		rec.Multiline = rec.Multiline || f.Lines > 1
		rec.Truncated = f.Truncated
		cont = fn(&rec)
	}
	for off := start; off < end && cont; {
		want := min(int64(cap(*buf)), end-off)
		data := (*buf)[:int(want)]
		n, err := s.f.ReadAt(data, off)
		if n > 0 {
			fr.Push(data[:n], emit)
			off += int64(n)
		}
		if !cont {
			return false
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false
		}
		if n == 0 {
			return false
		}
	}
	fr.Flush(true, emit)
	return cont
}

func (s *StaticStore) loadBlock(b uint64) *cachedBlock {
	s.cmu.Lock()
	if cb, ok := s.cache[b]; ok {
		s.touch(b)
		s.cmu.Unlock()
		return cb
	}
	s.cmu.Unlock()

	cps, _, spanEnd := s.snapshot()
	if b >= uint64(len(cps)) {
		return nil
	}
	cb := &cachedBlock{recs: make([]engine.LogRecord, 0, s.block)}
	scratch := s.getScratch()
	ok := s.readSegment(context.Background(), indexView{cps, spanEnd}, segment{b, b + 1}, &scratch, &scanSpec{}, func(r *engine.LogRecord) bool {
		r.DetachRaw()
		cb.bytes += int64(r.RetainedBytes())
		cb.recs = append(cb.recs, *r)
		return true
	})
	s.putScratch(scratch)
	if !ok {
		return nil
	}

	s.cmu.Lock()
	// Concurrent random-access requests may have loaded the same block while
	// the lock was released for disk I/O. Reuse the winner instead of inserting
	// duplicate LRU entries (which could evict a still-hot block prematurely).
	if existing, ok := s.cache[b]; ok {
		s.touch(b)
		s.cmu.Unlock()
		return existing
	}
	// A single oversized block is returned to the caller but not retained. This
	// keeps a few pathological records from defeating the static cache budget.
	if cb.bytes > s.maxCacheBytes {
		s.cmu.Unlock()
		return cb
	}
	for len(s.order) > 0 && (len(s.order) >= s.opts.CacheBlocks || s.cacheBytes+cb.bytes > s.maxCacheBytes) {
		oldest := s.order[0]
		if old := s.cache[oldest]; old != nil {
			s.cacheBytes -= old.bytes
			delete(s.cache, oldest)
		}
		s.order = s.order[1:]
	}
	s.cache[b] = cb
	s.order = append(s.order, b)
	s.cacheBytes += cb.bytes
	s.cmu.Unlock()
	return cb
}

// getScratch/putScratch reuse file-read buffers across sequential scans and
// cache misses. Oversized buffers are discarded so unusual records cannot make
// the pool retain unbounded amounts of memory.
func (s *StaticStore) getScratch() []byte {
	if b := s.scratchPool.Get(); b != nil {
		return b.([]byte)[:0]
	}
	return nil
}

func (s *StaticStore) putScratch(b []byte) {
	if cap(b) > 0 && cap(b) <= rangeSegmentBytes {
		s.scratchPool.Put(b[:0])
	}
}

func (s *StaticStore) touch(b uint64) {
	for i, v := range s.order {
		if v == b {
			copy(s.order[i:], s.order[i+1:])
			s.order[len(s.order)-1] = b
			return
		}
	}
}

// Get returns record id (1-based).
func (s *StaticStore) Get(id uint64) (engine.LogRecord, bool) {
	_, count, _ := s.snapshot()
	if id == 0 || id > count {
		return engine.LogRecord{}, false
	}
	b := (id - 1) / uint64(s.block)
	cb := s.loadBlock(b)
	if cb == nil {
		return engine.LogRecord{}, false
	}
	i := int((id - 1) % uint64(s.block))
	if i >= len(cb.recs) {
		return engine.LogRecord{}, false
	}
	return cb.recs[i], true
}

// Range scans sequentially in large reads without polluting the block cache,
// so searches and filters over huge files stay cheap on RAM. The *LogRecord
// passed to fn is reused; copy it if you need to keep it.
func (s *StaticStore) Range(from, to uint64, fn func(*engine.LogRecord) bool) {
	s.RangeMatching(context.Background(), from, to, nil, nil, fn)
}

// RangeRaw is Range with a raw-text prefilter only; see engine.MatchRanger.
func (s *StaticStore) RangeRaw(from, to uint64, keep func(string) bool, fn func(*engine.LogRecord) bool) {
	s.RangeMatching(context.Background(), from, to, keep, nil, fn)
}

// RangeMatching implements engine.MatchRanger. Scans whose results are sparse
// (a keep or accept predicate is given) run on several goroutines when more
// than one CPU is available; scans that deliver every record stay sequential,
// because ordered delivery would then cost more in buffering than it gains.
func (s *StaticStore) RangeMatching(ctx context.Context, from, to uint64, keep func(string) bool, accept func(*engine.LogRecord) bool, fn func(*engine.LogRecord) bool) {
	cps, count, spanEnd := s.snapshot()
	if count == 0 {
		return
	}
	if from < 1 {
		from = 1
	}
	if to == 0 || to > count {
		to = count
	}
	if from > to {
		return
	}
	bs := uint64(s.block)
	b := (from - 1) / bs
	last := (to - 1) / bs
	// Cut the block range into read segments of about rangeSegmentBytes.
	ix := indexView{cps, spanEnd}
	var segs []segment
	for b <= last {
		e := b + 1
		for e <= last { // grow the segment while it stays within the read budget
			endOff := spanEnd
			if e+1 < uint64(len(cps)) {
				endOff = cps[e+1].off
			}
			if endOff-cps[b].off > rangeSegmentBytes {
				break
			}
			e++
		}
		segs = append(segs, segment{b, e})
		b = e
	}
	sp := &scanSpec{from: from, to: to, keep: keep, accept: accept}
	if sp.sparse() {
		if w := s.scanWorkers(len(segs)); w > 1 {
			s.rangeParallel(ctx, ix, segs, w, sp, fn)
			return
		}
	}
	buf := s.getScratch()
	defer func() { s.putScratch(buf) }()
	for _, sg := range segs {
		if ctx.Err() != nil {
			return
		}
		ok := s.readSegment(ctx, ix, sg, &buf, sp, func(r *engine.LogRecord) bool {
			deliver, more := sp.classify(r)
			if !deliver {
				return more
			}
			return fn(r)
		})
		if !ok {
			return
		}
	}
}

// ScanWindow suggests how many records a caller that scans backwards in
// windows should cover per call: about one read segment per worker, so each
// window can use the parallel path, but never fewer than minRecords. It is
// derived from the average record size indexed so far.
func (s *StaticStore) ScanWindow(minRecords uint64) uint64 {
	_, count, spanEnd := s.snapshot()
	if count == 0 || spanEnd <= 0 {
		return minRecords
	}
	avg := uint64(spanEnd) / count
	if avg < 1 {
		avg = 1
	}
	w := uint64(s.scanWorkers(1 << 30))
	n := w * rangeSegmentBytes / avg
	// whole checkpoint blocks, so windows align with segment boundaries
	if bs := uint64(s.block); bs > 0 {
		n = n / bs * bs
	}
	return max(n, minRecords)
}

// scanWorkers decides how many goroutines a Range over n segments may use.
func (s *StaticStore) scanWorkers(n int) int {
	w := s.opts.ScanWorkers
	if w <= 0 {
		w = min(runtime.GOMAXPROCS(0), 8)
	}
	return min(w, n)
}

// Result slices are pooled across scans. Without recycling every segment
// regrows its slice by doubling (re-copying and re-zeroing megabytes), which made
// delivering many records through the parallel path several times slower than a
// sequential scan. The pool is dropped by the GC when idle; very large slices are
// not kept so one segment of tiny records cannot pin tens of megabytes.
var recSlicePool sync.Pool

const maxPooledRecs = 1 << 16

func getRecSlice() []engine.LogRecord {
	if p, ok := recSlicePool.Get().(*[]engine.LogRecord); ok {
		return (*p)[:0]
	}
	return nil
}

func putRecSlice(r []engine.LogRecord) {
	if cap(r) == 0 || cap(r) > maxPooledRecs {
		return
	}
	recSlicePool.Put(&r)
}

type segResult struct {
	recs []engine.LogRecord
	ok   bool
}

// rangeParallel scans segments on several goroutines but delivers records to fn
// in file order and on the calling goroutine, so callers keep Range's
// single-threaded callback contract. Only a bounded window of segments is in
// flight, and scanning stops promptly once fn returns false.
func (s *StaticStore) rangeParallel(ctx context.Context, ix indexView, segs []segment, workers int, sp *scanSpec, fn func(*engine.LogRecord) bool) {
	var stop atomic.Bool
	// Workers watch a derived context so that they also stop promptly when the
	// consumer returns early (fn returned false), not only on caller cancel.
	wctx, wcancel := context.WithCancel(ctx)
	defer wcancel()
	results := make([]chan segResult, len(segs))
	for i := range results {
		results[i] = make(chan segResult, 1)
	}
	window := make(chan struct{}, workers+1) // segments started but not yet consumed
	jobs := make(chan int)
	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := s.getScratch()
			defer func() { s.putScratch(buf) }()
			for i := range jobs {
				out := getRecSlice()

				hint := 0
				if s.block > 0 && s.block <= maxPooledRecs {
					blocks := segs[i].b1 - segs[i].b0
					maxBlocks := uint64(maxPooledRecs / s.block)
					if blocks > maxBlocks {
						blocks = maxBlocks
					}

					hint = int(blocks) * s.block
				}

				ok := s.readSegment(wctx, ix, segs[i], &buf, sp, func(r *engine.LogRecord) bool {
					if stop.Load() {
						return false
					}
					deliver, more := sp.classify(r)
					if deliver {
						if len(out) == 0 && hint > cap(out) {
							out = make([]engine.LogRecord, 0, hint)
						}
						out = append(out, *r)
					}

					return more
				})
				results[i] <- segResult{recs: out, ok: ok}
			}
		}()
	}
	wg.Add(1)
	go func() { // dispatcher
		defer wg.Done()
		defer close(jobs)
		for i := range segs {
			select {
			case window <- struct{}{}:
			case <-stopCh:
				return
			}
			select {
			case jobs <- i:
			case <-stopCh:
				return
			}
		}
	}()
	defer func() {
		stop.Store(true)
		wcancel() // before Wait: workers blocked in a long scan must see it
		close(stopCh)
		wg.Wait()
	}()
	for i := range segs {
		var r segResult
		select {
		case r = <-results[i]:
		case <-ctx.Done():
			return
		}
		<-window
		for j := range r.recs {
			if !fn(&r.recs[j]) {
				return
			}
		}
		putRecSlice(r.recs)
		if !r.ok {
			return
		}
	}
}

// Position returns a payload-free static locator for a logical record. The
// cached record supplies the exact byte offset and length; only those numbers
// and the source identity are retained by the position.
func (s *StaticStore) Position(id uint64) (engine.SourcePosition, bool) {
	r, ok := s.Get(id)
	if !ok || s.opts.SourceID == "" || r.Raw.Length <= 0 {
		return engine.SourcePosition{}, false
	}
	return engine.SourcePosition{SourceID: s.opts.SourceID, Kind: engine.PositionStaticOffset, RecordID: id, Offset: r.Raw.Offset, Length: r.Raw.Length}, true
}

// ResolvePosition resolves a static position by re-reading the corresponding
// logical record. Source identity and locator kind are validated first.
func (s *StaticStore) ResolvePosition(p engine.SourcePosition) (engine.LogRecord, bool) {
	if p.Kind != engine.PositionStaticOffset || p.SourceID == "" || p.SourceID != s.opts.SourceID || p.Offset < 0 || p.Length <= 0 {
		return engine.LogRecord{}, false
	}
	r, ok := s.Get(p.RecordID)
	if !ok || r.Raw.Offset != p.Offset || r.Raw.Length != p.Length {
		return engine.LogRecord{}, false
	}
	return r, true
}

// Raw reads the original bytes of a record from disk on demand. Static cached
// records intentionally do not retain Raw.Data.
func (s *StaticStore) Raw(r *engine.LogRecord) (string, error) {
	n := r.Raw.Length
	if n <= 0 {
		return r.Raw.Data, nil
	}
	clipped := n > maxRawBytes
	if clipped {
		n = maxRawBytes
	}
	buf := make([]byte, n)
	m, err := s.f.ReadAt(buf, r.Raw.Offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return r.Raw.Data, err
	}
	if m == 0 {
		return "", nil
	}
	out := strings.TrimRight(unsafe.String(&buf[0], m), "\r\n") // buf is never reused: no copy needed
	if clipped {
		out += fmt.Sprintf("\n… record continues for %d more bytes (showing first %d MiB)", int64(r.Raw.Length)-int64(maxRawBytes), maxRawBytes>>20)
	}
	return out, nil
}

var _ engine.RecordStore = (*StaticStore)(nil)
