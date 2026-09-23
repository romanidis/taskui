package app

import (
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/romanidis/taskui/internal/keys"
)

type keyKind int

const (
	keyChar keyKind = iota
	keyEnter
	keyEsc
	keyTab
	keyBackTab
	keyBackspace
	keyDelete
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPageUp
	keyPageDown
	keyOther
)

// Key is the shape the handlers want: Bubble Tea's key messages carry more detail than
// the dispatch tables need, and flattening them here keeps every handler a straight port
// of the original.
type Key struct {
	kind keyKind
	ch   rune
	mods keys.Mods
}

// fromTea flattens one of Bubble Tea's key events.
//
// The special keys come first because several of them are also control codes — ⇥ is 0x09
// and ⏎ is 0x0d — so asking "is this printable" before asking "is this ⇥" would answer
// wrong. Space is deliberately not among them: it has always arrived here as a character,
// and the handlers match it as one.
func fromTea(msg tea.KeyPressMsg) Key {
	k := tea.Key(msg)
	// Caps lock and num lock are states rather than things you hold, and the terminal has
	// already applied them to the character. Carrying them would make a binding stop working
	// with caps lock on.
	mod := k.Mod &^ (tea.ModCapsLock | tea.ModNumLock)

	special := func(kind keyKind) Key { return Key{kind: kind, mods: modsOf(mod)} }
	switch k.Code {
	case tea.KeyEnter:
		return special(keyEnter)
	case tea.KeyEscape:
		return special(keyEsc)
	case tea.KeyTab:
		// v2 has no separate shift+⇥ code: it reports ⇥ with shift held, which is the same
		// event either way.
		if mod.Contains(tea.ModShift) {
			return Key{kind: keyBackTab, mods: modsOf(mod &^ tea.ModShift)}
		}
		return special(keyTab)
	case tea.KeyBackspace:
		return special(keyBackspace)
	case tea.KeyDelete:
		return special(keyDelete)
	case tea.KeyUp:
		return special(keyUp)
	case tea.KeyDown:
		return special(keyDown)
	case tea.KeyLeft:
		return special(keyLeft)
	case tea.KeyRight:
		return special(keyRight)
	case tea.KeyHome:
		return special(keyHome)
	case tea.KeyEnd:
		return special(keyEnd)
	case tea.KeyPgUp:
		return special(keyPageUp)
	case tea.KeyPgDown:
		return special(keyPageDown)
	}

	// Anything with text is a character, and so is anything printable — a control code
	// arrives as the letter that made it, with ctrl set, and that letter is printable.
	if k.Text == "" && !unicode.IsPrint(k.Code) {
		return Key{kind: keyOther}
	}
	ch := k.Code
	if text := []rune(k.Text); len(text) > 0 {
		// The terminal applies shift itself whenever it changes the character, so `G` arrives
		// as `G`. Keeping the shift as well would make `G` and `⇧g` two different bindings for
		// one keystroke — so it is dropped, and survives only on keys shift cannot change.
		if text[0] != k.Code {
			mod &^= tea.ModShift
		}
		ch = text[0]
	}
	return Key{kind: keyChar, ch: ch, mods: modsOf(mod)}
}

// modsOf keeps the three modifiers that can be bound and discards the rest. Meta, hyper and
// super are not on every keyboard and not in the config's vocabulary.
func modsOf(mod tea.KeyMod) keys.Mods {
	var out keys.Mods
	if mod.Contains(tea.ModCtrl) {
		out |= keys.ModCtrl
	}
	if mod.Contains(tea.ModAlt) {
		out |= keys.ModAlt
	}
	if mod.Contains(tea.ModShift) {
		out |= keys.ModShift
	}
	return out
}

// Char builds a plain character keypress, for tests and for `--keys`.
func Char(c rune) Key { return Key{kind: keyChar, ch: c} }

// Enter is the ⏎ key. It, Esc and Tab are the non-character keys `--keys` can feed.
func Enter() Key { return Key{kind: keyEnter} }

// Esc is the escape key.
func Esc() Key { return Key{kind: keyEsc} }

// Ctrl builds a control chord, for tests and for `--keys`.
func Ctrl(c rune) Key { return Key{kind: keyChar, ch: c, mods: keys.ModCtrl} }

// Tab is the ⇥ key.
func Tab() Key { return Key{kind: keyTab} }

// isChar matches one unmodified character. Modified or not is the whole distinction a
// binding like `shift+space` rests on: if this let ⇧␣ through as ␣, the literal `case`
// above the keymap would swallow it before the binding was ever consulted.
func (k Key) isChar(c rune) bool { return k.kind == keyChar && k.ch == c && k.mods == 0 }

func (k Key) isCtrl(c rune) bool {
	return k.kind == keyChar && k.ch == c && k.mods == keys.ModCtrl
}

// typed is a character meant as text: the prompts take shift, because it is already in the
// character, but not ctrl or alt, which are how you get out of one.
func (k Key) typed() bool {
	return k.kind == keyChar && k.mods&(keys.ModCtrl|keys.ModAlt) == 0
}

// KeyFor maps one character of a `--keys` string to a keypress.
//
// The control characters are the keys that make them: ⇥ is 0x09, ⏎ is 0x0a, esc is 0x1b.
// That is how a screenshot reaches the three keys no letter can.
func KeyFor(c rune) Key {
	switch c {
	case '\t':
		return Tab()
	case '\n':
		return Enter()
	case 0x1b:
		return Esc()
	default:
		return Char(c)
	}
}

// KeysFrom turns a whole `--keys` string into the presses it names.
//
// One rune is one press, except `^` and the character after it, which is a control chord:
// `^d` is ⌃d. Written that way because the alternative is a recipe carrying a literal 0x04,
// which nobody can read in a Taskfile or edit without a hex editor — and the motion keys
// this reaches are exactly the ones a screenshot could not demonstrate before. `^^` is a
// literal caret, which nothing is bound to but which the escape would otherwise swallow.
func KeysFrom(feed string) []Key {
	runes := []rune(feed)
	out := make([]Key, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		if runes[i] != '^' || i+1 >= len(runes) {
			out = append(out, KeyFor(runes[i]))
			continue
		}
		i++
		if runes[i] == '^' {
			out = append(out, Char('^'))
			continue
		}
		out = append(out, Ctrl(unicode.ToLower(runes[i])))
	}
	return out
}
