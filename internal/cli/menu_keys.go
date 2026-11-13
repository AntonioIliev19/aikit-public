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

var errMenuInterrupt = errors.New("menu interrupted")

type menuOption struct {
	name, description string
}

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

func pickMenu(input *bufio.Reader, out io.Writer, t terminal, title string, options []menuOption, raw func() (func() error, error)) (choice int, err error) {
	if raw != nil {
		restore, err := raw()
		if err != nil {
			return -1, err
		}
		defer func() { err = errors.Join(err, restore()) }()
	}
	if _, err := fmt.Fprintln(out, title); err != nil {
		return -1, err
	}
	selected := 0
	redraw := false
	for {
		var frame strings.Builder
		if redraw {
			fmt.Fprintf(&frame, "\x1b[%dA\r\x1b[J", len(options)+2)
		}
		for i, option := range options {
			line := fmt.Sprintf("  %-13s %s", option.name, option.description)
			if i == selected {
				line = t.paint("1;96", "❯ "+line[2:])
			}
			fmt.Fprintln(&frame, strings.TrimRight(line, " "))
		}
		fmt.Fprintln(&frame)
		fmt.Fprintln(&frame, t.paint("2", "  ↑↓ navigate  ·  ↵ select  ·  ⌫ back"))
		if _, err := io.WriteString(out, frame.String()); err != nil {
			return -1, err
		}
		for {
			key, err := readMenuKey(input)
			if err != nil {
				return -1, err
			}
			switch key {
			case 'A':
				selected = (selected + len(options) - 1) % len(options)
			case 'B':
				selected = (selected + 1) % len(options)
			case '\r', '\n':
				_, err := fmt.Fprintf(out, "\x1b[%dA\r\x1b[J  %s\n\n", len(options)+2, t.paint("1;96", options[selected].name))
				return selected, err
			case '\b', 127:
				return -1, nil
			case 3:
				return -1, errMenuInterrupt
			default:
				continue
			}
			break
		}
		redraw = true
	}
}

func readMenuKey(input *bufio.Reader) (byte, error) {
	key, err := input.ReadByte()
	if err != nil || key != 27 {
		if key == 'A' || key == 'B' {
			key = 0
		}
		return key, err
	}
	return skipMenuEscape(input)
}

func skipMenuEscape(input *bufio.Reader) (byte, error) {
	prefix, err := input.ReadByte()
	if err != nil || prefix != '[' && prefix != 'O' {
		return 0, err
	}
	for {
		key, err := input.ReadByte()
		if err != nil {
			return 0, err
		}
		if key >= 0x40 && key <= 0x7e {
			if key == 'A' || key == 'B' {
				return key, nil
			}
			return 0, nil
		}
	}
}
