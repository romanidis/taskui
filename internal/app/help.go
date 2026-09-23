package app

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

// PushHelpFind and PopHelpFind narrow as you type. Back to the top on every keystroke: the
// list under the scroll position has changed, and the answer is usually the first line of
// what is left.
func (a *App) PushHelpFind(c rune) {
	a.HelpQuery += string(c)
	a.HelpOffset = 0
}

func (a *App) PopHelpFind() {
	a.HelpQuery = withoutLastRune(a.HelpQuery)
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
