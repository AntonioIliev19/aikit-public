package cli

import (
	"os"
	"runtime"
	"strings"
	"unicode/utf8"
)

// ANSI SGR codes from the 16-colour palette, so the user's terminal theme applies.
const (
	styleBold    = "1"
	styleDim     = "90" // bright black
	styleAccent  = "34" // blue: commands
	styleSelect  = "36" // cyan: selection marker
	styleSelBold = "1;36"
	styleOK      = "32"
	styleWarn    = "33"
	styleError   = "31"
)

// glyphSet holds the symbols used in human output. The ASCII set is used when
// colour is off or the locale is not UTF-8, so older console hosts never show boxes.
type glyphSet struct {
	brand, on, off, ok, fail, marker, up, down, rule string
}

var (
	unicodeGlyphs = glyphSet{"◆", "●", "○", "✓", "✗", "›", "↑", "↓", "─"}
	asciiGlyphs   = glyphSet{"*", "*", "o", "+", "x", ">", "^", "v", "-"}
)

func glyphsFor(t terminal) glyphSet {
	if t.color && localeUTF8(runtime.GOOS, os.Getenv) {
		return unicodeGlyphs
	}
	return asciiGlyphs
}

// localeUTF8 reports whether text output is UTF-8. Windows consoles are written
// through WriteConsoleW by the Go runtime, so the code page does not matter there.
func localeUTF8(goos string, getenv func(string) string) bool {
	if goos == "windows" {
		return true
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value := getenv(name); value != "" {
			value = strings.ToLower(value)
			return strings.Contains(value, "utf-8") || strings.Contains(value, "utf8")
		}
	}
	return true
}

// streamPolicy decides colour and live output for one stream. NO_COLOR counts
// when it is set at all (a superset of the "non-empty" convention), and the
// --no-color flag turns colour off without disabling live progress.
func streamPolicy(tty bool, term string, noColorEnv, noColorFlag bool) terminal {
	return terminalPolicy(tty, term, noColorEnv || noColorFlag)
}

// fit truncates plain text to at most width display cells (one cell per rune).
// A width of zero or less disables truncation.
func fit(text string, width int) string {
	if width <= 0 || utf8.RuneCountInString(text) <= width {
		return text
	}
	runes := []rune(text)
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "~"
}

// clip is fit for a known terminal width: no room left means no text.
func clip(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return fit(text, width)
}

func cells(text string) int { return utf8.RuneCountInString(text) }

func pad(text string, width int) string {
	if n := cells(text); n < width {
		return text + strings.Repeat(" ", width-n)
	}
	return text
}
