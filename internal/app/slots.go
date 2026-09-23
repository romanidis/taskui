package app

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/theme"
)

// MaxSlots is how many runs can be open at once.
//
// The cap is not about memory — it is that every slot may own a process group, and a watch
// loop that could claim slots without bound would spawn containers without bound with it.
// Six is past what fits legibly in the slot bar anyway.
const MaxSlots = 6

// slot is one open run and how you were looking at it: where you had scrolled, what you had
// unfolded, whether you were following.
//
// The view belongs to the run rather than to the screen, because the whole point of leaving
// `docker compose logs -f` in one slot is to come back to it — and coming back to the top of
// a 20,000-line buffer is not coming back to it.
//
// The slot on screen is the app's own, embedded; the rest are parked, and switching moves
// the pointer. The view used to be loose fields on the app, copied out into the slot being
// left and back in from the one arriving, and each of the four places that opened a slot
// afresh reset its own subset of them.
type slot struct {
	Run *run.Run
	// Seq is creation order, so a run keeps its position in the slot bar for as long as it
	// is open — a bar whose entries reshuffle when you switch is unusable as a switcher.
	// Zero for the empty slot the app holds while nothing is open.
	Seq uint64
	// RunRows is the run walked into rows the way this slot shows it, and RunCursor indexes
	// it. The rows stay with the slot while it is parked because a rebuild finds the
	// cursor's row again by identity, and it can only do that among the rows the cursor was
	// in — anchoring against another run's rows put it on whatever that run had there.
	RunRows   []RunRow
	RunCursor int
	RunOffset int
	// runFolds is how much of each task's output this slot is showing. Absent means the
	// default, which is a peek.
	runFolds map[string]Fold
	// followedOpen is the task following opened by itself, so it can be given back when
	// following moves on.
	followedOpen string
	// Following: while true the view tracks whatever is running. Any manual cursor move
	// turns it off — once you have gone looking for something, the view should stop moving
	// under you.
	Following bool
	// focusedFailure exists so a failure yanks the view exactly once, not on every poll.
	focusedFailure string
	// SavedTo is where the finished run was written, if it was.
	SavedTo string
}

// newSlot is a fresh view of a run: at the top, nothing opened, following what runs.
func newSlot(r *run.Run, seq uint64) *slot {
	return &slot{Run: r, Seq: seq, runFolds: map[string]Fold{}, Following: true}
}

// SlotInfo is one entry in the slot bar.
type SlotInfo struct {
	Seq     uint64
	Root    string
	Status  run.Status
	Elapsed time.Duration
	Focused bool
}

// TakeBell reports whether the terminal should be rung, and clears the request.
func (a *App) TakeBell() bool {
	ring := a.pendingBell
	a.pendingBell = false
	return ring
}

// noteFinished rings for a run that ended while you were looking at something else.
//
// Narrow on purpose. A run you watched finish does not need announcing — you watched it —
// and the whole reason to want a bell is that you left a long one going and went to read
// something. Each slot rings once: a finished run stays finished, and polling it forty times
// a second is not forty pieces of news.
func (a *App) noteFinished() {
	if a.Bell == theme.BellNever {
		return
	}
	if a.belled == nil {
		a.belled = map[*run.Run]bool{}
	}
	for _, s := range a.openSlots() {
		r := s.Run
		if !r.Finished() || a.belled[r] {
			continue
		}
		a.belled[r] = true
		// Watching it happen is not news.
		if a.Screen == ScreenRun && s == a.slot {
			continue
		}
		if a.Bell == theme.BellFailed && r.Outcome() == run.Ok {
			continue
		}
		a.pendingBell = true
	}
}

// openSlots is every open slot: the parked ones in the order they were parked, then the one
// on screen. That is the order a host hears about them in, so when two runs end on the same
// poll, the one you are looking at is the one the host ends up speaking for.
func (a *App) openSlots() []*slot {
	if a.Run == nil {
		return a.Parked
	}
	return append(slices.Clip(a.Parked), a.slot)
}

// runInSlot is whichever run occupies a slot, focused or parked.
func (a *App) runInSlot(seq uint64) *run.Run {
	for _, s := range a.openSlots() {
		if s.Seq == seq {
			return s.Run
		}
	}
	return nil
}

