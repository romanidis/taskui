package app

import (
	"github.com/romanidis/taskui/internal/keys"
)

// action says which action, if any, this key means on screen.
//
// Dispatch goes through actions rather than literal characters, which is what makes the
// `keys:` block in `config.yaml` work: the map is consulted here, and the handlers only
// ever see actions.
func (a *App) action(k Key, screen Screen) keys.Action {
	if k.kind != keyChar {
		return keys.None
	}
	c := keys.Chord{Key: k.ch, Mods: k.mods}
	switch screen {
	case ScreenPicker:
		return a.Keymap.Picker(c)
	case ScreenRun:
		return a.Keymap.Run(c)
	case ScreenHistory:
		return a.Keymap.History(c)
	case ScreenTimeline:
		return a.Keymap.Timeline(c)
	case ScreenDiff:
		return a.Keymap.Diff(c)
	case ScreenProfile:
		return a.Keymap.Profile(c)
	case ScreenDetail:
		return a.Keymap.Detail(c)
	case ScreenHelp:
		return a.Keymap.Help(c)
	default:
		return keys.None
	}
}

// quit asks before leaving, if leaving would stop something.
//
// Quitting takes down every slot, and with several open most of them are not on screen —
// so the one keystroke that reaches runs you cannot see is the one that should not be able
// to happen by accident.
//
// It asks even with nothing running. Skipping the prompt when the count happened to be
// zero made `q` mean two different things depending on state you were not looking at: in
// the run view, one key away from `y`, it dropped you out of the tool mid-read the moment
// the last task finished. A key that sometimes exits instantly is a key you learn not to
// press.
func (a *App) quit() bool {
	a.Confirm = ConfirmQuit{Live: a.InFlightCount(), Detached: a.DetachedCount()}
	return false
}

// HandleKey answers one key press. It returns true when the app should exit.
func (a *App) HandleKey(k Key) bool {
	// Anything that is not `esc` breaks the streak, so the hint answers a real run of
	// presses rather than two of them ten minutes apart.
	if k.kind != keyEsc {
		a.EscStreak = 0
	}
	if a.Confirm != nil {
		return a.handleConfirmKey(k)
	}
	if a.Palette {
		return a.handlePaletteKey(k)
	}
	// Before the per-screen handlers, not after: the run screen returns early, and with
	// this below it `gg` and `G` reached every screen except the one with the most rows to
	// move through. The prompt guards inside it keep a run's own `i` and `/` intact.
	if a.handleNavKey(k) {
		return false
	}
	if taken, quitting := a.handleCommonKey(k); taken {
		return quitting
	}
	switch a.Screen {
	case ScreenRun:
		return a.handleRunKey(k)
	case ScreenHistory:
		return a.handleHistoryKey(k)
	case ScreenDetail:
		return a.handleDetailKey(k)
	case ScreenHelp:
		return a.handleHelpKey(k)
	case ScreenTimeline:
		return a.handleTimelineKey(k)
	case ScreenDiff:
		return a.handleDiffKey(k)
	case ScreenProfile:
		return a.handleProfileKey(k)
	case ScreenPicker:
		// Handled below, once the prompts have had their turn.
	}

	if a.EnteringArgs {
		a.handleArgsKey(k)
		return false
	}
	if a.Jumping {
		a.handleJumpKey(k)
		return false
	}
	if a.Filtering && a.handleFilterKey(k) {
		return false
	}

	return a.handlePickerKey(k)
}

// promptTakes says whether an open prompt should get this key rather than the screen behind
// it. Both of the handlers that run ahead of the per-screen ones ask this, so a prompt cannot
// be answered by one of them and ignored by the other.
//
// A typed character goes to whatever is open: a filter, a search, an argument line, or the
// running child's own stdin. A motion is asked about prompt by prompt, because these prompts
// use different parts of the keyboard and a motion the open one has no use for should still
// move the list behind it: `^d` pages the run while you are searching it, because the search
// line has nothing to do with `^d` and the output is right there. The filter and the help's
// find own no motions at all, which is what makes narrowing and then picking one gesture
// rather than two.
func (a *App) promptTakes(k Key) bool {
	switch {
	case k.typed():
		return a.EnteringArgs || a.Searching || a.SendingInput || a.HistorySearching ||
			a.Jumping || a.Filtering || a.HelpFinding
	// Every key is the child's while you are typing at it — `^d` most of all, since that
	// is the one that closes its stdin.
	case a.SendingInput:
		return true
	// A line editor: this is where the caret goes, and ^f ^t are how the start goes out.
	case a.EnteringArgs:
		return k.kind == keyLeft || k.kind == keyRight || k.kind == keyHome || k.kind == keyEnd ||
			k.isCtrl('f') || k.isCtrl('t')
	// ↑ and ↓ step through what the query matched, which is the whole point of typing it.
	case a.Searching, a.HistorySearching, a.Jumping:
		return k.kind == keyUp || k.kind == keyDown
	}
	return false
}

// handleCommonKey answers the two keys that mean the same thing on every screen.
//
// `esc` is deliberately not one of them: it closes a filter here and a panel there and a
// whole run somewhere else, and that difference is the point of it. Quitting and the `?`
// screen have no such difference, and eight copies of them is how the detail panel came to
// advertise `? keys` in its footer and then not answer it.
//
// Returns whether it took the key, and — because leaving has to travel back out to Bubble
// Tea — whether the app is going.
func (a *App) handleCommonKey(k Key) (bool, bool) {
	if a.promptTakes(k) {
		return false, false
	}
	switch {
	case k.isCtrl('c'), a.action(k, a.Screen) == keys.Quit:
		return true, a.quit()
	case a.action(k, a.Screen) == keys.Help:
		a.ToggleHelp()
		return true, false
	case a.action(k, a.Screen) == keys.Palette:
		a.OpenPalette()
		return true, false
	}
	return false, false
}
