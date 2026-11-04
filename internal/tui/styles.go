package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"github.com/asyncpoint/logi/internal/engine"
)

// Colours use the terminal's own 16-colour palette so logi follows the user's
// theme on dark and light backgrounds; downsampling and NO_COLOR are handled by
// the renderer, and --color always/never override detection.
var (
	stDim      = lipgloss.NewStyle().Faint(true)
	stHeader   = lipgloss.NewStyle().Bold(true).Reverse(true)
	stStatus   = lipgloss.NewStyle().Reverse(true)
	stBadge    = lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(lipgloss.Color("2"))
	stBadgeLv  = lipgloss.NewStyle().Bold(true).Reverse(true).Foreground(lipgloss.Color("6"))
	stHit      = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("3")).Foreground(lipgloss.Color("0"))
	stCursor   = lipgloss.NewStyle().Reverse(true)
	stPrompt   = lipgloss.NewStyle().Bold(true)
	stErrText  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	stOverlay  = lipgloss.NewStyle().Bold(true).Underline(true)
	stSelected = lipgloss.NewStyle().Reverse(true)
	stColHead  = lipgloss.NewStyle().Bold(true).Underline(true)
	stColSel   = lipgloss.NewStyle().Bold(true).Reverse(true)
)

// theme styles one severity: the level label, the message text, and the
// metadata columns (id/time/source) of the same row.
type theme struct {
	label, msg, meta lipgloss.Style
}

var themes [engine.NumSeverities]theme

func init() {
	c := func(n string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(n)) }
	plain := lipgloss.NewStyle()
	faint := lipgloss.NewStyle().Faint(true)
	themes[engine.SevUnknown] = theme{plain, plain, faint}
	themes[engine.SevTrace] = theme{faint, faint, faint}                                            // dim
	themes[engine.SevDebug] = theme{c("12"), faint, faint}                                          // blue label, dim text
	themes[engine.SevInfo] = theme{c("2"), plain, faint}                                            // green label
	themes[engine.SevNotice] = theme{c("6").Bold(true), plain, faint}                               // cyan label
	themes[engine.SevWarn] = theme{c("3").Bold(true), c("3"), c("3").Faint(true)}                   // yellow row
	themes[engine.SevError] = theme{c("9").Bold(true), c("9"), c("9").Faint(true)}                  // red row
	themes[engine.SevCritical] = theme{c("13").Bold(true), c("13").Bold(true), c("13").Faint(true)} // magenta row
	themes[engine.SevFatal] = theme{                                                                // white on red label, bold red text
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("1")),
		c("9").Bold(true), c("9").Faint(true)}
}

// sourcePaletteCodes colours the source column per file when several files are
// merged (--dir-mode all), so interleaved lines can be told apart at a glance,
// like docker compose does for services. They are terminal palette indexes, so
// they follow the user's theme and degrade with the colour profile like the
// rest of the UI. Red (1, 9) is left out on purpose: it means "error" here.
// Five hues come first (cyan, green, magenta, blue, yellow); beyond five files
// the bright variants follow, and after ten the colours repeat (the file name
// still tells them apart).
var sourcePaletteCodes = []string{"6", "2", "5", "12", "3", "14", "10", "13", "11", "4"}

var sourcePalette = func() []lipgloss.Style {
	out := make([]lipgloss.Style, len(sourcePaletteCodes))
	for i, code := range sourcePaletteCodes {
		out[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(code))
	}
	return out
}()

// sourceStyleFor returns the style of a file slot (see app.Session.SourceSlot).
func sourceStyleFor(slot int) lipgloss.Style {
	n := len(sourcePalette)
	return sourcePalette[((slot%n)+n)%n]
}

// sourceGutterBar is the bar drawn in the gutter cell in a file's colour; on a
// bookmarked row the same cell shows the bookmark star, in the same colour.
const sourceGutterBar = "▌"

// gutterGlyph is what the bookmark cell shows when the file-colour bar is on: the
// star on a bookmarked row (keeping the file's colour), else the bar. With
// markers enabled and more files than colours, rows of repeated colours show
// their lap's shape instead of the bar, since the source column that would
// carry it is hidden.
func gutterGlyph(slot int, bookmarked, markers bool) string {
	n := len(sourcePalette)
	switch {
	case bookmarked:
		return "★"
	case markers && slot >= n:
		return sourceMarkers[(slot/n-1)%len(sourceMarkers)]
	}
	return sourceGutterBar
}

// sourceMarkers tell apart files that share a colour. With more files than
// palette colours, the first lap of files is unmarked and each later lap gets its
// own shape (second lap ◆, third ▲, fourth ■, fifth ●, then the shapes repeat).
var sourceMarkers = []string{"◆", "▲", "■", "●"}

// sourceMarkerCell is the two-cell marker (shape and a space, or two spaces for
// the first lap) shown before a file name when files outnumber colours.
func sourceMarkerCell(slot int) string {
	n := len(sourcePalette)
	if slot < n {
		return "  "
	}
	return sourceMarkers[(slot/n-1)%len(sourceMarkers)] + " "
}

func themeFor(s engine.Severity) theme {
	if int(s) < len(themes) {
		return themes[s]
	}
	return themes[engine.SevUnknown]
}

func sevStyle(s engine.Severity) lipgloss.Style { return themeFor(s).label }

// badge styles for the status-bar level counters (solid blocks that stand out
// against the reverse-video bar).
var badgeStyles = map[engine.Severity]lipgloss.Style{
	engine.SevFatal:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("1")),
	engine.SevCritical: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("5")),
	engine.SevError:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("9")),
	engine.SevWarn:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3")),
}

func sevLabel(s engine.Severity) string {
	switch s {
	case engine.SevUnknown:
		return "     "
	case engine.SevNotice:
		return "NOTE "
	}
	return fmt.Sprintf("%-5s", s.String())
}

// levelAliases documents which spellings normalise to each level (help legend).
var levelAliases = map[engine.Severity]string{
	engine.SevTrace:    "TRACE TRC TRCE VRB VERBOSE FINER FINEST",
	engine.SevDebug:    "DEBUG DBG DBUG DEBU FINE CONFIG",
	engine.SevInfo:     "INFO INF INFORMATION",
	engine.SevNotice:   "NOTICE NOTE SUCCESS",
	engine.SevWarn:     "WARN WARNING WRN",
	engine.SevError:    "ERROR ERR ERRO FAIL SEVERE EXCEPTION",
	engine.SevCritical: "CRITICAL CRIT ALERT DPANIC",
	engine.SevFatal:    "FATAL FATA FTL PANIC EMERG",
}

func commas(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
