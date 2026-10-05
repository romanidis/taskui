package app

import (
	"time"

	"github.com/romanidis/taskui/internal/keys"
	"github.com/romanidis/taskui/internal/run"
)

// ProfileTotal is the run's own elapsed time — the denominator the shares are of.
//
// The run's clock rather than the sum of the tasks': under `--output prefixed` two tasks can
// overlap, so the sum can exceed the wall clock and shares computed against it would not
// add up to anything.
func (a *App) ProfileTotal() time.Duration {
	if a.Run == nil {
		return 0
	}
	return a.Run.Elapsed()
}

// OpenProfile shows it.
func (a *App) OpenProfile() {
	if a.Run == nil {
		a.Status = "nothing running to profile"
		return
	}
	a.ProfileRows = a.Run.Profile()
	a.ProfileCursor = 0
	a.ProfileOffset = 0
	// Opened on a finished run, these figures are already the final ones.
	a.profileFinal = nil
	if a.Run.Finished() {
		a.profileFinal = a.Run
	}
	a.profileReturn = a.Screen
	a.Screen = ScreenProfile
	a.Status = ""
	if len(a.ProfileRows) == 0 {
		a.Status = "nothing in this run has finished yet"
	}
}

// RefreshProfile keeps a profile of a live run current. The Bubble Tea loop calls it on
// every tick; the headless driver has its own loop and calls it too.
//
// A profile that froze at the moment you pressed `z` would be showing a run that no longer
// exists — during a slow build, which is exactly when you would open it. It stops updating
// once the run does, so a finished profile holds still while you read it.
//
// The cursor follows its task rather than its index: the list is sorted by time, so rows
// overtake each other as the numbers move, and an index-holding cursor would drift onto
// whatever happened to slide underneath it.
func (a *App) RefreshProfile() {
	if a.Screen != ScreenProfile || a.Run == nil {
		return
	}
	// Once more after the run ends, and only once: the poll that finishes a run is the one
	// that settles its last task, and a profile that stopped a poll early went on showing
	// that task as running, at the time it had one tick before.
	if a.Run.Finished() {
		if a.profileFinal == a.Run {
			return
		}
		a.profileFinal = a.Run
	}
	on := ""
	if cost, ok := a.SelectedCost(); ok {
		on = cost.Name
	}
	a.ProfileRows = a.Run.Profile()
	for i, c := range a.ProfileRows {
		if c.Name == on {
			a.ProfileCursor = i
			return
		}
	}
	a.ProfileCursor = clamp(a.ProfileCursor, 0, max(0, len(a.ProfileRows)-1))
}

func (a *App) CloseProfile() {
	a.Screen = a.profileReturn
	if a.Screen == ScreenProfile {
		a.Screen = ScreenRun
	}
	a.Status = ""
}

func (a *App) ProfileMoveCursor(delta int) {
	if len(a.ProfileRows) == 0 {
		return
	}
	a.ProfileCursor = clamp(a.ProfileCursor+delta, 0, len(a.ProfileRows)-1)
}

// SelectedCost is the row under the cursor.
func (a *App) SelectedCost() (run.Cost, bool) {
	if a.ProfileCursor >= len(a.ProfileRows) {
		return run.Cost{}, false
	}
	return a.ProfileRows[a.ProfileCursor], true
}

// GotoProfiledTask leaves the profile for the task it names, in the run view.
func (a *App) GotoProfiledTask() {
	cost, ok := a.SelectedCost()
	if !ok {
		return
	}
	a.Screen = ScreenRun
	a.Following = false
	a.RunSetFold(cost.Name, FoldFull)
	a.RebuildRunRows()
	a.cursorToTask(cost.Name)
	a.Status = ""
}

func (a *App) handleProfileKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenProfile) }

	switch {
	case k.kind == keyEsc:
		a.CloseProfile()

	// The point of finding the slow step is going to look at it.
	case k.kind == keyEnter:
		a.GotoProfiledTask()

	case act() == keys.Edit:
		if cost, ok := a.SelectedCost(); ok {
			a.EditDefinition(cost.Name)
		}
	}
	return false
}
