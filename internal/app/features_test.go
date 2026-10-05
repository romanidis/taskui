package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/romanidis/taskui/internal/pivot"
	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/store"
	"github.com/romanidis/taskui/internal/task"
	"github.com/romanidis/taskui/internal/theme"
	"github.com/romanidis/taskui/internal/watch"
)

// --- marks ---------------------------------------------------------------------------

func TestMarkingSeveralTasksAndRunningTheSet(t *testing.T) {
	a := sample(t)
	parkOn(t, a, "backend:lint")
	press(a, Char('m'))
	parkOn(t, a, "app:lint")
	press(a, Char('m'))

	marked := a.Marked()
	if len(marked) != 2 {
		t.Fatalf("marked %v", marked)
	}
	if !a.IsMarked("backend:lint") || !a.IsMarked("app:lint") {
		t.Errorf("wrong set: %v", marked)
	}
	// `m` again takes it back.
	press(a, Char('m'))
	if a.IsMarked("app:lint") {
		t.Error("the second press should have unmarked it")
	}
}

// With a set chosen, `⏎` runs the set. Running whatever the cursor is on instead would
// quietly discard the choice.
func TestEnterRunsTheMarkedSetRatherThanTheCursor(t *testing.T) {
	a := sample(t)
	parkOn(t, a, "backend:lint")
	press(a, Char('m'))
	parkOn(t, a, "app:lint")
	press(a, Char('m'))
	// The cursor is on app:lint; both are marked.
	parkOn(t, a, "site:build")

	a.RunMarked()
	if len(a.marked) != 0 {
		t.Error("marks should be spent once run")
	}
	// Two slots, neither of them the task under the cursor.
	roots := map[string]bool{}
	for _, s := range a.Slots() {
		roots[s.Root] = true
	}
	if !roots["backend:lint"] || !roots["app:lint"] {
		t.Errorf("slots hold %v", roots)
	}
	if roots["site:build"] {
		t.Error("it ran the cursor's task instead of the marked set")
	}
}

func TestClearingMarks(t *testing.T) {
	a := sample(t)
	parkOn(t, a, "backend:lint")
	press(a, Char('m'))
	press(a, Char('M'))
	if len(a.Marked()) != 0 {
		t.Errorf("still marked: %v", a.Marked())
	}
}

// A batch that quietly did less than you asked is worse than one that says so.
func TestABatchTooBigForTheSlotsSaysWhatItLeft(t *testing.T) {
	a := sample(t)
	for _, name := range []string{
		"all", "build", "fmt", "lint", "app:build", "app:fmt", "app:lint",
	} {
		parkOn(t, a, name)
		press(a, Char('m'))
	}
	if len(a.Marked()) != 7 {
		t.Fatalf("marked %d", len(a.Marked()))
	}

	a.RunMarked()
	if got := len(a.Slots()); got != MaxSlots {
		t.Errorf("filled %d slots, want %d", got, MaxSlots)
	}
	if !strings.Contains(a.Status, "no slots free") {
		t.Errorf("status = %q", a.Status)
	}
}

// A group is not a task, and marking one should say so rather than doing nothing.
func TestMarkingAGroupSaysItIsNotATask(t *testing.T) {
	a := sample(t)
	a.SetFoldAll(true)
	a.Cursor = groupRow(t, a)
	press(a, Char('m'))
	if len(a.Marked()) != 0 {
		t.Error("marked a group")
	}
	if !strings.Contains(a.Status, "not one") {
		t.Errorf("status = %q", a.Status)
	}
}

// --- the profile ---------------------------------------------------------------------

