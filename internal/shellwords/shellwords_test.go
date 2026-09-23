package shellwords

import (
	"reflect"
	"testing"
)

func TestSplitsLikeAShell(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"-- convert report.pdf", []string{"--", "convert", "report.pdf"}},
		{`-- "My Post Title"`, []string{"--", "My Post Title"}},
		{"NAME=backend", []string{"NAME=backend"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
		{"", []string{}},
	} {
		if got := Split(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestQuotesInsideWordsAndEscapesSurvive(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{`MSG="hello world"`, []string{"MSG=hello world"}},
		{`path\ with\ spaces`, []string{"path with spaces"}},
		// An empty quoted string is a real argument, and `--` is one too.
		{`-- '' empty`, []string{"--", "", "empty"}},
	} {
		if got := Split(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// What a shell would do with a backslash, which depends on the quotes around it.
func TestABackslashMeansWhatItMeansInAShell(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		// Single quotes are literal all the way through.
		{`'C:\dir'`, []string{`C:\dir`}},
		// Double quotes escape only the few characters that are special inside them.
		{`"C:\dir"`, []string{`C:\dir`}},
		{`"say \"hi\""`, []string{`say "hi"`}},
		{`"a\\b"`, []string{`a\b`}},
		// Outside quotes it escapes whatever follows.
		{`a\"b`, []string{`a"b`}},
		{`trailing\`, []string{`trailing\`}},
	} {
		if got := Split(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Join exists to put a stored argument list back on the prompt, so what matters is that
// Split gets the same list back out of it.
func TestJoiningRoundTrips(t *testing.T) {
	for _, args := range [][]string{
		{"--", "-p", "ingest"},
		{"--", "My Post Title"},
		{"NAME=a b", `quote"inside`, `back\slash`},
		{"--", ""},
		{"single'quote", "dollar$sign", "back`tick"},
		// Every kind of space Split breaks on, not only the ASCII ones.
		{"line\nbreak", "no\u00a0break", "tab\there"},
		{},
	} {
		line := Join(args)
		got := Split(line)
		if len(args) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, args) {
			t.Errorf("Join(%q) = %q, which splits back to %q", args, line, got)
		}
	}
}
