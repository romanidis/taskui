package app

import (
	"slices"

	"github.com/romanidis/taskui/internal/run"
)

// Fold is how much of a task's output is on screen.
//
// Three states rather than open-or-shut, because a run is two things at once: a shape you
// are scanning and a log you are reading. FoldPeek is the resting state — a task folded
// down to nothing is a task you have to open to discover whether it was worth opening, and
// on a 26-node run that is 26 guesses. The last few lines are almost always enough to
// answer it, because the interesting part of a command's output is the end.
type Fold int

const (
	// FoldPeek is a window on the last few lines, one line per row, cut off at the edge
	// rather than wrapped — a single 300-character line must not swallow the whole window.
	// It is the zero value, and therefore the default.
	FoldPeek Fold = iota
	// FoldFull is all of it, wrapped, which is the mode you read in.
	FoldFull
	// FoldHidden is one row: the task, how it went, how much it said. Nothing else.
	FoldHidden
)

// Next cycles hidden → peek → full → hidden.
//
// A cycle rather than a toggle plus a second key: the three states are one axis, and "how
// much of this do I want to see" is a question with an obvious "more" direction.
func (f Fold) Next() Fold {
	switch f {
	case FoldHidden:
		return FoldPeek
	case FoldPeek:
		return FoldFull
	default:
		return FoldHidden
	}
}

// RunRow is a row in the run view. Output lines are rows of the same list as the tasks
// that produced them, which is what makes one fold tree hold both.
type RunRow struct {
	// IsTask distinguishes a task header from one of its output lines.
	IsTask bool
	// Name is the task, for a task row.
	Name string
	// Task is the owning task, for a line row.
	Task  string
	Index int
	Depth int
	// Fold applies to task rows.
	Fold Fold
	// Peek marks a line that is part of a peek window rather than the full output: one
	// row, clipped.
	//
	// Carried on the row rather than looked up per frame because it decides this row's
	// height, and the height of every row is measured twice on every draw — once to choose
	// the column count and once to lay them out.
	Peek bool
}

// follow keeps the view pointed at the interesting thing: whatever is running, or — once
// something breaks — the task that broke.
func (a *App) follow() {
	if a.Run == nil {
		return
	}

	// A failure wins over following, and pins the view there.
	if name, ok := a.firstFailure(); ok {
		if a.focusedFailure != name {
			a.focusedFailure = name
			a.Following = false
			// Whatever was merely running is no longer the point.
			a.releaseFollowed(name)
			a.expandTo(name)
			a.RebuildRunRows()
			if !a.cursorInTask(name) {
				a.cursorToTask(name)
			}
		}
		return
	}

	if !a.Following {
		return
	}
	// Following moves the view, not the fold.
	//
	// It used to open the running task in full, from back when folded meant empty and a
	// peek did not exist. Now that every task rests on a window of its own last few lines,
	// expanding one of them undoes the thing the window is for: a single task with a
	// thousand lines of output becomes the whole screen, and the twenty-five peeks around
	// it — the point of watching a run rather than tailing a log — are pushed off it.
	// Peeks tail on their own, so the running task is already showing its latest lines
	// wherever it sits.
	for _, name := range slices.Backward(a.Run.Order) {
		t, ok := a.Run.Tasks[name]
		if !ok || t.Status != run.Running {
			continue
		}
		// Hand back anything an earlier follow did open, so a run that started before this
		// behaviour changed does not leave a full task behind it.
		a.releaseFollowed("")
		a.RebuildRunRows()
		// The cursor is already inside this task — reading it, almost certainly, since a
		// task whose lines you can see is one that is open. Following exists to bring what
		// is running into view, and it is in view: moving the cursor to the header now
		// would be dragging you off the line you were on, once per line that arrives.
		if a.cursorInTask(name) {
			return
		}
		a.cursorToTask(name)
		return
	}
}

// cursorInTask reports whether the run cursor is on a task or on one of its output lines.
func (a *App) cursorInTask(name string) bool {
	if a.RunCursor < 0 || a.RunCursor >= len(a.RunRows) {
		return false
	}
	row := a.RunRows[a.RunCursor]
	if row.IsTask {
		return row.Name == name
	}
	return row.Task == name
}

// releaseFollowed gives back the task following opened, unless it is keep.
//
// Only the one we opened, and only while it is still how we left it. A task the user
// opened is theirs; closing it because something else started running would be the tool
// arguing with the person using it.
func (a *App) releaseFollowed(keep string) {
	if a.followedOpen == "" || a.followedOpen == keep {
		return
	}
	previous := a.followedOpen
	if a.FoldOf(previous) == FoldFull {
		a.runFolds[previous] = FoldPeek
	}
	a.followedOpen = ""
}