// timed builds a finished run whose tasks took known amounts of time.
func timed(t *testing.T, a *App) {
	t.Helper()
	r := run.Detached("all", run.GraphFrom(
		run.Edge{Parent: "all", Children: []string{"fmt", "build"}},
		run.Edge{Parent: "build", Children: []string{"compile"}},
	))
	for _, name := range []string{"all", "fmt", "build", "compile"} {
		r.Feed(name, "working")
	}
	r.Finish(0)
	// Settled durations, set directly: a test must not depend on real elapsed time.
	r.Tasks["all"].SetDurationForTest(1000 * time.Millisecond)
	r.Tasks["fmt"].SetDurationForTest(200 * time.Millisecond)
	r.Tasks["build"].SetDurationForTest(700 * time.Millisecond)
	r.Tasks["compile"].SetDurationForTest(600 * time.Millisecond)
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
}

// Self time is the whole point. Ranked by total duration, every aggregate outranks every
// task that did any work, and the profile says `all` is the slow one — which is true and
// useless.
func TestTheProfileRanksBySelfTimeNotTotal(t *testing.T) {
	a := sample(t)
	timed(t, a)
	press(a, Char('T'))

	if a.Screen != ScreenProfile {
		t.Fatalf("screen = %v", a.Screen)
	}
	if len(a.ProfileRows) == 0 {
		t.Fatal("no rows")
	}
	if got := a.ProfileRows[0].Name; got != "compile" {
		t.Errorf("slowest is %q, want compile — the one that did the work", got)
	}

	by := map[string]Cost{}
	for _, c := range a.ProfileRows {
		by[c.Name] = c
	}
	// `all` took a second, of which 900ms was its children's.
	if got := by["all"].Self; got != 100*time.Millisecond {
		t.Errorf("all self = %v, want 100ms", got)
	}
	if got := by["all"].Duration; got != time.Second {
		t.Errorf("all duration = %v, want its full second", got)
	}
	// `build` took 700ms and `compile` accounted for 600 of them.
	if got := by["build"].Self; got != 100*time.Millisecond {
		t.Errorf("build self = %v, want 100ms", got)
	}
	// A leaf keeps all of its own time.
	if got := by["compile"].Self; got != 600*time.Millisecond {
		t.Errorf("compile self = %v", got)
	}
}

// A parent that overlapped its children can subtract past zero. It did not spend negative
// time.
func TestSelfTimeNeverGoesNegative(t *testing.T) {
	a := sample(t)
	timed(t, a)
	a.Run.Tasks["all"].SetDurationForTest(10 * time.Millisecond)
	for _, c := range a.Profile() {
		if c.Self < 0 {
			t.Errorf("%s has self time %v", c.Name, c.Self)
		}
	}
}

func TestEnterOnTheProfileGoesToThatTask(t *testing.T) {
	a := sample(t)
	timed(t, a)
	press(a, Char('T'))
	press(a, Enter())

	if a.Screen != ScreenRun {
		t.Fatalf("screen = %v", a.Screen)
	}
	name, ok := a.RunSelectedTask()
	if !ok || name != "compile" {
		t.Errorf("landed on %q (%v), want compile", name, ok)
	}
}

// A profile that froze when you pressed `z` would be describing a run that has moved on —
// during a slow build, which is when you would open it.
func TestALiveProfileKeepsUp(t *testing.T) {
	a := sample(t)
	r := run.Detached("all", run.GraphFrom(run.Edge{Parent: "all", Children: []string{"slow"}}))
	r.Feed("slow", "starting")
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	press(a, Char('T'))

	before := len(a.ProfileRows)
	r.Feed("other", "appeared")
	a.RefreshLive()
	if len(a.ProfileRows) <= before {
		t.Errorf("rows went %d → %d; a live profile should have picked the new task up",
			before, len(a.ProfileRows))
	}
}

func TestAFinishedProfileHoldsStill(t *testing.T) {
	a := sample(t)
	timed(t, a)
	press(a, Char('T'))
	rows := len(a.ProfileRows)
	a.Run.Feed("sneaky", "after the fact")
	a.RefreshLive()
	if len(a.ProfileRows) != rows {
		t.Error("a finished profile moved under the cursor")
	}
}

