package app

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
