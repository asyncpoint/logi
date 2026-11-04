// Package engine contains the domain model and bounded storage of logi.
// It has no dependency on the TUI and does no I/O.
package engine

import (
	"strings"
)

// Severity is a normalized log level.
type Severity uint8

const (
	SevUnknown Severity = iota
	SevTrace
	SevDebug
	SevInfo
	SevNotice
	SevWarn
	SevError
	SevCritical
	SevFatal

	NumSeverities = int(SevFatal) + 1
)

var sevNames = [...]string{"", "TRACE", "DEBUG", "INFO", "NOTICE", "WARN", "ERROR", "CRIT", "FATAL"}

// String returns the short upper-case label ("" for unknown).
func (s Severity) String() string {
	if int(s) < len(sevNames) {
		return sevNames[s]
	}
	return ""
}

// levelTable maps every level spelling seen in common logging stacks to a
// normalized Severity. Keys are upper case.
//
//	log4j/logback/slf4j  TRACE DEBUG INFO WARN ERROR FATAL
//	java.util.logging    FINEST FINER FINE CONFIG INFO WARNING SEVERE
//	.NET / MEL           TRCE DBUG INFO WARN FAIL CRIT      Serilog VRB DBG INF WRN ERR FTL
//	Python / loguru      DEBUG INFO SUCCESS WARNING ERROR CRITICAL EXCEPTION
//	logrus / zap / slog  TRAC DEBU INFO WARN ERRO FATA PANI DPANIC
//	syslog               DEBUG INFO NOTICE WARNING ERR CRIT ALERT EMERG PANIC
//	Windows event log    Information Warning Error Critical Verbose SuccessAudit FailureAudit
var levelTable = map[string]Severity{
	"TRACE": SevTrace, "TRC": SevTrace, "TRCE": SevTrace, "TRAC": SevTrace, "VERBOSE": SevTrace,
	"VERB": SevTrace, "VRB": SevTrace, "FINEST": SevTrace, "FINER": SevTrace,

	"DEBUG": SevDebug, "DBG": SevDebug, "DEB": SevDebug, "DEBU": SevDebug, "DBUG": SevDebug,
	"FINE": SevDebug, "CONFIG": SevDebug,

	"INFO": SevInfo, "INF": SevInfo, "INFORMATION": SevInfo, "INFORMATIONAL": SevInfo,

	"NOTICE": SevNotice, "NOTE": SevNotice, "SUCCESS": SevNotice, "SUCCESSAUDIT": SevNotice, "AUDITSUCCESS": SevNotice,

	"WARN": SevWarn, "WRN": SevWarn, "WARNING": SevWarn,

	"ERROR": SevError, "ERR": SevError, "ERRO": SevError, "SEVERE": SevError, "FAIL": SevError,
	"FAILURE": SevError, "EXCEPTION": SevError, "FAILUREAUDIT": SevError, "AUDITFAILURE": SevError,

	"CRITICAL": SevCritical, "CRIT": SevCritical, "ALERT": SevCritical, "DPANIC": SevCritical,

	"FATAL": SevFatal, "FATA": SevFatal, "FTL": SevFatal, "PANIC": SevFatal, "PANI": SevFatal,
	"EMERG": SevFatal, "EMERGENCY": SevFatal,
}

// ParseSeverity maps a level token (any case) to a Severity without allocating.
// Single letters are not accepted here (too ambiguous); see ParseSeverityLetter.
func ParseSeverity(tok string) Severity {
	if len(tok) < 3 || len(tok) > 13 {
		return SevUnknown
	}
	var buf [13]byte
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		buf[i] = c
	}
	return levelTable[string(buf[:len(tok)])] // map lookup with string(b) does not allocate
}

// ParseSeverityLetter maps the one-letter levels used by glog/klog and
// bracketed forms like "[E]": T/V trace, D debug, I info, N notice, W warn,
// E error, C critical, F fatal.
func ParseSeverityLetter(c byte) Severity {
	switch c {
	case 'T', 't', 'V', 'v':
		return SevTrace
	case 'D', 'd':
		return SevDebug
	case 'I', 'i':
		return SevInfo
	case 'N', 'n':
		return SevNotice
	case 'W', 'w':
		return SevWarn
	case 'E', 'e':
		return SevError
	case 'C', 'c':
		return SevCritical
	case 'F', 'f':
		return SevFatal
	}
	return SevUnknown
}

