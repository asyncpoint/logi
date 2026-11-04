// Package parser normalizes framed records into engine.LogRecord.
// Parsers extract only what the list view needs; deeper interpretation is
// deferred until a record is inspected.
package parser

import "github.com/asyncpoint/logi/internal/engine"

// Parser converts the text of one logical record. It must never panic and must
// always return a usable record: on failure it returns the raw text as the
// message together with a non-nil error (data is never dropped).
type Parser interface {
	Name() string
	Parse(text string) (engine.LogRecord, error)
}
