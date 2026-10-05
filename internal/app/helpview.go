package app

import (
	"fmt"
	"strings"

	"github.com/romanidis/taskui/internal/keys"
)

func (a *App) helpHeader() line {
	t := a.Theme
	return a.header("keys", []span{
		styled("the footer shows a subset; this is all of them", fg(t.Colors.Dim)),
	})
}

// helpMatch says whether a binding answers the find query.
//
// Plain case-insensitive substring, over the keys and the description, rather than the
// fuzzy match the picker uses on task names. A name is a short token where a subsequence is
// a good guess at what you meant; these are sentences, and fuzzy over prose matches almost
// everything.
//
// The section title is deliberately not part of the haystack. It was, and `ke` — three
// letters of `Picker` — kept every binding in that section, none of which said `ke`
// anywhere. A filter whose surviving lines do not contain what you typed is a filter you
// cannot read, and the title is a heading you can already see; being able to search it back
// is not worth the rows it drags in.
func helpMatch(b keys.Binding, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(b.Keys+" "+b.What), strings.ToLower(query))
}

// helpMatches is how many bindings the query leaves, for the prompt's counter.
func (a *App) helpMatches() int {
	n := 0
	for _, section := range keys.Sections {
		for _, b := range keys.Spelled(section, a.Keymap) {
			if helpMatch(b, a.HelpQuery) {
				n++
			}
		}
	}
	return n
}

func (a *App) drawHelp(width, height int) []string {
	t := a.Theme
	// Widest key column across every section, so the descriptions line up as one table
	// rather than five.
	pad := keys.WidestKeys(a.Keymap)

	var lines []line
	for _, section := range keys.Sections {
		// A section with nothing left in it is dropped whole. Its title is what says where
		// a binding lives, so an empty one would answer the question with a heading and
		// nothing under it.
		var body []line
		for _, binding := range keys.Spelled(section, a.Keymap) {
			if !helpMatch(binding, a.HelpQuery) {
				continue
			}
			body = append(body, line{
				plain("  "),
				styled(padRight(binding.Keys, pad), fgBold(t.Colors.Mode)),
				plain("  "),
				styled(binding.What, fg(t.Colors.Text)),
			})
		}
		if len(body) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, line{})
		}
		lines = append(lines, line{
			styled(section.Title, fgBold(t.Colors.Accent)),
			styled("  — "+section.Note, fg(t.Colors.Dim)),
		})
		lines = append(lines, body...)
	}
	if len(lines) == 0 {
		lines = append(lines, line{styled("  no binding says `"+a.HelpQuery+"`", fg(t.Colors.Dim))})
	}

	return a.scrollPane(lines, width, height, &a.HelpOffset)
}

func (a *App) helpFooter() line {
	// `q` works from here too, so the question it can raise has to be visible from here
	// too — a confirmation with nowhere to draw is a modal you cannot see.
	if l, ok := a.confirmBar(); ok {
		return l
	}
	t := a.Theme
	if a.HelpFinding || a.HelpQuery != "" {
		l := line{
			plain(" "),
			styled("find: ", fg(t.Colors.Accent)),
			plain(a.HelpQuery),
		}
		if a.HelpFinding {
			l = append(l, styled(t.Glyphs.Cursor, fg(t.Colors.Accent)))
		}
		if a.HelpQuery != "" {
			l = append(l, styled(fmt.Sprintf("   %d keys", a.helpMatches()), fg(t.Colors.Dim)))
		}
		// Once `⏎` has been pressed the prompt is gone and the scroll keys are back, so the
		// hints are the ones that still do something.
		hints := "   ⏎ keep   esc clear"
		if !a.HelpFinding {
			// Not the section's own footer: that one offers `esc … close`, and in this state
			// `esc` clears the query instead. Advertising both on one line makes the key look
			// like it does whichever the reader guesses.
			find, _ := a.Keymap.KeyOf(keys.Search)
			hints = "   j k ↑ ↓ scroll   " + find.Display() + " find   esc clear"
		}
		return append(l, styled(hints, fg(t.Colors.Dim)))
	}
	return line{plain(" "), styled(keys.Footer(&keys.HelpSection, a.Keymap), fg(t.Colors.Dim))}
}
