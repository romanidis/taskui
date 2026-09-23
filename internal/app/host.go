package app

import (
	"maps"

	"github.com/romanidis/taskui/internal/events"
	"github.com/romanidis/taskui/internal/run"
)

// SendEventsTo attaches a host's event sink. Everything the runs do is reported to it —
// what started, how each task went, what it exited with — so an editor can fill a quickfix
// list and colour a statusline without drawing the run itself.
func (a *App) SendEventsTo(sink *events.Sink) {
	a.events = sink
	a.deltas = map[*run.Run]*events.Deltas{}
}

// HasHost reports whether a host is listening. It is what decides who opens a file: with a
// host attached, `e` hands the location over rather than launching $EDITOR inside the
// terminal the host is showing.
func (a *App) HasHost() bool { return a.events != nil }

// emit reports whatever has changed about every open run.
func (a *App) emit() {
	if a.events == nil {
		return
	}
	live := map[*run.Run]bool{}
	for _, s := range a.openSlots() {
		r := s.Run
		// A run read off disk is history being browsed, not something happening: telling
		// the host it started and exited would be news of a run that ended long ago.
		if r.IsStored() {
			continue
		}
		live[r] = true
		d := a.deltas[r]
		if d == nil {
			d = events.NewDeltas()
			a.deltas[r] = d
		}
		d.Start(a.events, r, a.Root)
		d.Flush(a.events, r)
		if r.Finished() && !d.Done() {
			d.Finish(a.events, r, s.SavedTo)
		}
	}
	// A closed or replaced run's tracker goes with it.
	maps.DeleteFunc(a.deltas, func(r *run.Run, _ *events.Deltas) bool { return !live[r] })
}
