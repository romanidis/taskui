package app

import "github.com/romanidis/taskui/internal/keys"

// ToggleHelp is `?` from anywhere; `esc` comes back to where you were.
func (a *App) ToggleHelp() {
	if a.inHelp {
		a.Screen = a.helpReturn
		a.inHelp = false
		a.ClearHelpFind()
		return
	}
	a.helpReturn = a.Screen
	a.inHelp = true
	a.Screen = ScreenHelp
	a.HelpOffset = 0
	a.Status = ""
}

// BeginHelpFind opens the find prompt on the `?` screen.
//
// The keymap is 140 bindings over eight screens, which is a page and a half of scrolling to
// answer "which key copies a line". It is the same key that finds a task in the picker, so
// the thing you press to look something up does not change with the screen you are on.
func (a *App) BeginHelpFind() {
	a.HelpFinding = true
	a.HelpOffset = 0
}

// ClearHelpFind drops the query and the prompt, which is what `esc` means here — the whole
// keymap back, rather than the screen closed.
func (a *App) ClearHelpFind() {
	a.HelpFinding = false
	a.HelpQuery = ""
	a.HelpOffset = 0
}

func (a *App) HelpScroll(delta int) {
	a.HelpOffset = max(0, a.HelpOffset+delta)
}

func (a *App) handleHelpKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenHelp) }

	// The find prompt owns every key that is not a way out of it or a way to scroll what it
	// left — `q` and `?` are bindings out there and letters in here, and typing `quit` to
	// look up how to quit must not quit.
	if a.HelpFinding {
		switch {
		case k.kind == keyEsc:
			a.ClearHelpFind()
			return false
		case k.kind == keyEnter:
			// Keep what it narrowed to and give the scroll keys back, as the picker's
			// filter does: you search to find the line, then you read it.
			a.HelpFinding = false
			return false
		// Back to the top on every keystroke: the list under the scroll position has
		// changed, and the answer is usually the first line of what is left.
		case k.kind == keyBackspace:
			a.HelpQuery = withoutLastRune(a.HelpQuery)
			a.HelpOffset = 0
			return false
		case k.typed():
			a.HelpQuery += string(k.ch)
			a.HelpOffset = 0
			return false
		}
	}

	switch {
	// `esc` drops the query first and closes the screen second, so backing out of a search
	// does not also throw away the keymap you were reading.
	case k.kind == keyEsc && a.HelpQuery != "":
		a.ClearHelpFind()
	case k.kind == keyEsc:
		a.ToggleHelp()

	// Find a binding in the keymap itself. The same key that searches every other screen,
	// because "show me the one I mean" should not change name with the screen — and
	// rebinding `search` moves all of them, because Rebind reaches every screen that offers it.
	case act() == keys.Search:
		a.BeginHelpFind()
	}
	return false
}
