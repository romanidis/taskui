package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romanidis/taskui/internal/events"
	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/store"
)

type bufferSink struct{ *bytes.Buffer }

func (bufferSink) Close() error { return nil }

func eventTypes(buf *bytes.Buffer) map[string]int {
	out := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out[m["type"].(string)]++
		}
	}
	return out
}

func oneTaskRun(root string) *run.Run {
	r := run.Detached(root, run.GraphFrom(run.Edge{Parent: root}))
	r.Feed(root, "output")
	return r
}

// A re-run of the same task is a new run to the host: it has to hear it start and end,
// or the statusline stays on the last run's ✗ after the fix went green.
func TestTheHostHearsEveryRunOfATask(t *testing.T) {
	a := sample(t)
	var buf bytes.Buffer
	a.SendEventsTo(events.New(bufferSink{&buf}))

	first := oneTaskRun("test")
	a.OpenRunForTest(first)
	first.Finish(1)
	a.emit()

	a.Run = oneTaskRun("test")
	a.Run.Finish(0)
	a.emit()

	got := eventTypes(&buf)
	if got["run"] != 2 || got["exit"] != 2 {
		t.Errorf("events = %v, want two runs and two exits", got)
	}
}

// Browsing history is not a run happening.
func TestAStoredRunIsNotAnnouncedToTheHost(t *testing.T) {
	a := sample(t)
	var buf bytes.Buffer
	a.SendEventsTo(events.New(bufferSink{&buf}))

	a.OpenRunForTest(run.FromStored(run.Stored{Root: "test", Graph: run.GraphFrom(run.Edge{Parent: "test"})}))
	a.emit()

	if got := eventTypes(&buf); len(got) != 0 {
		t.Errorf("a stored run sent %v", got)
	}
}

// Restarting a task reuses its slot. What the old run had done — rung, been let go of —
// is not what the new one has done.
func TestARestartDoesNotInheritTheOldRunsBellOrDetach(t *testing.T) {
	a := sample(t)
	a.Bell = 0 // unwatched
	old := oneTaskRun("up")
	a.OpenRunForTest(old)
	a.Screen = ScreenRun
	press(a, Char('A'))
	if !a.IsDetached(a.slot.Seq) {
		t.Fatal("not detached")
	}
	a.Screen = ScreenPicker
	old.Finish(0)
	a.noteFinished()
	if !a.TakeBell() {
		t.Fatal("a run finishing unwatched should ring")
	}

	restarted := oneTaskRun("up")
	a.Run = restarted
	if a.IsDetached(a.slot.Seq) {
		t.Error("the restarted run was left detached, so quitting would leave it running")
	}
	restarted.Finish(0)
	a.noteFinished()
	if !a.TakeBell() {
		t.Error("the restarted run finished unwatched and did not ring")
	}
}

// Detaching archives what the run has so far. If taskui is still open when it ends, the
// record is finished off in place — one entry, the whole output, the real outcome.
func TestADetachedRunIsArchivedWholeWhenItFinishes(t *testing.T) {
	a := sample(t)
	r := oneTaskRun("build")
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	press(a, Char('A'))
	if a.SavedTo == "" {
		t.Fatal("detaching should have archived the output so far")
	}

	r.Feed("build", "the rest")
	r.ApplyFailed("build")
	r.Finish(1)
	a.archiveIfFinished(a.slot)

	runs := store.List(a.StateDir())
	if len(runs) != 1 {
		t.Fatalf("history holds %d runs, want the one", len(runs))
	}
	if runs[0].Exit != 1 {
		t.Errorf("exit = %d, want the one it finished with", runs[0].Exit)
	}
	text, err := os.ReadFile(filepath.Join(a.SavedTo, "build.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "the rest") {
		t.Errorf("the archive stops at the detach: %q", text)
	}
}

// Every slot open, all of them finished: a finished slot is given up on demand, the one on
// screen included, so a new task still has somewhere to go.
func TestSixFinishedSlotsStillTakeANewRun(t *testing.T) {
	a := sample(t)
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		r := oneTaskRun(name)
		r.Finish(0)
		a.OpenRunForTest(r)
	}
	if len(a.openSlots()) != MaxSlots {
		t.Fatalf("open = %d", len(a.openSlots()))
	}
	if !a.slotAvailable("new") {
		t.Error("a finished slot is recyclable")
	}

	a.Parked = a.Parked[:0]
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		a.Parked = append(a.Parked, newSlot(oneTaskRun(name), uint64(len(a.Parked)+1)))
	}
	// Five live parked runs; the one on screen is the only finished slot, and it counts.
	if !a.slotAvailable("new") {
		t.Error("the finished run on screen is a free slot")
	}
	if _, why := a.newSlotSeq(); why != "" || a.Run != nil {
		t.Errorf("a new slot should take the finished run on screen's place: %q", why)
	}
}

