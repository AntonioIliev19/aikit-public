package index

import (
	"strings"
	"unicode"
)

// termsVersion identifies the FTS term expansion below. Bump it together with
// any change to identifierSubtokens so existing rows are re-extracted.
const termsVersion = "1"

// maxSubtokens bounds the expansion appended to one chunk.
const maxSubtokens = 4096

// ftsText is the text stored in fts_chunks: the chunk itself followed by the
// camelCase / PascalCase parts of its identifiers. unicode61 already splits on
// '_' (and other punctuation), so snake_case needs no expansion; it does not
// split "requestHandler", so a search for "handler" would otherwise miss it.
// Evidence snippets are always re-read from the source file, never from here.
func ftsText(text string) string {
	extra := identifierSubtokens(text)
	if extra == "" {
		return text
	}
	return text + "\n" + extra
}

// identifierSubtokens returns the lower-cased, de-duplicated parts of every
// mixed-case identifier in text, space separated, in first-seen order.
// "requestHandler" -> "request handler"; "HTTPServer2Go" -> "http server2 go".
func identifierSubtokens(text string) string {
	seen := map[string]bool{}
	var out []string
	runes := []rune(text)
	for i := 0; i < len(runes) && len(out) < maxSubtokens; {
		if !isWordRune(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && isWordRune(runes[j]) {
			j++
		}
		parts := splitCamel(runes[i:j])
		if len(parts) > 1 {
			for _, part := range parts {
				part = strings.ToLower(part)
				if !seen[part] && len(out) < maxSubtokens {
					seen[part] = true
					out = append(out, part)
				}
			}
		}
		i = j
	}
	return strings.Join(out, " ")
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// splitCamel splits at lower/digit->upper ("fooBar", "utf8Decode") and at the
// last upper of an acronym followed by lower case ("HTTPServer" -> HTTP, Server).
func splitCamel(word []rune) []string {
	var parts []string
	start := 0
	for k := 1; k < len(word); k++ {
		prev, cur := word[k-1], word[k]
		boundary := unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev))
		if !boundary && unicode.IsUpper(prev) && unicode.IsUpper(cur) && k+1 < len(word) && unicode.IsLower(word[k+1]) {
			boundary = true
		}
		if boundary {
			parts = append(parts, string(word[start:k]))
			start = k
		}
	}
	return append(parts, string(word[start:]))
}
