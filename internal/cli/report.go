package cli

import (
	"fmt"
	"strings"
)

// stepReport is the terminal-only rendering of a state-changing command. It is
// built from the command's real result; redirected output keeps the original
// single-line text so scripts are unaffected.
type stepReport struct {
	steps   []reportStep
	changed []string
	next    []nextCommand
}

type reportStep struct{ label, subject, detail string }

type nextCommand struct{ command, explanation string }

func (r stepReport) render(t terminal) string {
	g := glyphsFor(t)
	var b strings.Builder
	labelWidth := 0
	for _, step := range r.steps {
		labelWidth = max(labelWidth, cells(step.label))
	}
	for _, step := range r.steps {
		line := t.paint(styleOK, g.ok) + " " + t.paint(styleDim, pad(step.label, labelWidth))
		if step.subject != "" {
			line += "  " + t.paint(styleBold, step.subject)
		}
		if step.detail != "" {
			line += "  " + t.paint(styleDim, step.detail)
		}
		fmt.Fprintln(&b, line)
	}
	fmt.Fprintln(&b)
	if len(r.changed) == 0 {
		fmt.Fprintf(&b, "%s  %s\n", t.paint(styleBold, "Changed"), t.paint(styleDim, "nothing"))
	}
	for i, change := range r.changed {
		label := "       "
		if i == 0 {
			label = t.paint(styleBold, "Changed")
		}
		fmt.Fprintf(&b, "%s  %s\n", label, change)
	}
	if len(r.next) > 0 {
		width := 0
		for _, n := range r.next {
			width = max(width, cells(n.command))
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, t.paint(styleBold, "Next"))
		for _, n := range r.next {
			fmt.Fprintf(&b, "  %s   %s\n", t.paint(styleAccent, pad(n.command, width)), t.paint(styleDim, n.explanation))
		}
	}
	return b.String()
}

func syncNext() []nextCommand {
	return []nextCommand{
		{"conmesh sync --dry-run", "preview what will change in this project"},
		{"conmesh sync --trust", "apply the changes"},
	}
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func harnessList(harnesses []string) string {
	if len(harnesses) == 0 {
		return "no harness"
	}
	return "harness " + strings.Join(harnesses, " + ")
}

// thousands formats a non-negative count with comma separators.
func thousands(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
