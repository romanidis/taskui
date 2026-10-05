package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/romanidis/taskui/internal/keys"
)

// paletteEntry is one thing the palette can do: the key that does it, what it does, and the
// keypress the palette makes on your behalf.
type paletteEntry struct {
	keys  string
	what  string
	press Key
	// words is what a query is matched against: the action's own name, as a config spells
	// it, and its footer label, beside what it does. "timeline" finds `⇧H`, whose
	// description never uses the word.
	words string
}

// OpenPalette is `:` from any screen: every action the screen offers, by what it does.
//
// Sixty-odd bindings over eight screens, and the ones you need least often — detach, the
// profile, widening the history — are the ones nobody remembers. `?` lists them all; this
// lists the ones that apply here, narrows them as you type, and does the one you pick.
func (a *App) OpenPalette() {
	a.Palette = true
	a.PaletteQuery = ""
	a.PaletteCursor = 0
	a.Status = ""
}

// paletteEntries is what the palette offers here, in the order the key table lists it.
//
// Read from the screen's own section of that table, the one the `?` screen and the footers
// read, so it cannot offer an action the screen does not answer. In the picker it offers what
// the footer would for the row the cursor is on. Every entry is pressed rather than called:
// choosing one is pressing its key, so the palette does exactly what the key does.
func (a *App) paletteEntries() []paletteEntry {
	section := map[Screen]*keys.Section{
		ScreenPicker: &keys.Picker, ScreenRun: &keys.Run, ScreenHistory: &keys.HistorySection,
		ScreenTimeline: &keys.TimelineSection, ScreenDiff: &keys.DiffSection,
		ScreenProfile: &keys.ProfileSection, ScreenDetail: &keys.DetailSection, ScreenHelp: &keys.HelpSection,
	}[a.Screen]
	if section == nil {
		return nil
	}
	row := keys.Row(0)
	if a.Screen == ScreenPicker {
		row = a.pickerRow()
	}
	spelled := keys.Spelled(section, a.Keymap)
	var out []paletteEntry
	for i, b := range section.Bindings {
		if row != 0 && b.On != 0 && b.On&row == 0 {
			continue
		}
		var press Key
		name := ""
		switch first, _, _ := strings.Cut(b.Keys, " "); first {
		case "⏎":
			press = Enter()
		case "space":
			press = Char(' ')
		case "⇥":
			press = Tab()
		default:
			placeholder, ok := strings.CutPrefix(first, "{")
			name = strings.TrimSuffix(placeholder, "}")
			action, known := keys.ActionByName(name)
			// The motions are not actions, and the palette and the keymap screen are where
			// you already are.
			if !ok || !known || action == keys.Palette || action == keys.Help {
				continue
			}
			chord, bound := a.Keymap.KeyOf(action)
			if !bound {
				continue
			}
			press = Key{kind: keyChar, ch: chord.Key, mods: chord.Mods}
		}
		out = append(out, paletteEntry{
			keys: spelled[i].Keys, what: spelled[i].What, press: press,
			words: strings.ToLower(name + " " + b.Footer + " " + spelled[i].What),
		})
	}

	// Every word of the query, anywhere in an entry. Not fuzzy, as the task filter is: a
	// task name is a short path where `blint` finding `backend:lint` is the point, and a
	// description is a sentence, where four letters in order match half the table. In the
	// table's order, which is grouped, rather than by score.
	query := strings.Fields(strings.ToLower(a.PaletteQuery))
	var matched []paletteEntry
	for _, e := range out {
		if !slices.ContainsFunc(query, func(w string) bool { return !strings.Contains(e.words, w) }) {
			matched = append(matched, e)
		}
	}
	return matched
}

// handlePaletteKey answers a key while the palette is open. It returns true when the app
// should exit, which an entry can ask for: quitting is an action like any other.
func (a *App) handlePaletteKey(k Key) bool {
	entries := a.paletteEntries()
	switch {
	case k.kind == keyEsc:
		a.Palette = false
	case k.isCtrl('c'):
		a.Palette = false
		return a.quit()
	case k.kind == keyEnter:
		a.Palette = false
		if a.PaletteCursor < len(entries) {
			return a.HandleKey(entries[a.PaletteCursor].press)
		}
	case k.kind == keyDown:
		a.PaletteCursor = clamp(a.PaletteCursor+1, 0, max(0, len(entries)-1))
	case k.kind == keyUp:
		a.PaletteCursor = clamp(a.PaletteCursor-1, 0, max(0, len(entries)-1))
	case k.kind == keyBackspace:
		a.PaletteQuery = withoutLastRune(a.PaletteQuery)
		a.PaletteCursor = 0
	case k.typed():
		a.PaletteQuery += string(k.ch)
		a.PaletteCursor = 0
	}
	return false
}

// drawPalette lists what the palette offers, in place of the screen's body; the header stays,
// so you can see which screen it is offering the actions of.
func (a *App) drawPalette(width, height int) []string {
	t := a.Theme
	entries := a.paletteEntries()
	a.PaletteCursor = clamp(a.PaletteCursor, 0, max(0, len(entries)-1))
	widest := 0
	for _, e := range entries {
		widest = max(widest, cells(e.keys))
	}
	offset := max(0, a.PaletteCursor-height+1)
	out := make([]string, 0, height)
	for i := offset; i < len(entries) && len(out) < height; i++ {
		e := entries[i]
		l := line{
			styled(" "+padRight(e.keys, widest)+"   ", fg(t.Colors.Accent)),
			styled(e.what, fg(t.Colors.Text)),
		}
		out = append(out, l.renderRow(width, i == a.PaletteCursor, t, a.Phase, 0, 1))
	}
	if len(entries) == 0 {
		out = append(out, line{styled("  nothing here does `"+a.PaletteQuery+"`", fg(t.Colors.Dim))}.
			renderRow(width, false, t, a.Phase, 0, 1))
	}
	return out
}

func (a *App) paletteFooter() line {
	t := a.Theme
	n := len(a.paletteEntries())
	return line{
		plain(" "),
		styled(":", fg(t.Colors.Search)),
		plain(a.PaletteQuery),
		styled(t.Glyphs.Cursor, fg(t.Colors.Search)),
		styled(fmt.Sprintf("   %d %s", n, plural(n, "action", "actions")), fg(t.Colors.Dim)),
		styled("   ↑ ↓ choose   ⏎ do it   esc close", fg(t.Colors.Dim)),
	}
}