// claimSlot finds the slot this run should go in, parking whatever is on screen to make
// room.
//
// Slots are keyed by task name, which is what makes the bar readable — `▶ up` is always
// the same run of `up`. Re-running a task therefore reuses its slot and keeps its
// position, rather than pushing a near-duplicate entry alongside it.
//
// It returns the sequence number to give the new run, or the reason it cannot start.
func (a *App) claimSlot(name string) (uint64, string) {
	switch s := a.taskSlot(name); {
	case s == a.slot:
		// Restarting the run already on screen. The caller replaces it and retires the old
		// one, which is what a restart is.
		return s.Seq, ""
	case s != nil:
		// Restarting one that was parked: take its slot back so it does not move.
		a.retire(s.Run)
		a.Parked = slices.DeleteFunc(a.Parked, func(p *slot) bool { return p == s })
		a.parkFocused()
		return s.Seq, ""
	}
	// A genuinely new slot.
	if len(a.openSlots()) >= MaxSlots && !a.recycleSlot() {
		return 0, fmt.Sprintf("all %d run slots are busy — stop one with `x` first", MaxSlots)
	}
	a.parkFocused()
	a.nextSeq++
	return a.nextSeq, ""
}

// slotAvailable reports whether starting name would find it a slot — the same answer
// claimSlot will give, without claiming anything. A batch asks it per task rather than
// counting free slots up front, because the count kept disagreeing with the claim: a
// task already in a slot reuses it, and a finished slot is given up on demand.
func (a *App) slotAvailable(name string) bool {
	return a.slotRun(name) != nil || len(a.openSlots()) < MaxSlots || a.recyclable() >= 0
}

// recyclable is the parked slot a new run may take when every slot is open — the first
// that has finished — or -1 for none, or len(a.Parked) for the focused run.
//
// A finished run has already been archived, so reclaiming its slot loses nothing you
// cannot reopen from history. A live one is somebody's compose stack; it is never taken
// without being asked. The one on screen counts too: six finished slots with the focused
// one among them are six finished slots.
func (a *App) recyclable() int {
	for i, p := range a.Parked {
		if p.Run.Finished() {
			return i
		}
	}
	if a.Run != nil && a.Run.Finished() {
		return len(a.Parked)
	}
	return -1
}

// recycleSlot gives up the slot recyclable names, and reports whether there was one.
func (a *App) recycleSlot() bool {
	switch i := a.recyclable(); {
	case i < 0:
		return false
	case i == len(a.Parked):
		a.slot = newSlot(nil, 0)
	default:
		a.Parked = append(a.Parked[:i], a.Parked[i+1:]...)
	}
	return true
}

// claimStoredSlot is where a run read off disk goes.
//
// Deliberately not claimSlot: that keys on task name, and an archived `deploy` would then
// take the slot of the `deploy` you have running right now and kill it. Browsing history
// instead reuses one slot, so paging through twenty old runs does not bury the live ones.
func (a *App) claimStoredSlot() (uint64, string) {
	if a.Run != nil && a.Run.IsStored() {
		return a.slot.Seq, ""
	}
	for i, p := range a.Parked {
		if p.Run.IsStored() {
			seq := p.Seq
			a.Parked = append(a.Parked[:i], a.Parked[i+1:]...)
			a.parkFocused()
			return seq, ""
		}
	}
	if len(a.openSlots()) >= MaxSlots && !a.recycleSlot() {
		return 0, fmt.Sprintf("all %d run slots are busy — stop one with `x` first", MaxSlots)
	}
	a.parkFocused()
	a.nextSeq++
	return a.nextSeq, ""
}

// parkFocused moves the slot on screen into the parking lot, view and all.
func (a *App) parkFocused() {
	if a.Run == nil {
		return
	}
	a.Parked = append(a.Parked, a.slot)
	a.slot = newSlot(nil, 0)
}

// show puts a slot on screen.
//
// Its rows come with it, so the rebuild finds the cursor's row again among the rows it was
// on. The search hits cannot: they are indices into the run just left, and running the
// query again against this one is both cheap and what keeping the query meant.
func (a *App) show(s *slot) {
	a.slot = s
	a.refreshSearch()
	a.RebuildRunRows()
}

