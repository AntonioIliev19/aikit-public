package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// commandNames lists every real top-level subcommand, in the order used to
// break ties between equally close suggestions.
var commandNames = []string{
	"menu", "help", "doctor", "update", "init", "status", "index", "search", "symbol",
	"references", "research-skill", "knowledge", "mcp", "sync", "agent", "skill", "package",
}

func knownCommand(name string) bool {
	for _, known := range commandNames {
		if name == known {
			return true
		}
	}
	return false
}

// suggestCommand returns the closest real subcommand for a mistyped one, or ""
// when nothing is close enough to be a plausible typo.
func suggestCommand(word string) string {
	if word == "" || strings.HasPrefix(word, "-") || knownCommand(word) {
		return ""
	}
	word = strings.ToLower(word)
	limit := 2
	if len(word) <= 4 {
		limit = 1
	}
	best, bestDistance := "", limit+1
	for _, name := range commandNames {
		if d := editDistance(word, name); d < bestDistance {
			best, bestDistance = name, d
		}
	}
	return best
}

// editDistance is the optimal-string-alignment distance: insertions, deletions,
// substitutions and adjacent transpositions each cost one.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range rb {
		d[0][j+1] = j + 1
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

// offerSuggestion reports an unknown command and, only when interactive, asks
// whether to run the suggestion. The default answer is No. The mistyped word and
// any other arguments are never echoed, matching the existing diagnostics policy.
func offerSuggestion(input io.Reader, out io.Writer, t terminal, suggestion string, extraArgs, interactive bool) bool {
	g := glyphsFor(t)
	command := t.paint(styleAccent, "conmesh "+suggestion)
	if extraArgs {
		command += " with the same arguments"
	}
	fmt.Fprintf(out, "%s Unknown command\n", t.paint(styleError, g.fail))
	fmt.Fprintf(out, "  All commands: %s\n", t.paint(styleAccent, "conmesh --help"))
	if !interactive {
		fmt.Fprintf(out, "  Did you mean %s?\n", command)
		return false
	}
	fmt.Fprintf(out, "  Did you mean %s? [y/N] ", command)
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