func TestSixLiveSlotsTakeNothingNew(t *testing.T) {
	a := sample(t)
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		a.OpenRunForTest(oneTaskRun(name))
	}
	a.marked = map[string]bool{"a": true, "new": true}
	a.RunMarked()
	if !strings.Contains(a.Status, "every slot is taken") {
		t.Errorf("status = %q; a live marked task frees no slot for the other", a.Status)
	}
}

func TestDecliningABatchSaysItWasNotRun(t *testing.T) {
	a := sample(t)
	a.Confirm = ConfirmRunSet{
		Set:       RunSet{Runs: []invocation{{name: "deploy"}}, Marked: true},
		Dangerous: []string{"deploy"},
	}
	a.ConfirmNo()
	if a.Status != "not run" {
		t.Errorf("status = %q", a.Status)
	}
}

// Coming back to a slot puts the cursor back on the row you left, found in that slot's own
// rows rather than in the rows of the one you were just looking at.
func TestSwitchingBackToASlotFindsItsOwnRow(t *testing.T) {
	a := sample(t)
	// The same task at different rows in the two runs: `test` is row 1 here and row 2 in
	// the other, which is where this one's cursor sits.
	mine := run.Detached("b", run.GraphFrom(run.Edge{Parent: "b", Children: []string{"test", "lint"}}))
	a.OpenRunForTest(mine)
	a.cursorToTask("lint")
	seq, want := a.slot.Seq, a.RunRows[a.RunCursor].Name

	other := run.Detached("a", run.GraphFrom(run.Edge{Parent: "a", Children: []string{"y", "test"}}))
	a.OpenRunForTest(other)
	a.FocusSlot(seq)

	if got := a.RunRows[a.RunCursor].Name; got != want {
		t.Errorf("cursor on %q, want %q", got, want)
	}
}

// A run that kept printing while you were in another slot has moved every row below what it
// printed. Coming back finds the line you were reading, not whatever slid into its place.
func TestComingBackToASlotFindsTheLineItWasOn(t *testing.T) {
	a := sample(t)
	r := run.Detached("b", run.GraphFrom(run.Edge{Parent: "b", Children: []string{"early", "late"}}))
	r.Feed("early", "e0")
	for _, l := range []string{"l0", "l1", "l2"} {
		r.Feed("late", l)
	}
	a.OpenRunForTest(r)
	a.RunSetFold("early", FoldFull)
	for i, row := range a.RunRows {
		if !row.IsTask && row.Task == "late" && row.Index == 1 {
			a.RunCursor = i
		}
	}
	seq := a.slot.Seq

	a.OpenRunForTest(oneTaskRun("other"))
	for _, l := range []string{"e1", "e2", "e3"} {
		r.Feed("early", l)
	}
	a.FocusSlot(seq)

	if row := a.RunRows[a.RunCursor]; row.IsTask || row.Task != "late" || row.Index != 1 {
		t.Errorf("cursor on %+v, want the second line of late", row)
	}
}

