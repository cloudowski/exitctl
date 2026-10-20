// Package terminal renders exitctl results for a terminal: the scan summary
// (contracts/cli.md "exitctl scan") and the shared style primitives (color,
// emoji, bars, width math). Color and emoji only reinforce information; the
// plain-text output carries all of it (constitution principle VIII).
package terminal

import (
	"io"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Options controls terminal presentation.
type Options struct {
	// Color enables ANSI colors (reinforcement only).
	Color bool
	// Emoji enables emoji markers (recommended alternative: "⭐️").
	Emoji bool
	// UTF8 enables Unicode bars and separators; false uses ASCII fallbacks.
	UTF8 bool
	// All shows every anchor instead of the top 5, without the 24-line cap.
	All bool
	// Wide prints full anchor addresses and every alternative regardless of
	// the terminal width, and prefixes addresses shared by several anchors
	// with the directory part that tells them apart.
	Wide bool
	// Width is the terminal width in columns; 0 means unknown (80 assumed).
	Width int
	// Names maps service IDs and alternative IDs to display names. Optional;
	// IDs are printed as-is when no name is known.
	Names map[string]string
}

// DetectOptions derives Options from the flags and the environment (research
// R11). noColor is the --no-color flag; emoji is the --emoji flag value
// (auto, always, never; anything else is treated as auto).
func DetectOptions(noColor bool, emoji string, out io.Writer) Options {
	return detectOptions(noColor, emoji, isTerminal(out), os.Getenv)
}

func detectOptions(noColor bool, emoji string, tty bool, getenv func(string) string) Options {
	termName := getenv("TERM")
	limitedTerm := termName == "linux" || termName == "dumb"
	utf8 := utf8Locale(getenv)
	o := Options{UTF8: utf8, Width: 80}
	_, noColorEnv := lookup(getenv, "NO_COLOR")
	o.Color = !noColor && !noColorEnv && tty && termName != "dumb"
	switch emoji {
	case "always":
		o.Emoji = true
	case "never":
		o.Emoji = false
	default:
		o.Emoji = utf8 && !limitedTerm
	}
	return o
}

// lookup treats a non-empty value as set (NO_COLOR convention).
func lookup(getenv func(string) string, key string) (string, bool) {
	v := getenv(key)
	return v, v != ""
}

// utf8Locale reports whether the effective locale is UTF-8. LC_ALL overrides
// LC_CTYPE, which overrides LANG (POSIX precedence).
func utf8Locale(getenv func(string) string) bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := getenv(k); v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}

// isTerminal reports whether w is a character device (a terminal).
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ANSI 256-color codes used for reinforcement.
const (
	colGreen  = "34"
	colLime   = "112"
	colAmber  = "178"
	colOrange = "208"
	colRed    = "196"
	colDim    = "245"
)

// paint wraps s in an ANSI foreground color when color is enabled.
func (o Options) paint(color string, bold bool, s string) string {
	if !o.Color || s == "" {
		return s
	}
	codes := "38;5;" + color
	if bold {
		codes = "1;" + codes
	}
	return "\x1b[" + codes + "m" + s + "\x1b[0m"
}

// bold renders s in bold when color is enabled.
func (o Options) bold(s string) string {
	if !o.Color || s == "" {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

// dim renders s in a muted color when color is enabled.
func (o Options) dim(s string) string { return o.paint(colDim, false, s) }

// gradeColor returns the band color of a grade letter.
func gradeColor(grade string) string {
	switch grade {
	case "A":
		return colGreen
	case "B":
		return colLime
	case "C":
		return colAmber
	case "D":
		return colOrange
	case "E":
		return colRed
	default:
		return colRed
	}
}

// valueColor returns the band color of a 0-100 lock-in value (higher = more
// lock-in), aligned with the default grade bands.
func valueColor(v int) string {
	switch {
	case v < 10:
		return colGreen
	case v < 25:
		return colLime
	case v < 40:
		return colAmber
	case v < 55:
		return colOrange
	default:
		return colRed
	}
}

// grade renders a grade letter, colored by band when color is enabled.
func (o Options) grade(g string) string { return o.paint(gradeColor(g), true, g) }

// coverageColor returns the band color of a 0..1 coverage fraction (higher
// is better, the reverse of valueColor).
func coverageColor(f float64) string {
	switch {
	case f >= 0.9:
		return colGreen
	case f >= 0.75:
		return colLime
	case f >= 0.5:
		return colAmber
	case f >= 0.25:
		return colOrange
	default:
		return colRed
	}
}

// coverageMarker returns the prefix of the coverage line: a status emoji
// with emoji enabled, empty otherwise.
func (o Options) coverageMarker(f float64) string {
	if !o.Emoji {
		return ""
	}
	switch {
	case f >= 0.9:
		return "✅ "
	case f >= 0.5:
		return "⚠️ "
	default:
		return "❌ "
	}
}

// BarCells is the number of cells in a dimension bar.
const BarCells = 10

// barFilled returns the number of filled cells for a 0-100 value (value/10,
// rounded half up, clamped to 0..BarCells).
func barFilled(v int) int {
	n := (v + 5) / 10
	if v < 0 {
		n = 0
	}
	if n > BarCells {
		n = BarCells
	}
	return n
}

// bar renders a 10-cell bar: "█"/"░" in UTF-8, "#"/"." otherwise.
func (o Options) bar(v int) string {
	full, empty := "█", "░"
	if !o.UTF8 {
		full, empty = "#", "."
	}
	n := barFilled(v)
	return o.paint(valueColor(v), false, strings.Repeat(full, n)) + o.dim(strings.Repeat(empty, BarCells-n))
}

// recommended returns the recommended-alternative marker.
func (o Options) recommended() string {
	if o.Emoji {
		return "⭐️"
	}
	return "[R]"
}

// leverMarker returns the prefix of the biggest-lever line: "🎯 " with
// emoji, empty otherwise.
func (o Options) leverMarker() string {
	if o.Emoji {
		return "🎯 "
	}
	return ""
}

// sep returns the footer separator: "·" in UTF-8, "-" otherwise.
func (o Options) sep() string {
	if o.UTF8 {
		return "·"
	}
	return "-"
}

// width returns the display width of s (ANSI sequences must not be present).
func width(s string) int { return runewidth.StringWidth(s) }

// padRight pads s with spaces to display width w.
func padRight(s string, w int) string {
	if d := w - width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// padLeft right-aligns s in display width w.
func padLeft(s string, w int) string {
	if d := w - width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

// truncate shortens s to display width w, ending with "...".
func truncate(s string, w int) string {
	if width(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "...")
}
