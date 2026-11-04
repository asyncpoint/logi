package framing

import (
	"bytes"

	"github.com/asyncpoint/logi/internal/engine"
)

// Detect chooses a framer from a small initial sample. Files whose
// records mostly begin with a timestamp are framed as multiline records with
// timestamp starts; everything else is framed line by line.
func Detect(sample []byte, lim engine.Limits, offset int64) Framer {
	if MultilineLikely(sample) {
		return NewMultilineFramer(lim, offset, LooksLikeTimestamp)
	}
	return NewLineFramer(lim, offset)
}

// MultilineLikely inspects at most ~256 lines of sample.
func MultilineLikely(sample []byte) bool {
	var ts, total int
	for n := 0; n < 256 && len(sample) > 0; n++ {
		i := bytes.IndexByte(sample, '\n')
		var line []byte
		if i < 0 {
			line, sample = sample, nil
		} else {
			line, sample = sample[:i], sample[i+1:]
		}
		line = trimCR(line)
		if len(line) == 0 {
			continue
		}
		total++
		if LooksLikeTimestamp(line) {
			ts++
		}
	}
	return ts >= 1 && total > 0 && float64(ts)/float64(total) >= 0.2
}