// expandTo opens one task's output all the way, for following and for failures.
//
// Only that task. This used to walk up the graph opening every ancestor too, which was the
// only way to see anything back when folded meant empty — a parent showing nothing gave no
// clue that the thing you cared about was underneath it. Every task now peeks by default,
// so the chain already speaks for itself, and opening four ancestors in full to land on
// one leaf just buries the leaf again.
func (a *App) expandTo(name string) {
	a.runFolds[name] = FoldFull
}

func (a *App) cursorToTask(name string) {
	for i, r := range a.RunRows {
		if r.IsTask && r.Name == name {
			a.RunCursor = i
			return
		}
	}
}

type lineKey struct {
	task  string
	index int
}

// rowFilter is the run view's filter mode: the tasks and lines a search left standing.
// Nil means everything, which is what the picker's inline runs use — a search filters the
// run you are reading, not the list you are browsing.
type rowFilter struct {
	tasks map[string]bool
	lines map[lineKey]bool
}

// runRowsFor walks a run's execution graph into rows: every task, then whatever of its
// output the fold state calls for, then its children.
//
// A function over a run rather than a method on the app, because the picker draws runs the
// app is not focused on — every open slot at once — and a walk that reached for a.Run would
// draw the wrong one under every task but the last. The two views therefore cannot disagree
// about what a run contains; they differ in what surrounds the rows, not in the rows.
func runRowsFor(r *run.Run, foldOf func(string) Fold, peek int, filter *rowFilter) []RunRow {
	var rows []RunRow
	seen := map[string]bool{}
	type frame struct {
		name  string
		depth int
	}
	// Pushed in reverse so siblings come out in invocation order.
	stack := []frame{{r.Root, 0}}

	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// A diamond — `app:css` reached from both `check` and `build` — is shown at its
		// first position rather than duplicated.
		if seen[top.name] {
			continue
		}
		seen[top.name] = true

		children := r.Graph.Children(top.name)

		if filter != nil && !filter.tasks[top.name] {
			// Skip the row but keep walking: a parent with no hits of its own may still
			// contain a child that has them.
			for _, c := range slices.Backward(children) {
				stack = append(stack, frame{c, top.depth})
			}
			continue
		}

		// Filtering answers the question the fold state usually answers — you asked for
		// these lines by searching for them — so it opens everything it keeps.
		fold := foldOf(top.name)
		if filter != nil {
			fold = FoldFull
		}
		rows = append(rows, RunRow{IsTask: true, Name: top.name, Depth: top.depth, Fold: fold})

		if fold != FoldHidden {
			if t, ok := r.Tasks[top.name]; ok {
				// A peek is a window on the end of the buffer, not the start: a task that
				// is still going has its news at the bottom, and one that failed put the
				// reason there.
				from := 0
				if fold == FoldPeek {
					from = max(0, len(t.Lines)-peek)
				}
				for i := from; i < len(t.Lines); i++ {
					if filter != nil && !filter.lines[lineKey{top.name, i}] {
						continue
					}
					rows = append(rows, RunRow{
						Task:  top.name,
						Index: i,
						Depth: top.depth + 1,
						Peek:  fold == FoldPeek,
					})
				}
			}
		}
		for _, c := range slices.Backward(children) {
			stack = append(stack, frame{c, top.depth + 1})
		}
	}
	return rows
}

func (a *App) RebuildRunRows() {
	if a.Run == nil {
		a.RunRows = nil
		return
	}
	r := a.Run

	// What the cursor is on, so that rebuilding does not slide the view out from under it.
	// RunCursor is an index into a list that changes shape on every poll: a task higher up
	// the tree printing one more line shifts every row below it, and reading a stack trace
	// in the last task of a live run meant watching it creep past the cursor a line at a
	// time. The row is found again by identity below.
	anchored := false
	var anchorTask string
	anchorLine := -1
	anchorOffset := 0
	if a.RunCursor < len(a.RunRows) {
		row := a.RunRows[a.RunCursor]
		anchored = true
		if row.IsTask {
			anchorTask = row.Name
		} else {
			anchorTask = row.Task
			anchorLine = row.Index
			// How far below its own task's row this line sat. Kept as well as the line
			// index because a peek window slides as output arrives — index 40 drops out of
			// a five-line window that has moved on, while "the third row under this task"
			// still means something.
			for back := a.RunCursor - 1; back >= 0; back-- {
				if a.RunRows[back].IsTask {
					anchorOffset = a.RunCursor - back
					break
				}
			}
		}
	}

	// In filter mode the whole run collapses to matching lines under the tasks that
	// produced them: a hit is not useful if you cannot see which task said it.
	var filter *rowFilter
	if a.FilterMatches && a.Search != nil {
		// Each hit drags its neighbours in with it: `--- FAIL: TestOrderTotal` on its own
		// hides `order_test.go:88: want 1200, got 1180`, which is the useful half.
		filter = &rowFilter{lines: map[lineKey]bool{}, tasks: map[string]bool{}}
		for _, h := range a.SearchHits {
			n := 0
			if t, ok := r.Tasks[h.Task]; ok {
				n = len(t.Lines)
			}
			lo := max(0, h.Index-a.FilterContext)
			hi := min(h.Index+a.FilterContext+1, n)
			for i := lo; i < hi; i++ {
				filter.lines[lineKey{h.Task, i}] = true
				filter.tasks[h.Task] = true
			}
		}
	}

	a.RunRows = runRowsFor(r, a.FoldOf, a.PeekLines, filter)
	switch {
	case anchored:
		a.RunCursor = a.locate(anchorTask, anchorLine, anchorOffset)
	case a.RunCursor >= len(a.RunRows):
		a.RunCursor = max(0, len(a.RunRows)-1)
	}
}

