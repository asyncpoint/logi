// Package ncache is the built-in, compiled-in NCache log support.
//
// Two fixed-width table formats are understood, each with a header line at the
// top of the file; the column layout is read from that header:
//
//	ncache         cache log    TIMESTAMP APPDOMAIN LOGGERNAME THREADNAME LEVEL MESSAGE
//	ncache-events  event log    TIMESTAMP SOURCE EVENTID LEVEL MESSAGE
//
// In the cache log the MESSAGE column holds an event name followed by the text.
// The event log writes milliseconds as "06:22:56, 110", levels as Windows event
// types (Information/Warning/Error/...), and an "END" line after some entries.
// Continuation lines (stack traces) do not start with a timestamp.
package ncache

import (
	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/framing"
	"github.com/asyncpoint/logi/internal/parser"
)

// Profile names, as used by --parser.
const (
	Name       = "ncache"        // cache log
	EventsName = "ncache-events" // event log
)

// Plugin registers the NCache profiles.
type Plugin struct{}

func New() *Plugin { return &Plugin{} }

func (*Plugin) Name() string           { return Name }
func (*Plugin) Description() string    { return "Alachisoft NCache cache logs and event logs" }
func (*Plugin) EnabledByDefault() bool { return true }

// The columns mirror each file's own header order. The app domain is constant on
// almost every line, so it starts hidden (x / palette shows it).
var cacheColumns = []parser.ColumnSpec{
	{Name: "time", Title: "time"},
	{Name: "appdomain", Title: "appdomain", Width: 28, Hidden: true},
	{Name: "logger", Title: "logger", Width: 14},
	{Name: "thread", Title: "thread", Width: 14},
	{Name: "level", Title: "level"},
	{Name: "event", Title: "event", Width: 30, Mixed: true}, // also shown when merged with other formats
	{Name: "source", Title: "source", Hidden: true},
	{Name: "message", Title: "message"},
}

var eventColumns = []parser.ColumnSpec{
	{Name: "time", Title: "time"},
	{Name: "component", Title: "component", Width: 18},
	{Name: "eventid", Title: "eventid", Width: 7},
	{Name: "level", Title: "level"},
	{Name: "source", Title: "source", Hidden: true},
	{Name: "message", Title: "message"},
}

func (*Plugin) Register(r *parser.Registry) error {
	if err := r.Register(parser.Profile{
		Name:        Name,
		Description: "NCache cache log (columnar header, level, logger, thread, event, multiline exceptions)",
		Detect:      detectCache,
		NewParser:   func() parser.Parser { return Parser{} },
		NewParserFor: func(sample []byte) parser.Parser {
			return Parser{lay: layoutFromSample(sample, cacheLogLayout)}
		},
		Columns: cacheColumns,
		NewFramer: func(_ []byte, lim engine.Limits, offset int64) framing.Framer {
			f := framing.NewMultilineFramer(lim, offset, isStart)
			f.SetSkip(isHeader)           // the column header is not a record
			f.SetTerminator(isTerminator) // some exports end the file with a trailing "END" line
			return f
		},
	}); err != nil {
		return err
	}
	return r.Register(parser.Profile{
		Name:        EventsName,
		Description: "NCache event log (source, event id, Windows event levels)",
		Detect:      detectEvents,
		NewParser:   func() parser.Parser { return Parser{name: EventsName, lay: eventLogLayout} },
		NewParserFor: func(sample []byte) parser.Parser {
			return Parser{name: EventsName, lay: layoutFromSample(sample, eventLogLayout)}
		},
		Columns: eventColumns,
		NewFramer: func(_ []byte, lim engine.Limits, offset int64) framing.Framer {
			f := framing.NewMultilineFramer(lim, offset, isStart)
			f.SetSkip(isHeader)
			f.SetTerminator(isTerminator) // "END" closes an entry and is not part of it
			return f
		},
	})
}

var _ parser.Plugin = (*Plugin)(nil)
