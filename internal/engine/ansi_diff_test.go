package engine

import (
	"math/rand"
	"strings"
	"testing"
)

// stripANSIRef is the original byte-at-a-time implementation, kept as the
// specification the faster StripANSI must agree with.
func stripANSIRef(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != 0x1b {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[':
			j := i + 2
			for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
				j++
			}
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
				j++
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
				j++
			}
			i = j
		case ']', 'P', 'X', '^', '_':
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 {
					j++
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		default:
			j := i + 1
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
				j++
			}
			if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7e {
				j++
			}
			i = j
		}
	}
	return b.String()
}

func TestStripANSIMatchesReference(t *testing.T) {
	alphabet := []byte("ab 0;[]\\\x1b\x07m(B?PX^_~\xc3\xa9\n1")
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 200000; n++ {
		buf := make([]byte, rng.Intn(24))
		for i := range buf {
			buf[i] = alphabet[rng.Intn(len(alphabet))]
		}
		if got, want := StripANSI(string(buf)), stripANSIRef(string(buf)); got != want {
			t.Fatalf("input %q: got %q want %q", buf, got, want)
		}
	}
}

const coloredLine = "\x1b[32m2026-09-19 08:00:00.000\x1b[0m \x1b[1;31mERROR\x1b[0m [worker-7] client disconnected while reading request body (item 12345, 659ms)\x1b[0m"

func BenchmarkStripANSIColored(b *testing.B) {
	b.SetBytes(int64(len(coloredLine)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = StripANSI(coloredLine)
	}
}