// A run read off disk is history being browsed, not the task's slot. With `backend:lint`
// going in one slot and an old run of it open in another, the task is still the one
// running: `⏎` on it goes to that run rather than starting a second beside it, and `x`
// stops it rather than answering that it has already finished.
func TestAnOldRunOfATaskDoesNotStandInForTheLiveOne(t *testing.T) {
	for _, tc := range []struct {
		name     string
		liveLast bool
	}{
		{"with the old run on screen", false},
		{"with the live run on screen", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := sample(t)
			live := oneTaskRun("backend:lint")
			old := run.FromStored(run.Stored{
				Root:  "backend:lint",
				Graph: run.GraphFrom(run.Edge{Parent: "backend:lint"}),
			})
			opened := []*run.Run{live, old}
			if tc.liveLast {
				opened = []*run.Run{old, live}
			}
			for _, r := range opened {
				a.OpenRunForTest(r)
			}
			a.Screen = ScreenPicker
			defer a.KillAll()

			if _, running := a.RunningFor("backend:lint"); !running {
				t.Error("the picker says the task is not running")
			}
			a.RequestRun("backend:lint", nil)
			if !strings.Contains(a.Status, "already running") {
				t.Errorf("status = %q", a.Status)
			}
			if a.Run != live {
				t.Error("the run in focus is not the one going")
			}
			for _, s := range a.openSlots() {
				if s.Run != live && !s.Run.IsStored() {
					t.Error("a second run of the task was started beside the first")
				}
			}
			a.CancelTask("backend:lint")
			if !live.Cancelled() {
				t.Errorf("`x` did not reach the live run: %q", a.Status)
			}
		})
	}
}

// `r` on an old run of a task that is going again asks the restart question, and yes has to
// restart the run it asked about — not leave it going and start a second copy in the old
// run's slot.
func TestRerunningAnOldRunRestartsTheLiveOne(t *testing.T) {
	a := sample(t)
	live := oneTaskRun("backend:lint")
	a.OpenRunForTest(live)
	old := run.FromStored(run.Stored{Root: "backend:lint", Graph: run.GraphFrom(run.Edge{Parent: "backend:lint"})})
	a.OpenRunForTest(old)
	a.Screen = ScreenRun
	defer a.KillAll()

	a.RerunSelected()
	if c, ok := a.Confirm.(ConfirmRun); !ok || c.Reason != WouldStopRunning {
		t.Fatalf("confirm = %+v, want the restart question", a.Confirm)
	}
	a.ConfirmYes()

	if !live.Cancelled() {
		t.Error("the run the question was about is still going")
	}
	going := 0
	for _, s := range a.openSlots() {
		if s.Run == old {
			continue
		}
		if s.Run == live {
			t.Error("the restarted run is still in a slot")
		}
		going++
	}
	if going != 1 {
		t.Errorf("%d runs of the task are open beside the old one, want the restart", going)
	}
}

// A jump opens folds on its way to a match, which moves every row below them. `esc` has
// to put the cursor back on the row it started on, not on whatever slid into its place.
func TestCancellingAJumpReturnsToTheRowItLeft(t *testing.T) {
	a := appWith(t, []string{"a:x", "a:y", "b:x", "b:y"})
	a.SetFoldAll(false)
	at := -1
	for i, row := range a.PickerRows {
		if row.Tree >= 0 && a.Tree.Nodes[a.Rows[row.Tree].Node].Label == "b" {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("no row for group b")
	}
	a.Cursor = at

	a.BeginJump()
	typeText(a, "a:y")
	a.CancelJump()

	if got := a.Tree.Nodes[a.Rows[a.PickerRows[a.Cursor].Tree].Node].Label; got != "b" {
		t.Errorf("cursor on %q after esc, want b", got)
	}
}

// The poll that ends a run is the one that settles its last task; a profile that stopped
// refreshing a poll early kept that task running forever.
func TestAProfileOpenAcrossTheEndOfARunShowsHowItEnded(t *testing.T) {
	a := sample(t)
	r := oneTaskRun("build")
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	a.OpenProfile()

	r.Finish(0)
	a.refreshProfile()
	for _, c := range a.ProfileRows {
		if c.Name == "build" && c.Status == run.Running {
			t.Error("the profile still shows the run as going")
		}
	}
}
