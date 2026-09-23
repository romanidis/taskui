// Package shellwords splits and joins argument lines the way a POSIX shell reads them.
//
// Two things type a line of words that has to become argv: the args prompt, whose line goes
// to go-task after the task name, and $EDITOR, which people set to a program with flags —
// and, on a Mac, to a path with a space in it. Neither is ever handed to a shell. That is
// the reason this exists: without one, the quoting a shell would have done has to be done
// here, and done the way people expect from typing into a shell.
package shellwords

import (
	"strings"
	"unicode"
)

// Split splits a line into words, honouring quotes and backslashes the way a POSIX shell
// does, and nothing else — no expansion of any kind.
//
// Outside quotes a backslash takes the next character literally. Inside single quotes
// everything is literal until the closing quote, backslashes included, so `'C:\dir'` keeps
// its backslash. Inside double quotes a backslash only escapes `"`, `\`, `$` and a backtick,
// and is otherwise itself.
//
// `site:new -- "My Post Title"` has to reach go-task as one argument, not three.
func Split(line string) []string {
	out := []string{}
	var current strings.Builder
	var quote rune
	started := false

	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				current.WriteRune(c)
			}
		case c == '\\':
			if i+1 >= len(runes) {
				// A trailing backslash has nothing to escape; a shell would wait for more.
				// Here it is simply itself.
				current.WriteRune(c)
				started = true
				continue
			}
			next := runes[i+1]
			if quote == '"' && !strings.ContainsRune("\"\\$`", next) {
				current.WriteRune(c)
				started = true
				continue
			}
			i++
			current.WriteRune(next)
			started = true
		case quote == '"':
			if c == '"' {
				quote = 0
			} else {
				current.WriteRune(c)
			}
		case c == '\'' || c == '"':
			// An empty quoted string is still an argument.
			quote = c
			started = true
		case unicode.IsSpace(c):
			if started {
				out = append(out, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(c)
			started = true
		}
	}
	if started {
		out = append(out, current.String())
	}
	return out
}

// Join is Split backwards: the line that splits back into these arguments.
//
// Needed the moment a stored argument list is put back in front of you. `-- "My Post
// Title"` comes out of the archive as two arguments, and joining them with a space would
// hand back a line that runs as four.
func Join(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, quote(arg))
	}
	return strings.Join(parts, " ")
}

// quote wraps an argument in double quotes when Split would otherwise take it apart.
//
// Any space counts, not only the ASCII ones: Split breaks on everything [unicode.IsSpace]
// says is one, so an argument with a newline or a no-break space in it left unquoted came
// back as two.
func quote(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsFunc(arg, func(c rune) bool {
		return unicode.IsSpace(c) || strings.ContainsRune(`'"\$`+"`", c)
	}) {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range arg {
		if strings.ContainsRune(`"\$`+"`", c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	b.WriteByte('"')
	return b.String()
}
