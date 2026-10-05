package app

import (
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

func padRight(s string, width int) string {
	if n := cells(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// relativeTo shortens a path for display when it is inside the project, and leaves it alone
// when it is not. An absolute path repeated on every status line is mostly prefix.
func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func baseName(path string) string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return ""
	}
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

func millis(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }

// withoutLastRune is s with its last character taken off: backspace, for the five prompts
// that only ever type at their end. The args prompt has a caret and edits in place.
func withoutLastRune(s string) string {
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}
