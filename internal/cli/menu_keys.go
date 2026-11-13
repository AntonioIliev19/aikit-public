package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var (
	errMenuInterrupt = errors.New("menu interrupted")
	errMenuQuit      = errors.New("menu quit")
)

type menuOption struct {
	id, name, description string
	// group starts a new titled section when it differs from the previous option's.
	group, subtitle string
	// sameAs lists equivalent non-interactive commands, joined by sameAsJoin ("or" by default).
	sameAs     []string
	sameAsJoin string
}

const (
	keyNone = iota
	keyUp
	keyDown
	keyEnter
	keyBack
	keyQuit
	keyInterrupt
)

func terminalMenuMode(input *os.File) func() (func() error, error) {
	return func() (func() error, error) {
		fd := int(input.Fd())
		state, err := term.MakeRaw(fd)
		if err != nil {
			return nil, err
		}
		return func() error { return term.Restore(fd, state) }, nil
	}
}

func terminalWidth(f *os.File) int {
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return width
}

func pickMenu(input *bufio.Reader, out io.Writer, t terminal, width int, title string, options []menuOption, raw func() (func() error, error)) (choice int, err error) {
	if raw != nil {
		restore, err := raw()
		if err != nil {
			return -1, err
		}
		defer func() { err = errors.Join(err, restore()) }()
	}
	if title != "" {
		if _, err := fmt.Fprintln(out, title); err != nil {
			return -1, err
		}
	}
	g := glyphsFor(t)
	selected := 0
	drawn := 0
	for {
		var frame strings.Builder
		if drawn > 0 {
			fmt.Fprintf(&frame, "\x1b[%dA\r\x1b[J", drawn)
		}
		body := renderPicker(t, width, options, selected)
		frame.WriteString(body)
		if _, err := io.WriteString(out, frame.String()); err != nil {
			return -1, err
		}
		drawn = strings.Count(body, "\n")
		for {
			key, err := readMenuKey(input)
			if err != nil {
				return -1, err
			}
			switch key {
			case keyUp:
				selected = (selected + len(options) - 1) % len(options)
			case keyDown:
				selected = (selected + 1) % len(options)
			case keyEnter:
				_, err := fmt.Fprintf(out, "\x1b[%dA\r\x1b[J%s %s\n\n", drawn, t.paint(styleSelect, g.marker), t.paint(styleSelBold, options[selected].name))
				return selected, err
			case keyBack:
				return -1, nil
			case keyQuit:
				return -1, errMenuQuit
			case keyInterrupt:
				return -1, errMenuInterrupt
			default:
				continue
			}
			break
		}
	}
}

// renderPicker draws the option list and footer. Every line is kept within the
// terminal width so the line count used for redraws stays exact.
func renderPicker(t terminal, width int, options []menuOption, selected int) string {
	g := glyphsFor(t)
	nameWidth := 0
	grouped := false
	for _, o := range options {
		nameWidth = max(nameWidth, cells(o.name))
		grouped = grouped || o.group != ""
	}
	nameWidth = max(nameWidth+2, 13)
	var b strings.Builder
	group := ""
	for i, o := range options {
		if o.group != "" && o.group != group {
			if group != "" {
				fmt.Fprintln(&b)
			}
			group = o.group
			header := t.paint(styleBold, fit(o.group, width-1))
			if o.subtitle != "" && cells(o.group)+2+cells(o.subtitle) < width-1 || o.subtitle != "" && width <= 0 {
				header += "  " + t.paint(styleDim, o.subtitle)
			}
			fmt.Fprintln(&b, header)
		}
		desc := o.description
		if width > 0 {
			desc = clip(desc, width-1-2-nameWidth)
		}
		name := o.name
		if desc != "" {
			name = pad(name, nameWidth)
		} else if width > 0 {
			// Too narrow for a description: keep the label itself on one line.
			name = clip(name, max(1, width-1-2))
		}
		var line string
		if i == selected {
			line = t.paint(styleSelect, g.marker) + " " + t.paint(styleSelBold, name)
			if desc != "" {
				line += desc
			}
		} else {
			line = "  " + name
			if desc != "" {
				line += t.paint(styleDim, desc)
			}
		}
		fmt.Fprintln(&b, line)
	}
	fmt.Fprintln(&b)
	if grouped {
		fmt.Fprintln(&b, t.paint(styleDim, menuRule(g, width)))
		fmt.Fprintln(&b, sameAsLine(t, width, options[selected]))
	}
	fmt.Fprintln(&b, keyHints(t, g, width))
	return b.String()
}

