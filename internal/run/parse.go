package run

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// applyOverwrites applies in-place overwrite semantics: carriage return and backspace.
//
// Progress indicators redraw without a newline. `cargo` and downloaders use `\r` to return
// to column zero; npm and npx spinners use `\b` to rub out the previous frame. Kept
// verbatim, one "line" arrives as `10%\r50%\r100%` or `\|/-\|/-Need to install`, which
// renders as control characters, wraps into several rows, and lets a search match a state
// that was never the final answer.
//
// This is not terminal emulation: a short write over a longer one leaves the old tail
// visible on a real terminal and does not here. For progress output that is invisible, and
// the alternative is a column-tracking screen buffer for a cosmetic edge case.
func applyOverwrites(text string) string {
	if !strings.ContainsAny(text, "\r\b") {
		return text
	}
	var out []rune
	// A line ending in `\r` has not been overwritten by anything, so the text before it
	// still stands; without this, "done\r" would come out empty.
	var lastWritten []rune
	for _, c := range text {
		switch c {
		case '\r':
			if len(out) > 0 {
				lastWritten = out
			}
			out = nil
		case '\b':
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return string(lastWritten)
	}
	return string(out)
}

// parseLine turns one line of `task --output prefixed` into events.
//
// Three shapes matter:
//
//	`task: [name] cmd`  go-task echoing the command it is about to run
//	`[name] output`     an output line, tagged by `--output prefixed`
//	`task: Failed to run task "name": …`
func parseLine(text string) []Event {
	stripped := ansi.Strip(text)

	if rest, ok := strings.CutPrefix(stripped, "task: ["); ok {
		if name, _, ok := strings.Cut(rest, "] "); ok {
			return []Event{LineEvent{Task: name, Raw: text, IsCommand: true}}
		}
	}

	if strings.HasPrefix(stripped, "task: ") {
		// `task: Failed to run task "agg": task: Failed to run task "c": exit status 3` —
		// the innermost name is the one that actually failed.
		culprit := ""
		rest := stripped
		const needle = `Failed to run task "`
		for {
			i := strings.Index(rest, needle)
			if i < 0 {
				break
			}
			rest = rest[i+len(needle):]
			if end := strings.Index(rest, `"`); end >= 0 {
				culprit = rest[:end]
			}
		}
		var events []Event
		if culprit != "" {
			events = append(events, FailedEvent{Task: culprit})
		}
		return append(events, LineEvent{Raw: text})
	}

	if rest, ok := strings.CutPrefix(stripped, "["); ok {
		if name, _, ok := strings.Cut(rest, "] "); ok {
			// Trust the tag only if it looks like a task name — output that happens to
			// start with `[` should not invent a task.
			if name != "" && isTaskName(name) {
				raw := text
				if _, after, ok := strings.Cut(text, "] "); ok {
					raw = after
				}
				return []Event{LineEvent{Task: name, Raw: raw}}
			}
		}
	}

	return []Event{LineEvent{Raw: text}}
}

func isTaskName(name string) bool {
	for _, c := range name {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune(":-_.", c) {
			return false
		}
	}
	return true
}

// partialOf is a fragment as a Partial, with go-task's tag read off it the way parseLine
// reads one off a whole line. Without it a fragment of `[b] …` kept the tag as text and was
// put under whichever task spoke last, which under parallel deps is often another one.
func partialOf(text string) Partial {
	for _, event := range parseLine(text) {
		if line, ok := event.(LineEvent); ok {
			return Partial{Task: line.Task, Text: line.Raw}
		}
	}
	return Partial{Text: text}
}

// go-task's own announcements. It names the task in the message and gives the line no
// `[name]` prefix, so without this the reason and the task it is about end up in different
// places on screen.
var (
	upToDateNotice = regexp.MustCompile(`^task: Task "([^"]+)" is up to date$`)
	// Unanchored, and the *last* match is the one that counts: a failure inside an
	// aggregate is reported nested — `Failed to run task "all": task: Failed to run task
	// "guarded": task: precondition not met` — and the task that actually has the
	// unsatisfied precondition is the innermost one, not the one that invoked it.
	preconditionNotice = regexp.MustCompile(`Failed to run task "([^"]+)": task: precondition not met`)
)

// skipReason reads one line of output for an announcement that a task was not run.
//
// Trimmed first, and that is not cosmetic: the pty delivers CRLF, so a line arrives with a
// trailing carriage return and an anchored match quietly fails on it. Which it did — the
// first of two consecutive skips was explained and the second was not, because only the
// second carried the CR.
func skipReason(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if m := upToDateNotice.FindStringSubmatch(text); m != nil {
		return m[1], "up to date", true
	}
	if all := preconditionNotice.FindAllStringSubmatch(text, -1); len(all) > 0 {
		return all[len(all)-1][1], "precondition not met", true
	}
	return "", "", false
}
