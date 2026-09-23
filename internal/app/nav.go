package app

import (
	tea "charm.land/bubbletea/v2"
)

// HalfPage is what `^d` and `^u` move by.
func (a *App) HalfPage() int {
	return max(1, a.Viewport/2)
}

// Page is what `^f` and `^b` move by. The body height of the last frame, not the 15 rows
// PgUp and PgDn used to assume: on a tall terminal that was two thirds of a screen and on a
// short one it was four screens, and a page key that does not move a page is a worse guess
// than the number it is guessing at.
func (a *App) Page() int {
	return max(1, a.Viewport)
}

// MoveBy moves whatever the screen on show is moving, by delta rows.
//
// The counterpart to GotoTop and GotoBottom, and there for the same reason: every screen
// here is a list, every list moves the same way, and this is the one place that knows which
// cursor belongs to which screen. The keys that drive it — see handleNavKey — then do not
// have to know, so adding a motion adds it everywhere at once.
func (a *App) MoveBy(delta int) {
	switch a.Screen {
	case ScreenPicker:
		a.MoveCursor(delta)
	case ScreenRun:
		a.RunMoveCursor(delta)
	case ScreenHistory:
		a.HistoryMoveCursor(delta)
	case ScreenTimeline:
		a.TimelineMoveCursor(delta)
	case ScreenDiff:
		a.DiffMoveCursor(delta)
	case ScreenProfile:
		a.ProfileMoveCursor(delta)
	// The detail panel and the `?` screen are text rather than rows, so they have an offset
	// and no cursor. Moving them is scrolling them, which is the same key either way.
	case ScreenDetail:
		a.DetailScroll(delta)
	case ScreenHelp:
		a.HelpScroll(delta)
	}
}

// GotoTop is `gg` — first row of whatever is on screen.
func (a *App) GotoTop() {
	switch a.Screen {
	case ScreenPicker:
		a.Cursor = 0
		a.Offset = 0
	case ScreenRun:
		a.RunCursor = 0
		a.RunOffset = 0
		a.Following = false
	case ScreenHistory:
		a.HistoryCursor = 0
		a.HistoryOffset = 0
	case ScreenHelp:
		a.HelpOffset = 0
	case ScreenDetail:
		a.DetailOffset = 0
	case ScreenTimeline:
		a.TimelineCursor = 0
		a.TimelineOffset = 0
	case ScreenDiff:
		a.DiffCursor = 0
		a.DiffOffset = 0
	case ScreenProfile:
		a.ProfileCursor = 0
		a.ProfileOffset = 0
	}
}

// GotoBottom is `G` — last row.
func (a *App) GotoBottom() {
	switch a.Screen {
	case ScreenPicker:
		a.Cursor = max(0, len(a.PickerRows)-1)
	case ScreenRun:
		a.RunCursor = max(0, len(a.RunRows)-1)
		// Jumping to the end of a live run is the same intent as following it.
		a.Following = a.Run != nil && !a.Run.Finished()
	case ScreenHistory:
		a.HistoryCursor = max(0, len(a.History)-1)
	case ScreenHelp:
		// Clamped during rendering, so overshooting is harmless.
		a.HelpOffset = 1 << 20
	case ScreenDetail:
		a.DetailOffset = 1 << 20
	case ScreenTimeline:
		a.TimelineCursor = max(0, len(a.Timeline)-1)
	case ScreenDiff:
		a.DiffCursor = max(0, len(a.DiffRows)-1)
	case ScreenProfile:
		a.ProfileCursor = max(0, len(a.ProfileRows)-1)
	}
}

// wheelStep is how many rows a notch of the wheel moves.
//
// One, not the three that terminals and browsers use for scrolling a page. This is not a
// page: the wheel moves a *selection* through a list of tasks, and a selection that jumps
// three rows a notch overshoots what you were reaching for and has to be walked back. Three
// is right when the thing under the wheel is text you are reading past; one is right when it
// is a cursor you are aiming.
const wheelStep = 1

// handleWheel turns a notch of the wheel into the movement the arrow keys already do.
//
// Not a set of per-screen scroll handlers: every screen in this program already answers up
// and down — the picker, the run view, the history list, the timeline, the diff, the
// profile, and the jump and search prompts each in their own way — and a wheel that meant
// anything else on any one of them would be a second navigation model to keep in step with
// the first. Scrolling is therefore *defined* as arrowing, and everything that hangs off
// arrowing comes with it: following stops when you scroll away from what is running,
// because that is already what `k` does.
func (a *App) handleWheel(button tea.MouseButton) {
	// A confirmation reads every key that is not `y` as "no", and a wheel is not an answer
	// to a question. Scrolling past one leaves it standing.
	if a.Confirm != nil {
		return
	}
	// Typing into the run hands every key to the child, and a wheel turned into arrows
	// would reach it as `\x1b[A` — moving the selection of the `gum choose` you were only
	// scrolling up to read the question for.
	if a.SendingInput {
		return
	}

	var k Key
	switch button {
	case tea.MouseWheelUp:
		k = Key{kind: keyUp}
	case tea.MouseWheelDown:
		k = Key{kind: keyDown}
	default:
		// Horizontal wheels and tilting ones exist. Nothing here scrolls sideways.
		return
	}
	for range wheelStep {
		// The return value is "the app is quitting", which no movement key ever is.
		a.handleKey(k)
	}
}

// handleNavKey moves the cursor, on whatever screen is showing.
//
// Every screen here is a list and every list moves the same way, so the keys that move one
// are written once rather than eight times over — which is how the run view came to have no
// Home or End, and the detail panel and the `?` screen no `^d`. MoveBy, GotoTop and
// GotoBottom do the per-screen half; this is only the keys.
//
// It runs before the per-screen handlers, so a motion wins over an action rebound onto the
// same key. That is the bargain `gg` and `G` have always had, now extended to the rest: the
// motions are the one part of the keymap you can rely on without reading it.
//
// Returns true if the key was consumed.
func (a *App) handleNavKey(k Key) bool {
	if a.promptTakes(k) {
		return false
	}

	// `g` on its own only arms the pair. Everything below disarms it, and so does every key
	// that falls through, so a forgotten `g` cannot silently swallow the next keystroke.
	if k.isChar('g') {
		if a.PendingG {
			a.PendingG = false
			a.GotoTop()
		} else {
			a.PendingG = true
		}
		return true
	}
	a.PendingG = false

	switch {
	case k.isChar('j'), k.kind == keyDown:
		a.MoveBy(1)
	case k.isChar('k'), k.kind == keyUp:
		a.MoveBy(-1)
	case k.isCtrl('d'):
		a.MoveBy(a.HalfPage())
	case k.isCtrl('u'):
		a.MoveBy(-a.HalfPage())
	case k.isCtrl('f'), k.kind == keyPageDown:
		a.MoveBy(a.Page())
	case k.isCtrl('b'), k.kind == keyPageUp:
		a.MoveBy(-a.Page())
	case k.isChar('G'), k.kind == keyEnd:
		a.GotoBottom()
	case k.kind == keyHome:
		a.GotoTop()
	default:
		return false
	}
	return true
}