// AllSeverities lists every known level from least to most severe (excluding
// SevUnknown), for legends and help.
func AllSeverities() []Severity {
	return []Severity{SevTrace, SevDebug, SevInfo, SevNotice, SevWarn, SevError, SevCritical, SevFatal}
}

// RawRecord locates the original bytes of a record.
// Static records carry only Offset/Length and are read lazily; live records
// keep their text in Data because the source bytes must survive the ring.
type RawRecord struct {
	Offset int64
	Length int64
	Data   string // populated for live records and transient static scans
}

// PositionKind identifies the storage locator used by a SourcePosition.
type PositionKind uint8

const (
	PositionInvalid PositionKind = iota
	PositionLiveID
	PositionStaticOffset
)

// SourcePosition is a lightweight, payload-free identity for a record. It is
// safe to retain in query results and bookmark collections. Live positions can
// become stale after ring eviction; static positions resolve against the
// original file while that source remains open.
type SourcePosition struct {
	SourceID string
	Kind     PositionKind
	RecordID uint64
	Offset   int64
	Length   int64
}

// IsZero reports whether p does not identify a record.
func (p SourcePosition) IsZero() bool { return p.Kind == PositionInvalid || p.SourceID == "" }

// LogRecord is the normalized record produced by the parser stage.
type LogRecord struct {
	ID        uint64
	Timestamp Timestamp // wall-clock as written in the log; zero if absent
	Severity  Severity
	Message   string // substring of the raw text where possible (no copy)
	Fields    Fields // nil unless the parser produced structured fields
	Raw       RawRecord

	SourceID  string
	LineStart int64 // 1-based physical line (relative to first line read in live mode)
	LineEnd   int64

	Multiline bool
	Truncated bool
	ParseErr  bool // parser fell back to a raw representation
}

// DetachRaw converts a parsed record into a compact disk-backed representation.
// It copies semantic strings that may otherwise retain the full raw-record
// backing string, then releases Raw.Data. Static navigation caches use this
// before retaining parsed records; live records deliberately keep Raw.Data.
func (r *LogRecord) DetachRaw() {
	if r.Raw.Data == "" {
		return
	}
	r.Message = strings.Clone(r.Message)
	for i := range r.Fields {
		r.Fields[i].Key = strings.Clone(r.Fields[i].Key)
		r.Fields[i].Value = strings.Clone(r.Fields[i].Value)
	}
	r.Raw.Data = ""
}

// RetainedBytes returns a conservative estimate of the heap retained by this record.
//
// The ring uses this estimate for its byte budget, so it intentionally counts
// record-owned strings and structured-field container overhead rather than only
// the raw payload. Some parser strings may share the raw record backing store;
// counting those payloads again is deliberate because Go does not expose string
// backing-storage ownership without unsafe. A conservative overestimate keeps
// the configured byte budget from silently turning into an unbounded heap budget.
//
// This is still an estimate: Go map bucket layout and allocator size classes are
// runtime implementation details.
func (r *LogRecord) RetainedBytes() int {
	const (
		recordOverhead = 192
		stringHeader   = 16 // Go string header on supported 64-bit targets.
	)

	sz := recordOverhead + len(r.Raw.Data) + len(r.SourceID)
	if r.Raw.Data != "" {
		sz += stringHeader
	}
	if r.SourceID != "" {
		sz += stringHeader
	}
	if r.Message != "" {
		sz += stringHeader + len(r.Message)
	}
	if r.Fields != nil {
		// Slice header + one compact FieldValue per field. FieldValue contains
		// two string headers and one bool, with no map bucket or interface value.
		sz += 24 + len(r.Fields)*40
		for _, f := range r.Fields {
			sz += stringHeader + len(f.Key)
			sz += stringHeader + len(f.Value)
		}
	}
	return sz
}

// FirstLine returns s up to the first newline and the number of further lines.
func FirstLine(s string) (string, int) {
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return s, 0
	}
	return strings.TrimRight(s[:i], "\r"), strings.Count(s[i+1:], "\n") + 1
}

// Limits protects the engine against pathological records.
type Limits struct {
	MaxRecordBytes int // hard cap on retained bytes per logical record
	MaxLines       int // hard cap on physical lines per multiline record
}

// DefaultLimits are conservative defaults suited to typical application logs.
func DefaultLimits() Limits { return Limits{MaxRecordBytes: 64 << 10, MaxLines: 2000} }

// RawJSON is a top-level JSON value kept as its original text. It shares
// memory with the record instead of being decoded.
type RawJSON string