// --- detaching -------------------------------------------------------------------------

func TestDetachingTakesARunOutOfWhatQuittingStops(t *testing.T) {
	a := sample(t)
	r := run.Detached("forever", run.GraphFrom(run.Edge{Parent: "forever"}))
	r.Feed("forever", "tick")
	a.OpenRunForTest(r)
	a.Screen = ScreenRun

	if a.InFlightCount() != 1 {
		t.Fatalf("in flight = %d", a.InFlightCount())
	}
	press(a, Char('A'))

	if !a.IsDetached(a.slot.Seq) {
		t.Fatal("not detached")
	}
	if a.InFlightCount() != 0 {
		t.Errorf("quitting is still responsible for %d runs", a.InFlightCount())
	}
	if a.AnyInFlight() {
		t.Error("AnyInFlight should not count a detached run — it is what quitting waits on")
	}
	// It is still running, which is a different question.
	if !a.AnyRunning() {
		t.Error("AnyRunning should still see it")
	}
	if a.DetachedCount() != 1 {
		t.Errorf("detached count = %d", a.DetachedCount())
	}
}

// Detaching is not a promise never to stop it — `x` still reaches a detached run. It is
// only the blanket that no longer covers it.
func TestStoppingEverythingLeavesADetachedRunAlone(t *testing.T) {
	a := sample(t)
	r := run.Detached("forever", run.GraphFrom(run.Edge{Parent: "forever"}))
	r.Feed("forever", "tick")
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	press(a, Char('A'))

	a.CancelAll()
	if r.Cancelled() {
		t.Error("CancelAll reached a detached run")
	}
	// …but the direct key still does.
	a.CancelRun()
	if !r.Cancelled() {
		t.Error("`x` should still stop it")
	}
}

func TestDetachingAFinishedRunSaysThereIsNothingToLetGoOf(t *testing.T) {
	a := sample(t)
	r := run.Detached("done", run.GraphFrom(run.Edge{Parent: "done"}))
	r.Feed("done", "output")
	r.Finish(0)
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	press(a, Char('A'))

	if a.IsDetached(a.slot.Seq) {
		t.Error("detached a run that had already finished")
	}
	if !strings.Contains(a.Status, "already finished") {
		t.Errorf("status = %q", a.Status)
	}
}

// Detaching is the last moment its output can be written down, because after it there is no
// end to wait for.
func TestDetachingArchivesWhatItHas(t *testing.T) {
	a := sample(t)
	r := run.Detached("forever", run.GraphFrom(run.Edge{Parent: "forever"}))
	r.Feed("forever", "something worth keeping")
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	press(a, Char('A'))

	if a.SavedTo == "" {
		t.Fatal("nothing was archived")
	}
	a.reloadHistory()
	if len(a.History) == 0 {
		t.Error("the archive has no record of it")
	}
}

// --- where a task is written -----------------------------------------------------------

