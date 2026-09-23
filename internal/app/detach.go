package app

import (
	"fmt"

	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/store"
)

// Detach lets the focused run outlive taskui.
//
// The child is a session leader in its own process group, so it does not depend on taskui
// staying alive — verified rather than assumed: SIGKILL taskui and the run keeps going,
// reparented to init, still doing work. Writes to the pty whose master has closed return
// EIO and are ignored by every shell that matters. So "detached" needs to change exactly one
// thing: quitting stops signalling it.
//
// What it cannot do is keep showing you the output. Once taskui is gone the master is
// closed and everything the run prints after that is gone with it. That is why detaching
// archives what it has: the alternative is a two-hour run leaving nothing behind.
func (a *App) Detach() {
	if a.Run == nil {
		a.Status = "nothing here to detach"
		return
	}
	if a.Run.Finished() {
		a.Status = "`" + a.Run.Root + "` has already finished — nothing to let go of"
		return
	}
	if a.IsDetached(a.slot.Seq) {
		a.Status = "`" + a.Run.Root + "` is already detached — `x` still stops it"
		return
	}

	if a.detached == nil {
		a.detached = map[*run.Run]bool{}
	}
	a.detached[a.Run] = true

	// Archived now, not on quit: at quit taskui is on its way out, and a detached run has no
	// end to wait for — this is the last moment its output can be written down at all.
	// Unfinished, so `saveIfFinished` will not do it; store.Save is happy either way, and a
	// partial record beats none. Marked partial, so that if taskui is still open when the
	// run ends, the record is rewritten whole rather than stopping at this moment.
	kept := ""
	if a.SavedTo == "" {
		if dir, err := store.Save(a.stateDir, a.Root, a.Run); err == nil {
			a.SavedTo = dir
			if a.partial == nil {
				a.partial = map[*run.Run]bool{}
			}
			a.partial[a.Run] = true
			a.Outcomes = store.LastOutcomes(a.stateDir, a.Root)
			kept = " — output so far is in the archive"
		}
	}

	a.Status = fmt.Sprintf("`%s` will keep running when you quit%s", a.Run.Root, kept)
}

// IsDetached says whether a slot has been let go of.
func (a *App) IsDetached(seq uint64) bool {
	r := a.runInSlot(seq)
	return r != nil && a.detached[r]
}

// DetachedCount is how many runs would survive quitting.
func (a *App) DetachedCount() int {
	n := 0
	// A detached run that ended on its own is not something quitting has to warn about.
	for _, s := range a.openSlots() {
		if a.detached[s.Run] && !s.Run.Finished() {
			n++
		}
	}
	return n
}

// attachedRuns is every live slot that quitting is still responsible for.
func (a *App) attachedRuns() []*slot {
	var out []*slot
	for _, s := range a.openSlots() {
		if !s.Run.Finished() && !a.detached[s.Run] {
			out = append(out, s)
		}
	}
	return out
}
