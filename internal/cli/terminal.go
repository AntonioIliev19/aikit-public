package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/index"
)

type terminal struct{ live, color bool }

func terminalFor(w io.Writer) terminal { return terminalForOptions(w, false) }

// terminalForOptions checks one stream; stdout and stderr are decided separately.
func terminalForOptions(w io.Writer, noColorFlag bool) terminal {
	f, ok := w.(*os.File)
	if !ok {
		return terminal{}
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	t := streamPolicy(isTerminal(f), os.Getenv("TERM"), noColor, noColorFlag)
	if t.color {
		t.color = enableColor(f)
	}
	return t
}

func terminalPolicy(tty bool, term string, noColor bool) terminal {
	live := tty && term != "dumb"
	return terminal{live: live, color: live && !noColor}
}

func (t terminal) paint(code, text string) string {
	if !t.color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Styling is applied only at the terminal boundary, keeping redirected bytes intact.
func (t terminal) output(text string) string {
	if !t.color {
		return text
	}
	lines := strings.SplitAfter(text, "\n")
	jsonOutput := strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if jsonOutput {
			if strings.HasPrefix(trimmed, `"`) {
				// MarshalIndent escapes quotes inside keys, so find the closing unescaped quote.
				for j := 1; j < len(trimmed); j++ {
					if trimmed[j] == '\\' {
						j++
					} else if trimmed[j] == '"' {
						if j+1 < len(trimmed) && trimmed[j+1] == ':' {
							lines[i] = strings.Replace(line, trimmed[:j+1], t.paint("36", trimmed[:j+1]), 1)
						}
						break
					}
				}
			}
		} else if strings.HasPrefix(trimmed, "ConMesh") || strings.HasPrefix(trimmed, "Usage:") {
			lines[i] = t.paint("1;36", strings.TrimSuffix(line, "\n")) + "\n"
		} else if text == help && strings.HasPrefix(line, "  ") && trimmed != "" {
			command := strings.Fields(trimmed)[0]
			lines[i] = strings.Replace(line, command, t.paint("36", command), 1)
		} else if strings.Contains(line, ": false") || strings.Contains(line, ": true") {
			lines[i] = strings.ReplaceAll(line, ": false", ": "+t.paint("33", "false"))
			lines[i] = strings.ReplaceAll(lines[i], ": true", ": "+t.paint("32", "true"))
		} else if strings.HasPrefix(trimmed, "Project ") || strings.HasPrefix(trimmed, "Installed ") || strings.HasPrefix(trimmed, "Verified ") || strings.HasPrefix(trimmed, "Package removed") {
			lines[i] = t.paint("32", strings.TrimSuffix(line, "\n")) + "\n"
		}
	}
	return strings.Join(lines, "")
}

func progressPercent(p index.Progress) int {
	if p.Phase == "done" {
		return 100
	}
	if p.Scanned == 0 {
		return 0
	}
	return min(99, p.Processed*100/p.Scanned)
}

// indexSummary is the human summary of a finished index run. Files with syntax
// errors or over parser limits are still indexed, as plain text without symbols,
// so they are reported as a note and not as a warning.
func indexSummary(t terminal, report index.Progress) string {
	out := fmt.Sprintf("Indexed %d; unchanged %d; skipped %d; malformed %d; limited %d; deleted %d; elapsed %.2fs\n", report.Indexed, report.Unchanged, report.Skipped, report.Malformed, report.Limited, report.Deleted, float64(report.ElapsedMillis)/1000)
	if report.Refreshed {
		out += "Index format changed; all files were re-extracted\n"
	}
	if report.PlainText > 0 {
		out += fmt.Sprintf("Indexed as plain text without symbols: %d (syntax errors %d; over parser limits %d)\n", report.PlainText, report.Malformed, report.Limited)
	}
	if report.Skipped > 0 {
		r := report.SkipReasons
		var parts []string
		for _, reason := range []struct {
			label string
			count int
		}{
			{"ignored", r.Ignored}, {"built-in denied name", r.HardDenied}, {"not a regular file", r.UnsupportedEntry},
			{"too large", r.TooLarge}, {"sensitive content", r.SensitiveContent}, {"binary", r.Binary},
			{"over parser limits", r.ParserLimit}, {"changed during indexing", r.Changed}, {"unreadable", r.Unreadable},
		} {
			if reason.count > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", reason.label, reason.count))
			}
		}
		if len(parts) > 0 {
			out += "Skipped: " + strings.Join(parts, "; ") + "\n"
		}
	}
	if report.SemanticCleanupWarning {
		out += t.paint("33", "Warning: semantic cleanup did not complete") + "\n"
	}
	return out
}

type indexProgress struct {
	writer      io.Writer
	terminal    terminal
	cancel      func()
	err         error
	width       int
	lastPhase   string
	lastPercent int
	lastWrite   time.Time
}

func (p *indexProgress) update(report index.Progress) {
	if p.err != nil {
		return
	}
	percent := progressPercent(report)
	if report.Phase == p.lastPhase && percent == p.lastPercent && time.Since(p.lastWrite) < 100*time.Millisecond {
		return
	}
	label := "Scanning repository... total unknown"
	code := "36"
	switch report.Phase {
	case "processing", "finalizing", "done":
		filled := percent / 5
		label = fmt.Sprintf("Index [%s%s] %3d%%  %d/%d files", strings.Repeat("#", filled), strings.Repeat("-", 20-filled), percent, report.Processed, report.Scanned)
		if report.Phase == "finalizing" {
			label += "  finalizing"
		}
		if report.Phase == "done" {
			code = "32"
			label += "  complete"
		}
	case "failed":
		code = "31"
		label = fmt.Sprintf("Index failed or cancelled after %d/%d files; changes not committed", report.Processed, report.Scanned)
	}
	line := "\r" + p.terminal.paint(code, label) + strings.Repeat(" ", max(0, p.width-len(label)))
	if report.Phase == "done" || report.Phase == "failed" {
		line += "\n"
	}
	if report.Phase == "done" {
		line += indexSummary(p.terminal, report)
	}
	_, p.err = io.WriteString(p.writer, line)
	if p.err != nil {
		p.cancel()
	}
	p.width, p.lastPhase, p.lastPercent, p.lastWrite = len(label), report.Phase, percent, time.Now()
}