// Slots lists every open run, in slot-bar order.
func (a *App) Slots() []SlotInfo {
	describe := func(r *run.Run, seq uint64, focused bool) SlotInfo {
		status := r.Outcome()
		elapsed := time.Since(r.Started)
		if r.HasDuration {
			elapsed = r.Duration
		}
		return SlotInfo{Seq: seq, Root: r.Root, Status: status, Elapsed: elapsed, Focused: focused}
	}
	open := a.openSlots()
	out := make([]SlotInfo, 0, len(open))
	for _, s := range open {
		out = append(out, describe(s.Run, s.Seq, s == a.slot))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// FocusSlot puts a slot on screen, parking the one that was.
func (a *App) FocusSlot(seq uint64) {
	if a.Run != nil && seq == a.slot.Seq {
		return
	}
	at := -1
	for i, p := range a.Parked {
		if p.Seq == seq {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	target := a.Parked[at]
	a.Parked = append(a.Parked[:at], a.Parked[at+1:]...)
	a.parkFocused()
	a.show(target)
	a.Screen = ScreenRun
	a.Status = ""
}

// focusTask focuses whichever slot holds this task, if one does.
func (a *App) focusTask(name string) {
	if s := a.taskSlot(name); s != nil {
		a.FocusSlot(s.Seq)
	}
}

// CycleSlot steps through the slot bar. delta wraps, because with two slots — the usual
// case, a stack and the thing you are running against it — one key that always goes to the
// other one beats two keys that go in directions you have to think about.
func (a *App) CycleSlot(delta int) {
	slots := a.Slots()
	if len(slots) < 2 {
		a.Status = "only one run open"
		return
	}
	at := 0
	for i, s := range slots {
		if s.Seq == a.slot.Seq {
			at = i
		}
	}
	n := len(slots)
	next := ((at+delta)%n + n) % n
	a.FocusSlot(slots[next].Seq)
}

// FocusSlotNumber jumps straight to slot n, counting from one as the bar labels them.
func (a *App) FocusSlotNumber(n int) {
	slots := a.Slots()
	if n < 1 || n > len(slots) {
		a.Status = fmt.Sprintf("no slot %d", n)
		return
	}
	a.FocusSlot(slots[n-1].Seq)
}

// CloseSlot closes the slot on screen and falls back to the most recent of the rest.
//
// A live run is never closed out from under you — stopping it is a separate, deliberate
// key, and conflating the two would make a mistyped `X` kill a deploy.
func (a *App) CloseSlot() {
	if a.Run == nil {
		return
	}
	if !a.Run.Finished() {
		a.Status = "still running — stop it with `x` before closing it"
		return
	}

	newest := -1
	for i, p := range a.Parked {
		if newest < 0 || p.Seq > a.Parked[newest].Seq {
			newest = i
		}
	}
	if newest < 0 {
		a.slot = newSlot(nil, 0)
		a.ClearSearch()
		a.Screen = ScreenPicker
		a.Status = "no runs open"
		return
	}
	target := a.Parked[newest]
	a.Parked = append(a.Parked[:newest], a.Parked[newest+1:]...)
	a.show(target)
}

// taskSlot is the slot holding this task, on screen or parked: the one running it if there
// is one, and otherwise an old run of it opened from history.
//
// A run read off disk is history being browsed rather than the task's slot, so it never
// stands in for a live one. It used to, whenever it was the one on screen: with `up` going
// in one slot and last week's `up` open in the other, the picker said `up` was not
// running, `⏎` started a second copy beside the first, and `x` answered that it had
// already finished.
func (a *App) taskSlot(name string) *slot {
	var stored *slot
	for _, s := range a.openSlots() {
		if s.Run.Root != name {
			continue
		}
		if !s.Run.IsStored() {
			return s
		}
		stored = s
	}
	return stored
}

// slotRun is the run in this task's slot, whether it is on screen or parked.
func (a *App) slotRun(name string) *run.Run {
	if s := a.taskSlot(name); s != nil {
		return s.Run
	}
	return nil
}

func (a *App) liveSlot(name string) bool {
	r := a.slotRun(name)
	return r != nil && !r.Finished()
}

// RunningFor is how long this task has been running, if it is running at all.
//
// The picker's outcome column answers "how did it go last time", which is a different
// question from "is it going now" — and with runs living in slots that are not on screen,
// "now" was the one thing the task list stayed silent about. A task running this second
// looked exactly like one that ran yesterday.
func (a *App) RunningFor(name string) (time.Duration, bool) {
	r := a.slotRun(name)
	if r == nil || r.Finished() {
		return 0, false
	}
	return time.Since(r.Started), true
}