// sameAsLine shows the highlighted item's equivalent commands, dropping
// alternatives that would not fit on one line.
func sameAsLine(t terminal, width int, o menuOption) string {
	if len(o.sameAs) == 0 {
		return ""
	}
	join := o.sameAsJoin
	if join == "" {
		join = "or"
	}
	const prefix = "same as  "
	if width > 0 && cells(prefix) >= width-1 {
		return t.paint(styleDim, clip(strings.TrimSpace(prefix), width-1))
	}
	used := cells(prefix)
	line := t.paint(styleDim, prefix)
	for i, command := range o.sameAs {
		sep := ""
		if i > 0 {
			sep = "  " + join + "  "
		}
		if width > 0 && used+cells(sep)+cells(command) > width-1 {
			if i == 0 {
				line += t.paint(styleAccent, clip(command, width-1-used))
				break
			}
			// The alternative does not fit beside the first: continue on an indented line.
			sep = join + "  "
			indent := cells(prefix)
			if indent+cells(sep)+cells(command) > width-1 {
				break
			}
			line += "\n" + strings.Repeat(" ", indent) + t.paint(styleDim, sep) + t.paint(styleAccent, command)
			used = indent + cells(sep) + cells(command)
			continue
		}
		line += t.paint(styleDim, sep) + t.paint(styleAccent, command)
		used += cells(sep) + cells(command)
	}
	return line
}

func keyHints(t terminal, g glyphSet, width int) string {
	hints := [][2]string{{g.up + g.down, "navigate"}, {"enter", "select"}, {"esc", "back"}, {"q", "quit"}}
	used := 0
	var b strings.Builder
	for i, h := range hints {
		sep := ""
		if i > 0 {
			sep = "   "
		}
		n := cells(sep) + cells(h[0]) + 1 + cells(h[1])
		if width > 0 && used+n > width-1 {
			break
		}
		b.WriteString(sep + h[0] + " " + t.paint(styleDim, h[1]))
		used += n
	}
	return b.String()
}

func readMenuKey(input *bufio.Reader) (int, error) {
	key, err := input.ReadByte()
	if err != nil {
		return keyNone, err
	}
	switch key {
	case 27:
		return readMenuEscape(input)
	case '\r', '\n':
		return keyEnter, nil
	case '\b', 127:
		return keyBack, nil
	case 3:
		return keyInterrupt, nil
	case 'q', 'Q':
		return keyQuit, nil
	}
	return keyNone, nil
}

// readMenuEscape is called after ESC. A terminal sends a whole escape sequence
// in one write, so an ESC with nothing buffered behind it, or followed by a byte
// that cannot start a sequence, is the Esc key itself.
func readMenuEscape(input *bufio.Reader) (int, error) {
	if input.Buffered() == 0 {
		return keyBack, nil
	}
	prefix, err := input.ReadByte()
	if err != nil {
		return keyNone, err
	}
	if prefix != '[' && prefix != 'O' {
		_ = input.UnreadByte()
		return keyBack, nil
	}
	for {
		key, err := input.ReadByte()
		if err != nil {
			return keyNone, err
		}
		if key >= 0x40 && key <= 0x7e {
			switch key {
			case 'A':
				return keyUp, nil
			case 'B':
				return keyDown, nil
			}
			return keyNone, nil
		}
	}
}
