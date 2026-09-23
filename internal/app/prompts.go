package app

import (
	"fmt"
	"strings"

	"github.com/romanidis/taskui/internal/keys"
)

// confirmBar is the confirmation line. It takes precedence over every other footer:
// nothing else is happening until it is answered.
func (a *App) confirmBar() (line, bool) {
	if a.Confirm == nil {
		return nil, false
	}
	t := a.Theme
	// Verb, subject, why, and what `y` will do. Spelling out the verb separately from the
	// subject is what keeps "run deploy:prod" from reading like "stop deploy:prod" at a
	// glance — these are the two questions it is least acceptable to confuse.
	var verb, subject, why, does string
	switch c := a.Confirm.(type) {
	case ConfirmRun:
		subject = "task " + c.Name
		if len(c.Args) > 0 {
			subject += " " + strings.Join(c.Args, " ")
		}
		why = "  —  this one touches production.  "
		if c.Reason == WouldStopRunning {
			why = "  —  this stops the run already going.  "
		}
		verb, does = " run ", " to run"
	case ConfirmQuit:
		verb, does = " quit ", " to quit"
		switch c.Live {
		case 0:
			// Nothing to lose, so there is no warning to give — but the question is still
			// asked, so `q` never means "gone" without a second keystroke.
			why = "  "
		case 1:
			why = "  —  1 run is still going, and quitting stops it.  "
		default:
			why = fmt.Sprintf("  —  %d runs are still going, and quitting stops them.  ", c.Live)
		}
		// The detached ones are the point of having detached them, and a prompt that only
		// counted what it was about to kill would be leaving out the good news.
		if n := c.Detached; n > 0 {
			why = strings.TrimSuffix(why, "  ") +
				fmt.Sprintf(
					"  %s will keep running.  ",
					plural(n, "1 detached run", fmt.Sprintf("%d detached runs", n)),
				)
		}
	case ConfirmRunMarked:
		verb, does = " run ", " to run"
		subject = fmt.Sprintf("%d marked tasks", len(c.Names))
		if len(c.Names) == 1 {
			subject = "1 marked task"
		}
		// Named, because the whole reason this is one question rather than several is that
		// the dangerous ones are in a batch with tasks that are not.
		why = "  —  " + strings.Join(c.Dangerous, ", ") + " touches production.  "
	case ConfirmStopAll:
		verb, does = " stop ", " to stop"
		subject = fmt.Sprintf("all %d runs", c.Live)
		if c.Live == 1 {
			subject = "1 run"
		}
		why = "  —  including the ones you are not looking at.  "
	}

	return line{
		styled("  "+t.Glyphs.Danger+"  ", onBg(t.Colors.ConfirmFg, t.Colors.ConfirmBg)),
		styled(verb, fg(t.Colors.StatusFailed)),
		styled(subject, fgBold(t.Colors.StatusFailed)),
		styled(why, fg(t.Colors.StatusFailed)),
		styled("y", fgBold(t.Colors.StatusFailed)),
		styled(does+", anything else cancels", fg(t.Colors.Dim)),
	}, true
}

// hintGap is the space between one hint and the next, and before the pointer to the rest.
// Wide enough that two hints never read as one.
const hintGap = 3

// hintBar builds a footer of key hints that fits, and pins `? keys` to the right edge.
//
// The keys are accented and the labels are not, so the line reads as a row of controls
// rather than a paragraph of grey. It stops at a binding boundary: a hint you cannot finish
// reading — one clipped mid-word — is worse than one that was never offered, and the `?`
// screen already documents every last one of them.
func (a *App) hintBar(section *keys.Section) line {
	t := a.Theme
	// Spelled from the keymap like every other hint on the line. It was the last piece of
	// any footer that named a key instead of an action, which meant a rebound `help` left
	// every screen still pointing at `?`.
	help, hasHelp := a.Keymap.KeyOf(keys.Help)
	tail := ""
	if hasHelp {
		tail = help.Display() + " keys"
	}
	tailW := cells(tail)
	hints := keys.FooterHints(section, a.Keymap)
	fits := keys.FooterFits(hints, a.Width-1, tailW+hintGap)

	l := line{plain(" ")}
	used := 1
	for i, b := range hints[:fits] {
		if i > 0 {
			l = append(l, plain("   "))
			used += 3
		}
		l = append(l, styled(b.Keys, fg(t.Colors.Accent)), plain(" "), styled(b.Footer, fg(t.Colors.Dim)))
		used += cells(b.Keys) + 1 + cells(b.Footer)
	}
	l = append(l, plain(strings.Repeat(" ", max(hintGap, a.Width-used-tailW-1))))
	if !hasHelp {
		return l
	}
	return append(l, styled(help.Display(), fg(t.Colors.Accent)), styled(" keys", fg(t.Colors.Dim)))
}

// argsPrompt is shared by both screens that can open it.
func (a *App) argsPrompt() (line, bool) {
	if !a.EnteringArgs {
		return nil, false
	}
	t := a.Theme
	runes := []rune(a.ArgsInput)
	at := clamp(a.ArgsCursor, 0, len(runes))
	l := line{
		plain(" "),
		styled("task "+a.ArgsTarget+" ", fg(t.Colors.Accent)),
		plain(string(runes[:at])),
		styled(t.Glyphs.Cursor, fg(t.Colors.Accent)),
		plain(string(runes[at:])),
	}
	// While ⇥ is cycling, the alternatives take the line the hint would have had: the hint
	// says what an argument looks like, and the candidates say what it could be — and when
	// you are tabbing through them, the second is the question you are asking.
	if a.argsComp != nil {
		used := 0
		for _, s := range l {
			used += cells(s.text)
		}
		return append(l, a.argsCandidateStrip(used)...), true
	}
	// Said out loud, and only while it is true: a line you did not type is a line you have
	// to be told about, or the first `⏎` runs last week's command believing it is empty.
	// It stops being said the moment you change a character of it.
	if a.argsFromHistory && a.ArgsInput == a.argsPrefill {
		return append(l, styled("   last run   ⏎ runs it again", fg(t.Colors.Notice))), true
	}
	// A hint, not a default: the descriptions trail off into prose often enough that
	// pre-filling would hand you a subtly wrong command.
	if hint, ok := a.ArgsHint(); ok {
		l = append(l, styled("   e.g. "+hint, fg(t.Colors.Dim)))
	} else {
		l = append(l, styled("   ⏎ run   esc cancel   ⇥ complete", fg(t.Colors.Dim)))
	}
	return l, true
}

// argsCandidateStrip is where ⇥ goes next, in the order it will get there.
//
// It starts at the candidate *after* the one being shown, not at the front of the list: the
// active one is already on the line, in the prompt, spelled out — printing it again beside
// itself spends the scarcest row on the screen saying the same word twice.
func (a *App) argsCandidateStrip(used int) []span {
	c := a.argsComp
	t := a.Theme
	count := fmt.Sprintf("   %d/%d ⇥", c.idx+1, len(c.cands))
	budget := a.Width - 1 - used - cells(count)

	var out []span
	for i := 1; i < len(c.cands); i++ {
		pick := c.cands[(c.idx+i)%len(c.cands)]
		width := 2 + cells(pick)
		if width > budget {
			break
		}
		budget -= width
		out = append(out, styled("  "+pick, fg(t.Colors.Dim)))
	}
	return append(out, styled(count, fg(t.Colors.Dim)))
}
