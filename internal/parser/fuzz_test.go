package parser

import (
	"strings"
	"testing"
	"time"
)

var refTime = time.Date(2022, 3, 10, 12, 0, 0, 0, time.UTC)

// Log content is untrusted: no parser may panic or return out-of-range data,
// whatever bytes it is given.
func FuzzParsers(f *testing.F) {
	for _, s := range []string{
		`{"time":"2026-10-15T10:00:00.123+05:30","level":"warn","msg":"a\/b \ud83d\ude00","n":[1,{"x":"y"}]}`,
		`2026-10-15 10:00:00,123 ERROR [main] boom`,
		`Sep 19 08:26:10 host sshd[1]: Failed password`,
		`E0920 11:32:04.123456 1 f.go:1] x`,
		`{"ts":1758189600123,"level":50,"message":"x"`, `{`, `[`, ``, "\x00\xff",
		`{"a":"` + strings.Repeat(`\u`, 50),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, p := range []Parser{Generic{}, Generic{Ref: refTime}, JSON{}} {
			rec, _ := p.Parse(s)
			if len(rec.Message) > len(s)*4+16 { // unescaping never grows 4x
				t.Fatalf("%s: message %d bytes from %d-byte input", p.Name(), len(rec.Message), len(s))
			}
		}
		Field(s, "msg")
		Field(s, "a.b")
		ParseTimestampPrefix(s)
	})
}
