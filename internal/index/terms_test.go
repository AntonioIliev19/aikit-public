package index

import (
	"strings"
	"testing"
)

func TestIdentifierSubtokens(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"requestHandler", "request handler"},
		{"RequestHandler", "request handler"},
		{"HTTPServer", "http server"},
		{"parseHTTPResponse2Go", "parse http response2 go"},
		{"utf8Decode", "utf8 decode"},
		{"snake_case_value", ""}, // unicode61 already splits on '_'
		{"lowercase UPPERCASE Single", ""},
		{"getUserName getUserID", "get user name id"},
		{"const fooBar = barBaz;", "foo bar baz"},
		{"ÜberÄrger", "über ärger"},
	} {
		if got := identifierSubtokens(tc.in); got != tc.want {
			t.Errorf("identifierSubtokens(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ftsText("plain words only"); got != "plain words only" {
		t.Fatal("text without mixed-case identifiers must be stored unchanged", got)
	}
	if got := ftsText("requestHandler()"); got != "requestHandler()\nrequest handler" {
		t.Fatal("unexpected FTS text", got)
	}
	huge := strings.Repeat("aB", 20000)
	if n := len(strings.Fields(identifierSubtokens(huge))); n > maxSubtokens {
		t.Fatal("unbounded expansion", n)
	}
	var many strings.Builder
	for i := 0; i < maxSubtokens+100; i++ {
		many.WriteString("x")
		many.WriteString(strings.Repeat("y", i%7+1))
		many.WriteString("Z")
		many.WriteString(string(rune('a' + i%26)))
		many.WriteString(strings.Repeat("q", i/26))
		many.WriteString(" ")
	}
	if n := len(strings.Fields(identifierSubtokens(many.String()))); n > maxSubtokens {
		t.Fatal("unbounded expansion", n)
	}
}
