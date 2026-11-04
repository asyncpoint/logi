package parser

import (
	"strings"

	"github.com/asyncpoint/logi/internal/engine"
	"github.com/asyncpoint/logi/internal/framing"
)

// CRI is the container runtime log format that Kubernetes nodes keep under
// /var/log/pods (and containerd, CRI-O write): one line per record,
//
//	2026-10-15T10:00:00.123456789Z stderr F the application's own line
//
// with the stream (stdout or stderr) and a flag, F for a full line or P for a
// partial one. The time comes from the runtime, the level from the application's
// line, which may itself be plain text or a JSON object.
type CRI struct{}

func (CRI) Name() string { return "cri" }

// criHeader splits "<time> <stream> <flag> " off the front of line.
func criHeader(line string) (ts int, stream, flag string, rest string, ok bool) {
	_, n, ok := ParseTimestampPrefix(line)
	if !ok {
		return 0, "", "", "", false
	}
	r := strings.TrimLeft(line[n:], " ")
	switch {
	case strings.HasPrefix(r, "stdout "):
		stream, r = "stdout", r[len("stdout "):]
	case strings.HasPrefix(r, "stderr "):
		stream, r = "stderr", r[len("stderr "):]
	default:
		return 0, "", "", "", false
	}
	if len(r) == 0 || (r[0] != 'F' && r[0] != 'P') || (len(r) > 1 && r[1] != ' ') {
		return 0, "", "", "", false
	}
	flag = r[:1]
	if len(r) > 1 {
		r = r[2:]
	} else {
		r = ""
	}
	return n, stream, flag, r, true
}

func (CRI) Parse(text string) (engine.LogRecord, error) {
	n, stream, flag, rest, ok := criHeader(text)
	if !ok {
		return Generic{}.Parse(text)
	}
	t, _, _ := ParseTimestampPrefix(text[:n])
	var inner engine.LogRecord
	if strings.HasPrefix(strings.TrimSpace(rest), "{") {
		inner, _ = JSON{}.Parse(strings.TrimSpace(rest))
	} else {
		inner, _ = Generic{}.Parse(rest)
	}
	rec := engine.LogRecord{
		Timestamp: engine.TimestampOf(t),
		Severity:  inner.Severity,
		Message:   inner.Message,
		Fields:    engine.Fields{{Key: "stream", Value: stream}, {Key: "flag", Value: flag}},
	}
	return rec, nil
}

func detectCRI(sample []byte) float64 {
	var match, total int
	for _, line := range strings.Split(string(sample), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if total++; total > 64 {
			break
		}
		if _, _, _, _, ok := criHeader(line); ok {
			match++
		}
	}
	if total == 0 {
		return 0
	}
	return 0.97 * float64(match) / float64(total)
}

// CRIProfile is the container runtime (CRI) log format.
func CRIProfile() Profile {
	return Profile{
		Name:        "cri",
		Description: "Kubernetes/containerd container log files (<time> stdout|stderr F|P <line>)",
		Detect:      detectCRI,
		NewParser:   func() Parser { return CRI{} },
		NewFramer: func(_ []byte, lim engine.Limits, offset int64) framing.Framer {
			return framing.NewLineFramer(lim, offset)
		},
	}
}
