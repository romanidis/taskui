package app

import (
	"fmt"
	"maps"
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

// slotView is the view state that belongs to a run rather than to the app: where you had
// scrolled, what you had unfolded, whether you were following.
//
// Kept per slot because the whole point of leaving `docker compose logs -f` in one slot is
// to come back to it — and coming back to the top of a 20,000-line buffer is not coming
// back to it.
type slotView struct {
	cursor         int
	offset         int
	folds          map[string]Fold
	followedOpen   string
	following      bool
	focusedFailure string
	savedTo        string
}

// Parked is a run that is not the one on screen. Its capture goroutine keeps draining and
// its process keeps going: parking is a state of the UI, not of the child.
type Parked struct {
	Run *run.Run
	// Seq is creation order, so a run keeps its position in the slot bar for as long as it
	// is open — a bar whose entries reshuffle when you switch is unusable as a switcher.
	Seq  uint64
	view slotView
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
	for _, slot := range a.Slots() {
		r := a.runInSlot(slot.Seq)
		if r == nil || !r.Finished() || a.belled[r] {
			continue
		}
		a.belled[r] = true
		// Watching it happen is not news.
		if a.Screen == ScreenRun && a.FocusSeq == slot.Seq {
			continue
		}
		if a.Bell == theme.BellFailed && r.Exit == 0 {
			continue
		}
		a.pendingBell = true
	}
}

// runInSlot is whichever run occupies a slot, focused or parked.
func (a *App) runInSlot(seq uint64) *run.Run {
	if a.FocusSeq == seq {
		return a.Run
	}
	for _, p := range a.Parked {
		if p.Seq == seq {
			return p.Run
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
	// Restarting the run already on screen. The caller replaces it and retires the old
	// one, which is what a restart is.
	if a.Run != nil && a.Run.Root == name {
		return a.FocusSeq, ""
	}
	// Restarting one that was parked: take its slot back so it does not move.
	for i, p := range a.Parked {
		if p.Run.Root == name {
			seq := p.Seq
			a.retire(p.Run)
			a.Parked = append(a.Parked[:i], a.Parked[i+1:]...)
			a.parkFocused()
			return seq, ""
		}
	}
	// A genuinely new slot.
	if a.openSlots() >= MaxSlots && !a.recycleSlot() {
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
	return a.slotRun(name) != nil || a.openSlots() < MaxSlots || a.recyclable() >= 0
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
		a.Run = nil
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
		return a.FocusSeq, ""
	}
	for i, p := range a.Parked {
		if p.Run.IsStored() {
			seq := p.Seq
			a.Parked = append(a.Parked[:i], a.Parked[i+1:]...)
			a.parkFocused()
			return seq, ""
		}
	}
	if a.openSlots() >= MaxSlots && !a.recycleSlot() {
		return 0, fmt.Sprintf("all %d run slots are busy — stop one with `x` first", MaxSlots)
	}
	a.parkFocused()
	a.nextSeq++
	return a.nextSeq, ""
}

func (a *App) openSlots() int {
	n := len(a.Parked)
	if a.Run != nil {
		n++
	}
	return n
}

// parkFocused moves the run on screen into the parking lot, keeping its view state with it.
func (a *App) parkFocused() {
	if a.Run == nil {
		return
	}
	a.Parked = append(a.Parked, Parked{Run: a.Run, Seq: a.FocusSeq, view: a.snapshotView()})
	a.Run = nil
}

func (a *App) snapshotView() slotView {
	folds := make(map[string]Fold, len(a.runFolds))
	maps.Copy(folds, a.runFolds)
	return slotView{
		cursor:         a.RunCursor,
		offset:         a.RunOffset,
		folds:          folds,
		followedOpen:   a.followedOpen,
		following:      a.Following,
		focusedFailure: a.focusedFailure,
		savedTo:        a.SavedTo,
	}
}

func (a *App) restoreView(v slotView) {
	// The rows on hand are the slot being left. RebuildRunRows anchors the cursor to the row
	// it is on, and anchoring the restored index against another run's rows put it on
	// whatever that run had there — `lint` in one slot came back as `test`.
	a.RunRows = nil
	a.RunCursor = v.cursor
	a.RunOffset = v.offset
	a.runFolds = v.folds
	if a.runFolds == nil {
		a.runFolds = map[string]Fold{}
	}
	a.followedOpen = v.followedOpen
	a.Following = v.following
	a.focusedFailure = v.focusedFailure
	a.SavedTo = v.savedTo
	// The query survives a switch but its hits cannot: they are indices into the run you
	// just left. Re-running it against the new one is both cheap and what you meant by
	// keeping the query.
	a.refreshSearch()
	a.RebuildRunRows()
}

// Slots lists every open run, in slot-bar order.
func (a *App) Slots() []SlotInfo {
	describe := func(r *run.Run, seq uint64, focused bool) SlotInfo {
		status := run.Running
		if r.Finished() {
			status = run.Failed
			if r.Exit == 0 {
				status = run.Ok
			}
		}
		elapsed := time.Since(r.Started)
		if r.HasDuration {
			elapsed = r.Duration
		}
		return SlotInfo{Seq: seq, Root: r.Root, Status: status, Elapsed: elapsed, Focused: focused}
	}
	out := make([]SlotInfo, 0, a.openSlots())
	for _, p := range a.Parked {
		out = append(out, describe(p.Run, p.Seq, false))
	}
	if a.Run != nil {
		out = append(out, describe(a.Run, a.FocusSeq, true))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// FocusSlot puts a slot on screen, parking the one that was.
func (a *App) FocusSlot(seq uint64) {
	if a.Run != nil && seq == a.FocusSeq {
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
	a.Run = target.Run
	a.FocusSeq = target.Seq
	a.restoreView(target.view)
	a.Screen = ScreenRun
	a.Status = ""
}

// focusTask focuses whichever slot holds this task, if one does.
func (a *App) focusTask(name string) {
	for _, p := range a.Parked {
		if p.Run.Root == name {
			a.FocusSlot(p.Seq)
			return
		}
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
		if s.Seq == a.FocusSeq {
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
	a.Run = nil

	newest := -1
	for i, p := range a.Parked {
		if newest < 0 || p.Seq > a.Parked[newest].Seq {
			newest = i
		}
	}
	if newest < 0 {
		a.FocusSeq = 0
		a.RunRows = nil
		a.RunCursor = 0
		a.RunOffset = 0
		a.runFolds = map[string]Fold{}
		a.focusedFailure = ""
		a.SavedTo = ""
		a.ClearSearch()
		a.Screen = ScreenPicker
		a.Status = "no runs open"
		return
	}
	target := a.Parked[newest]
	a.Parked = append(a.Parked[:newest], a.Parked[newest+1:]...)
	a.Run = target.Run
	a.FocusSeq = target.Seq
	a.restoreView(target.view)
}

// slotRun is the slot holding this task, whether it is on screen or parked.
func (a *App) slotRun(name string) *run.Run {
	if a.Run != nil && a.Run.Root == name {
		return a.Run
	}
	for _, p := range a.Parked {
		if p.Run.Root == name {
			return p.Run
		}
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