// locate finds where the row the cursor was on has ended up.
//
// Exact line first; failing that, the task's own row plus however far down it the cursor
// sat. The fallback is what keeps a cursor inside a peek window from jumping to the task
// header every time the window slides — and it stops at this task's last line rather than
// walking into the next task's row.
func (a *App) locate(taskName string, line, offset int) int {
	last := max(0, len(a.RunRows)-1)
	if line >= 0 {
		for i, r := range a.RunRows {
			if !r.IsTask && r.Task == taskName && r.Index == line {
				return i
			}
		}
	}
	head := -1
	for i, r := range a.RunRows {
		if r.IsTask && r.Name == taskName {
			head = i
			break
		}
	}
	if head < 0 {
		// The task itself is gone — filtered out, or a different run is in the slot.
		return min(a.RunCursor, last)
	}
	own := 0
	for i := head + 1; i < len(a.RunRows); i++ {
		if a.RunRows[i].IsTask || a.RunRows[i].Task != taskName {
			break
		}
		own++
	}
	return min(head+min(offset, own), last)
}

func (a *App) RunMoveCursor(delta int) {
	if len(a.RunRows) == 0 {
		return
	}
	// Any deliberate move means the user is reading, not watching.
	a.Following = false
	a.RunCursor = clamp(a.RunCursor+delta, 0, len(a.RunRows)-1)
}

// FoldOf is how much of this task's output is showing.
func (a *App) FoldOf(name string) Fold {
	return a.runFolds[name]
}

// RunToggleFold is `o` on a task: hidden, peek, full, round again.
func (a *App) RunToggleFold() {
	if a.RunCursor >= len(a.RunRows) || !a.RunRows[a.RunCursor].IsTask {
		return
	}
	name := a.RunRows[a.RunCursor].Name
	a.Following = false
	// Touching a task's fold takes it over: following must not hand back something you
	// have since decided to keep open.
	if a.followedOpen == name {
		a.followedOpen = ""
	}
	a.runFolds[name] = a.FoldOf(name).Next()
	a.RebuildRunRows()
	a.cursorToTask(name)
}

// OpenRunForTest opens a ready-made run in a fresh slot, parking whatever was there. The
// real parking path, so a test that uses it is testing the same code a keypress would.
func (a *App) OpenRunForTest(r *run.Run) {
	a.parkFocused()
	a.nextSeq++
	a.slot = newSlot(r, a.nextSeq)
	a.RebuildRunRows()
	a.RebuildPickerRows()
}

// RunSetFold forces a task's fold state, for tests.
func (a *App) RunSetFold(name string, fold Fold) {
	a.runFolds[name] = fold
	a.RebuildRunRows()
}

// RunIsExpanded reports whether a task is showing all of its output.
func (a *App) RunIsExpanded(name string) bool { return a.FoldOf(name) == FoldFull }

// RunExpand opens a task all the way.
func (a *App) RunExpand(name string) { a.RunSetFold(name, FoldFull) }

// Follow is the test-visible entry point to the follow logic.
func (a *App) Follow() { a.follow() }

// RunToggleFoldAll is `⇧O` in the run view: move every task to the same state at once.
//
// Opening everything is how you read a run end to end; closing it is how you get back to
// the shape of what ran once you have. Mixed states go to full first, because mixed almost
// always means "I opened two of these and now want the rest" — and from there it is the
// same cycle a single `o` walks. On a long run this moves the cursor a very long way, so
// it stays pinned to the task you were on.
func (a *App) RunToggleFoldAll() {
	if a.Run == nil {
		return
	}
	here, hadHere := a.RunSelectedTask()
	names := a.Run.TaskNames()
	every := func(f Fold) bool {
		for _, k := range names {
			if a.FoldOf(k) != f {
				return false
			}
		}
		return true
	}
	next := FoldFull
	switch {
	case every(FoldFull):
		next = FoldHidden
	case every(FoldHidden):
		next = FoldPeek
	}
	a.runFolds = map[string]Fold{}
	for _, k := range names {
		a.runFolds[k] = next
	}
	a.followedOpen = ""
	a.Following = false
	a.RebuildRunRows()
	if hadHere {
		a.cursorToTask(here)
	}
}