func TestEOpensTheTasksOwnDefinition(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	// A real file, because the resolver checks — opening a path that is not there is how an
	// editor gets told to create it.
	root := t.TempDir()
	taskfile := filepath.Join(root, "Taskfile.yml")
	if err := os.WriteFile(taskfile, []byte("version: '3'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(pivot.Fixture([]string{"backend:lint"}), root)
	a.SetStateDir(t.TempDir())
	a.Details = map[string]task.Detail{
		"backend:lint": {Where: task.Where{File: taskfile, Line: 42}},
	}
	parkOn(t, a, "backend:lint")
	press(a, Char('e'))

	editor, ok := a.TakeEdit()
	if !ok {
		t.Fatalf("no editor — status %q", a.Status)
	}
	if editor.Args[0] != "+42" {
		t.Errorf("args = %v", editor.Args)
	}
	if editor.Args[1] != taskfile {
		t.Errorf("opened %s", editor.Args[1])
	}
}

// The listing arrives on a background goroutine. "Not yet" and "never" are different
// answers and the key should say which.
func TestEBeforeTheListingArrivesSaysToTryAgain(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	a := sample(t)
	parkOn(t, a, "backend:lint")
	press(a, Char('e'))

	if _, ok := a.TakeEdit(); ok {
		t.Error("opened something with no listing")
	}
	if !strings.Contains(a.Status, "still reading") {
		t.Errorf("status = %q", a.Status)
	}
}

func TestEWhenTheListingHasNoLocationForIt(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	a := sample(t)
	a.Details = map[string]task.Detail{"something:else": {}}
	parkOn(t, a, "backend:lint")
	press(a, Char('e'))

	if !strings.Contains(a.Status, "did not say where") {
		t.Errorf("status = %q", a.Status)
	}
}

func TestUpToDateComesFromTheListing(t *testing.T) {
	a := sample(t)
	a.Details = map[string]task.Detail{"backend:lint": {UpToDate: true}}
	if !a.UpToDate("backend:lint") {
		t.Error("should be up to date")
	}
	if a.UpToDate("app:lint") {
		t.Error("a task the listing says nothing about is not up to date")
	}
}

// --- re-running what failed ------------------------------------------------------------

// broken is a run of `all` where two of its three children failed.
func broken(t *testing.T, a *App) {
	t.Helper()
	r := run.Detached("all", run.GraphFrom(
		run.Edge{Parent: "all", Children: []string{"fmt", "lint", "test"}},
	))
	r.Feed("fmt", "formatted")
	r.Feed("lint", "boom")
	r.ApplyFailed("lint")
	r.Feed("test", "boom too")
	r.ApplyFailed("test")
	r.Finish(1)
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
}

// An aggregate is failed because its child was. Re-running the aggregate runs everything
// again, which is precisely what this key exists to avoid.
func TestTheFailuresAreTheTasksThatBrokeNotTheOnesBlamed(t *testing.T) {
	a := appWith(t, []string{"all", "fmt", "lint", "test"})
	broken(t, a)

	got := a.FailedTasks()
	want := map[string]bool{"lint": true, "test": true}
	if len(got) != 2 {
		t.Fatalf("got %v, want lint and test", got)
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("%q is not one of the tasks that broke", name)
		}
	}
}

func TestRerunFailedStartsEachInItsOwnSlot(t *testing.T) {
	a := appWith(t, []string{"all", "fmt", "lint", "test"})
	broken(t, a)
	press(a, Char('F'))

	roots := map[string]int{}
	for _, s := range a.Slots() {
		roots[s.Root]++
	}
	if roots["lint"] != 1 || roots["test"] != 1 {
		t.Errorf("slots hold %v, want one each of lint and test", roots)
	}
	if roots["fmt"] != 0 {
		t.Error("it restarted a task that passed")
	}
	// The original run keeps its slot; what must not happen is a *second* `all`, which is
	// the whole pipeline again and the thing this key exists to avoid.
	if roots["all"] != 1 {
		t.Errorf("`all` occupies %d slots, want just the run it came from", roots["all"])
	}
}

func TestRerunFailedOnAGreenRunSaysSo(t *testing.T) {
	a := appWith(t, []string{"all", "fmt"})
	r := run.Detached("all", run.GraphFrom(run.Edge{Parent: "all", Children: []string{"fmt"}}))
	r.Feed("fmt", "fine")
	r.Finish(0)
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	press(a, Char('F'))

	if !strings.Contains(a.Status, "nothing in") {
		t.Errorf("status = %q", a.Status)
	}
}

// A marked set is a set you chose; re-running what broke in a run is not that set, and
// spending it on the way was a surprise waiting for the next `⏎`.
func TestRerunningWhatFailedLeavesTheMarksAlone(t *testing.T) {
	a := appWith(t, []string{"all", "fmt", "lint", "test"})
	broken(t, a)
	a.marked = map[string]bool{"fmt": true}
	press(a, Char('F'))
	defer a.KillAll()

	if !a.IsMarked("fmt") {
		t.Error("re-running the failures cleared the marks")
	}
}

// --- production ---------------------------------------------------------------------

// dangerousCaller is `release`, which calls `deploy:prod` — the one on the danger list — as
// the coverage walk would report it once it has landed.
func dangerousCaller(t *testing.T) *App {
	t.Helper()
	a := appWith(t, []string{"build", "deploy:prod", "release"})
	for i := range a.Tasks {
		a.Tasks[i].Dangerous = a.Tasks[i].Name == "deploy:prod"
	}
	a.calls = run.GraphFrom(run.Edge{Parent: "release", Children: []string{"build", "deploy:prod"}})
	return a
}

// The danger list stopped at the task you pressed `⏎` on. `release` is not on it, and runs
// `deploy:prod`, which is.
func TestATaskThatCallsAProductionTaskAsksFirst(t *testing.T) {
	a := dangerousCaller(t)
	a.RequestRun("release", nil)

	c, ok := a.Confirm.(ConfirmRun)
	if !ok || c.Reason != CallsProduction || !reflect.DeepEqual(c.Calls, []string{"deploy:prod"}) {
		t.Fatalf("confirm = %+v", a.Confirm)
	}
	lines := a.RenderHeadless(100, 12)
	if footer := lines[len(lines)-1]; !strings.Contains(footer, "calls deploy:prod") {
		t.Errorf("the question does not say which: %q", footer)
	}
	if _, ok := a.productionQuestion(a.armed("build", nil)); ok {
		t.Error("`build` calls nothing on the list and should not be asked about")
	}
}

// Restarting asks whether to stop the run already going, and that yes is not a yes to
// production — the same second question a task on the list itself gets.
func TestRestartingATaskThatCallsProductionAsksBothQuestions(t *testing.T) {
	a := dangerousCaller(t)
	a.OpenRunForTest(run.Detached("release", run.GraphFrom(run.Edge{Parent: "release"})))
	a.Screen = ScreenRun
	a.RerunSelected()
	if c, ok := a.Confirm.(ConfirmRun); !ok || c.Reason != WouldStopRunning {
		t.Fatalf("confirm = %+v, want the restart question first", a.Confirm)
	}
	a.ConfirmYes()
	if c, ok := a.Confirm.(ConfirmRun); !ok || c.Reason != CallsProduction {
		t.Errorf("confirm = %+v, want the production question next", a.Confirm)
	}
}

// A batch is one question, and it names what in it reaches production, directly or not.
func TestAMarkedSetThatCallsProductionAsksOnce(t *testing.T) {
	a := dangerousCaller(t)
	a.marked = map[string]bool{"build": true, "release": true}
	a.RunMarked()
	if c, ok := a.Confirm.(ConfirmRunMarked); !ok || !reflect.DeepEqual(c.Dangerous, []string{"release"}) {
		t.Errorf("confirm = %+v", a.Confirm)
	}
}

// `F` restarts what broke, and what broke inside `release` was `deploy:prod`. It used to go
// straight out, one keypress from the run it failed in.
func TestRerunningAFailedProductionTaskAsksFirst(t *testing.T) {
	a := dangerousCaller(t)
	r := run.Detached("release", run.GraphFrom(run.Edge{Parent: "release", Children: []string{"build", "deploy:prod"}}))
	r.Feed("deploy:prod", "403 Forbidden")
	r.ApplyFailed("deploy:prod")
	r.Finish(1)
	a.OpenRunForTest(r)
	a.Screen = ScreenRun

	press(a, Char('F'))
	c, ok := a.Confirm.(ConfirmRerunFailed)
	if !ok || !reflect.DeepEqual(c.Dangerous, []string{"deploy:prod"}) {
		t.Fatalf("confirm = %+v", a.Confirm)
	}
	lines := a.RenderHeadless(100, 12)
	if footer := lines[len(lines)-1]; !strings.Contains(footer, "1 failed task") {
		t.Errorf("footer = %q", footer)
	}
	a.ConfirmNo()
	if a.Status != "not run" || len(a.Slots()) != 1 {
		t.Errorf("status = %q with %d slots; no should start nothing", a.Status, len(a.Slots()))
	}
}

// `F` in the picker arms --force. The two are different actions on different screens, which
// is what per-screen keymaps are for — but the run view must not have quietly inherited it.
func TestFInThePickerStillArmsForce(t *testing.T) {
	a := sample(t)
	press(a, Char('F'))
	if !a.ForceNext {
		t.Error("F in the picker should arm --force")
	}
}

// --- the bell ---------------------------------------------------------------------------

// ringing builds an app with one run that is about to finish somewhere you are not looking.
func ringing(t *testing.T, mode theme.BellMode, exit int) *App {
	t.Helper()
	a := sample(t)
	a.Bell = mode
	r := run.Detached("build", run.GraphFrom(run.Edge{Parent: "build"}))
	r.Feed("build", "working")
	a.OpenRunForTest(r)
	a.Screen = ScreenPicker // looking at something else
	a.noteFinished()        // not finished yet — nothing owed
	r.Finish(exit)
	return a
}

func TestARunThatFinishesWhileYouAreElsewhereRings(t *testing.T) {
	a := ringing(t, theme.BellUnwatched, 0)
	a.noteFinished()
	if !a.TakeBell() {
		t.Error("no bell for a run that finished off screen")
	}
}

// A run you watched finish needs no announcing — you watched it. The whole reason to want a
// bell is that you walked away from a long one.
func TestARunYouAreWatchingDoesNotRing(t *testing.T) {
	a := ringing(t, theme.BellUnwatched, 0)
	a.Screen = ScreenRun
	a.noteFinished()
	if a.TakeBell() {
		t.Error("it rang for a run that was on screen")
	}
}

// A finished run stays finished. Polling it forty times a second is not forty pieces of news.
func TestItRingsOnceNotOnEveryPoll(t *testing.T) {
	a := ringing(t, theme.BellUnwatched, 0)
	a.noteFinished()
	if !a.TakeBell() {
		t.Fatal("no first bell")
	}
	for range 5 {
		a.noteFinished()
	}
	if a.TakeBell() {
		t.Error("it rang again for the same run")
	}
}

func TestBellOffIsSilent(t *testing.T) {
	a := ringing(t, theme.BellNever, 1)
	a.noteFinished()
	if a.TakeBell() {
		t.Error("it rang with the bell off")
	}
}

func TestBellFailedOnlyRingsForFailures(t *testing.T) {
	green := ringing(t, theme.BellFailed, 0)
	green.noteFinished()
	if green.TakeBell() {
		t.Error("`failed` rang for a run that passed")
	}

	red := ringing(t, theme.BellFailed, 1)
	red.noteFinished()
	if !red.TakeBell() {
		t.Error("`failed` did not ring for a run that failed")
	}
}

func TestBellSettingParses(t *testing.T) {
	for _, c := range []struct {
		text string
		want theme.BellMode
	}{
		{"on", theme.BellUnwatched}, {"true", theme.BellUnwatched},
		{"off", theme.BellNever}, {"false", theme.BellNever}, {"never", theme.BellNever},
		{"failed", theme.BellFailed}, {" FAILED ", theme.BellFailed},
	} {
		got, ok := theme.ParseBell(c.text)
		if !ok || got != c.want {
			t.Errorf("%q parsed to %v (ok=%v), want %v", c.text, got, ok, c.want)
		}
	}
	if _, ok := theme.ParseBell("sometimes"); ok {
		t.Error("a value it does not understand should be reported, not guessed at")
	}
}

// A worktree is a different directory holding the same project, so the archive keyed by
// directory loses every task's history the day you branch. The middle rung is what gets it
// back, and it must not also drag in the repositories you were not asking about.
func TestTheRepoScopeReachesOtherWorktreesAndNothingElse(t *testing.T) {
	a := appWith(t, []string{"test"})
	a.repo, a.repoRead = "/main/.git", true

	here := store.Manifest{Dir: a.Root, Repo: "/main/.git"}
	worktree := store.Manifest{Dir: "/main/.worktrees/backend", Repo: "/main/.git"}
	elsewhere := store.Manifest{Dir: "/other", Repo: "/other/.git"}

	for _, c := range []struct {
		scope                          HistoryScope
		here, wantWorktree, wantOthers bool
	}{
		{ScopeProject, true, false, false},
		{ScopeRepo, true, true, false},
		{ScopeEverywhere, true, true, true},
	} {
		a.HistoryScope = c.scope
		if got := a.inHistoryScope(here); got != c.here {
			t.Errorf("%v: this directory = %v", c.scope, got)
		}
		// Everywhere is answered by reloadHistory rather than by the predicate, which only
		// has to agree with it on the two narrow rungs.
		if c.scope != ScopeEverywhere {
			if got := a.inHistoryScope(worktree); got != c.wantWorktree {
				t.Errorf("%v: a sibling worktree = %v", c.scope, got)
			}
			if got := a.inHistoryScope(elsewhere); got != c.wantOthers {
				t.Errorf("%v: another repository = %v", c.scope, got)
			}
		}
	}
}

// An old manifest has no repository recorded. Answering "not this repo" for a run made in
// this very directory would lose history the narrow scope always showed.
func TestARunWithNoRecordedRepoIsStillThisDirectorys(t *testing.T) {
	a := appWith(t, []string{"test"})
	a.repo, a.repoRead = "/main/.git", true
	a.HistoryScope = ScopeRepo
	if !a.inHistoryScope(store.Manifest{Dir: a.Root}) {
		t.Error("a run from before Repo existed dropped out of its own directory's history")
	}
}

// The rung in the middle is skipped where it would show the same list twice — outside a
// checkout, or in a clone with no other worktrees in the archive.
func TestWideningSkipsTheRepoRungWhenThereIsNothingThere(t *testing.T) {
	a := appWith(t, []string{"test"})
	a.repo, a.repoRead = "", true
	if got := a.nextHistoryScope(); got != ScopeEverywhere {
		t.Errorf("outside a checkout, widening went to %v", got)
	}
	a.HistoryScope = ScopeEverywhere
	if got := a.nextHistoryScope(); got != ScopeProject {
		t.Errorf("widening past everything went to %v, want back to this project", got)
	}
}

// Marks say "these belong together"; watch mode is the loop that keeps saying it. A `check`
// split across three tasks is the case watch mode was always one task short of.
func TestWatchingTheMarkedSet(t *testing.T) {
	a := sample(t)
	parkOn(t, a, "backend:lint")
	press(a, Char('m'))
	parkOn(t, a, "backend:build")
	press(a, Char('m'))

	press(a, Char('W'))
	if len(a.Watching) != 2 {
		t.Fatalf("watching %v, want both marked tasks", a.Watching)
	}
	if !strings.Contains(a.Status, "2 marked tasks") {
		t.Errorf("status = %q", a.Status)
	}
	// The marks stay: this arms a mode that keeps referring to them, where `⏎` spends them.
	if len(a.Marked()) != 2 {
		t.Error("arming watch consumed the marks")
	}
	// The header names what is being watched without spelling out a list that will not fit.
	if a.WatchLabel() != "2 tasks" {
		t.Errorf("label = %q", a.WatchLabel())
	}

	press(a, Char('W'))
	if len(a.Watching) != 0 {
		t.Errorf("watch did not turn off: %v", a.Watching)
	}
}

// With nothing marked it is still the one task it always was — the row under the cursor in
// the picker, which is where `⇧W` did not used to reach at all.
func TestWatchingOneTaskFromThePicker(t *testing.T) {
	a := sample(t)
	parkOn(t, a, "backend:lint")

	press(a, Char('W'))
	if len(a.Watching) != 1 || a.Watching[0] != "backend:lint" {
		t.Fatalf("watching %v, want the task under the cursor", a.Watching)
	}
	if a.WatchLabel() != "backend:lint" {
		t.Errorf("label = %q", a.WatchLabel())
	}
}

// Watch re-runs without asking, so arming it on a task that reaches the danger list would be
// a yes to production on every save. It refuses instead — for the task itself and for one
// that calls it.
func TestWatchWillNotArmOnAProductionTask(t *testing.T) {
	for _, name := range []string{"deploy:prod", "release"} {
		a := dangerousCaller(t)
		parkOn(t, a, name)
		press(a, Char('W'))
		if len(a.Watching) != 0 {
			t.Errorf("%s: watching %v, want nothing", name, a.Watching)
		}
		if !strings.Contains(a.Status, "touches production") {
			t.Errorf("%s: status = %q", name, a.Status)
		}
	}
}

// `.taskui-danger` says `deploy:*`. `deploy:apply` matches it but is `internal: true`, so
// `task --list-all` never lists it, a.Tasks never holds it, and isDangerous answers false
// for it — so `release`, which calls it, starts without a question.
func TestATaskCallingAnInternalProductionTaskAsks(t *testing.T) {
	dir := t.TempDir()
	taskfile := `version: '3'
tasks:
  release:
    desc: Cut a release
    cmds:
      - task: deploy:apply
  deploy:apply:
    internal: true
    cmds:
      - echo applying to production
`
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"), []byte(taskfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".taskui-danger"), []byte("deploy:*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, err := task.Discover(dir)
	if err != nil {
		t.Skip(err)
	}
	a := New(tasks, dir)
	a.SetStateDir(t.TempDir())
	a.StartCoverage()
	a.AwaitCoverage(30 * time.Second)
	t.Logf("release reaches %v", a.calls.Reachable("release"))

	a.RequestRun("release", nil)
	defer a.KillAll()
	if a.Confirm == nil {
		t.Errorf(
			"`release` calls deploy:apply (matches deploy:* in .taskui-danger) but started without asking; running = %q",
			rootOf(a),
		)
	}
}

// You are typing at `prompt` (input mode, `i`). Watch mode re-runs `test` on a save; start()
// claims a slot by parking the focused one and putting the new run on screen. SendingInput
// is an app-wide flag, not the slot's, so it stays on — and the next keystroke (a `y⏎`, a
// ^C) is written to `test`'s pty, not to the task you were answering.
func TestAWatchedRunStartingEndsTyping(t *testing.T) {
	dir := t.TempDir()
	taskfile := `version: '3'
tasks:
  prompt:
    cmds:
      - read -r x; echo "got $x"
  test:
    cmds:
      - sleep 5
`
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"), []byte(taskfile), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, err := task.Discover(dir)
	if err != nil {
		t.Skip(err)
	}
	a := New(tasks, dir)
	a.SetStateDir(t.TempDir())
	defer a.KillAll()

	a.Screen = ScreenRun
	if err := a.StartRunWith("prompt", nil); err != nil {
		t.Fatal(err)
	}
	typingAt := a.Run
	a.BeginInput()
	if !a.SendingInput {
		t.Fatal("input mode did not start")
	}

	w, err := watch.Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	w.Settle = 10 * time.Millisecond
	a.watcher = w
	a.Watching = []string{"test"}
	defer w.Close()

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !a.PollWatch() {
		if time.Now().After(deadline) {
			t.Fatal("watch never fired")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if a.SendingInput && a.Run != typingAt {
		t.Errorf("still in input mode, but the run on screen is now %q — the next key goes to it, not to %q",
			a.Run.Root, typingAt.Root)
	}
}
