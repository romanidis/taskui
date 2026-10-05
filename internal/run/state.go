package run

import (
	"slices"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (r *Run) apply(event Event) {
	switch e := event.(type) {
	case Redacting:
		r.RedactedSecrets = e.N

	case Naming:
		r.names = e.Names

	case GraphReady:
		g := e.Graph.Renamed(r.canonical)
		for _, name := range g.Reachable(r.Root) {
			if _, ok := r.Tasks[name]; !ok {
				r.Tasks[name] = newTaskRun()
			}
		}
		r.Graph = g

	case Partial:
		r.lastOutput = time.Now()
		name := r.canonical(e.Task)
		if name == "" {
			name = r.Root
			if r.hasActive {
				name = r.active
			}
		}
		r.touch(name)
		if r.provisional != nil && r.provisional.task == name {
			// Same unterminated line growing: replace it in place.
			if t, ok := r.Tasks[name]; ok && r.provisional.index < len(t.Lines) {
				t.Lines[r.provisional.index] = newLine(e.Text, false)
			}
			return
		}
		// A fragment for a different task than the one already holding one: the old guess is
		// never coming back, so it goes rather than being orphaned in place.
		r.dropProvisional()
		if index, ok := r.pushLine(name, newLine(e.Text, false)); ok {
			r.provisional = &provisionalLine{task: name, index: index}
		}

	case LineEvent:
		r.lastOutput = time.Now()
		// Untagged, with nothing running yet: go-task's own errors — a missing `requires:`
		// var, an unknown task, a malformed Taskfile — are printed before any task starts,
		// so they carry no `[name]` prefix and there is no active task to inherit.
		// Dropping them left the user with an empty tree and a bare exit code.
		name := r.canonical(e.Task)
		if name == "" {
			if r.hasActive {
				name = r.active
			} else {
				name = r.Root
			}
		}
		r.touch(name)
		// Before the provisional branch, which returns early: a line that supersedes an
		// unterminated one takes a different path to storage, and a check placed after that
		// branch reads only the lines that did not. Which is exactly what happened — the
		// first of two identical skips was explained and the second was not, purely by which
		// arrived whole.
		if task, why, ok := skipReason(ansi.Strip(e.Raw)); ok {
			r.apply(Skipping{Task: r.canonical(task), Why: why})
		}
		// A completed line supersedes the provisional one it grew out of.
		if r.provisional != nil {
			if r.provisional.task == name {
				at := r.provisional.index
				r.provisional = nil
				if t, ok := r.Tasks[name]; ok && at < len(t.Lines) {
					t.Lines[at] = newLine(e.Raw, e.IsCommand)
					return
				}
			} else {
				// The tag says the fragment was attributed to the wrong task. This line is
				// the whole of it, so the fragment is a duplicate and comes out.
				r.dropProvisional()
			}
		}
		r.pushLine(name, newLine(e.Raw, e.IsCommand))

	case FailedEvent:
		name := r.canonical(e.Task)
		r.touch(name)
		r.fail(name)

	case Skipping:
		// Deliberately does not touch(): a task go-task decided not to run has not started,
		// and opening it would put it in Order as though it had.
		if t, ok := r.Tasks[e.Task]; ok {
			t.Note = e.Why
		}

	case Exited:
		r.provisional = nil
		r.Exit = e.Code
		r.HasExit = true
		r.Duration = time.Since(r.Started)
		r.HasDuration = true
		r.settle(e.Code)
		r.drained = true
	}
}

// canonical is name as this run keys it: as the task list spells it, except that the root
// keeps the spelling it was invoked by. `taskui --run b` is a run of `b` from the header
// down, and the `[build]` its output is tagged with has to land on that same row.
func (r *Run) canonical(name string) string {
	if name == "" || r.names == nil {
		return name
	}
	c := r.names.Canonical(name)
	if c == r.names.Canonical(r.Root) {
		return r.Root
	}
	return c
}

// touch notes that name is producing output now, and closes out anything that was running
// and is not an ancestor of it.
func (r *Run) touch(name string) {
	if r.hasActive && r.active == name {
		return
	}
	r.adoptStray(name)

	ancestors := r.ancestorsOf(name)
	now := time.Now()
	for other, t := range r.Tasks {
		if t.Status != Running || other == name || ancestors[other] {
			continue
		}
		// Two tasks under one parent's `deps:` are started together, so the other one
		// speaking says nothing about this one. go-task interleaves their lines under
		// `--output prefixed`, and closing on the first line from a sibling marked half a
		// parallel build ✓ the moment the other half printed anything — with a duration
		// frozen at however long it had run so far, and no way back, since the reopen guard
		// below only reopens what is still Pending.
		if r.Graph.Concurrent(name, other) {
			continue
		}
		// A parent stays Running while its children work; a sibling that has stopped
		// producing output has finished.
		t.Status = Ok
		t.close(now)
	}

	// Open the whole chain, not just the task itself. An aggregate like `lint` whose
	// commands are all `task:` invocations never produces a line tagged with its own name —
	// every line belongs to a child — so it would otherwise look as though it never ran.
	// Ordered from the root down so Order reads top-down.
	chain := append(r.pathTo(name, ancestors), name)

	for _, task := range chain {
		// The graph is built from the invoked root, but go-task can report a task we never
		// saw — an alias, or a path `--summary` did not reveal.
		entry, ok := r.Tasks[task]
		if !ok {
			entry = newTaskRun()
			r.Tasks[task] = entry
		}
		if entry.started.IsZero() {
			entry.started = now
			r.Order = append(r.Order, task)
		}
		if entry.Status == Pending {
			entry.Status = Running
		}
	}
	r.active = name
	r.hasActive = true
}

// pathTo is the chain of tasks name is running under, root first.
//
// One chain, not every ancestor. A task two others share — `build` under both `lint` and
// `test` — has two above it, and opening both put `test` Running the moment `lint` started
// building; `lint`'s own output then closed it as finished, ✓ in microseconds, before it
// had run anything — and a run that failed before reaching it still called it a pass. The
// chain taken is the first in invocation order with nothing on it finished: the one go-task
// can still be inside. With none, every ancestor, as there is nothing better to go on.
func (r *Run) pathTo(name string, ancestors map[string]bool) []string {
	var path []string
	onPath := map[string]bool{}
	var walk func(node string) bool
	walk = func(node string) bool {
		if node == name {
			return true
		}
		if t := r.Tasks[node]; !ancestors[node] || onPath[node] || (t != nil && t.Status.Settled()) {
			return false
		}
		onPath[node] = true
		path = append(path, node)
		if slices.ContainsFunc(r.Graph.Edges[node], walk) {
			return true
		}
		path = path[:len(path)-1]
		delete(onPath, node)
		return false
	}
	if walk(r.Root) {
		return path
	}
	var all []string
	for _, t := range r.Graph.Reachable(r.Root) {
		if ancestors[t] {
			all = append(all, t)
		}
	}
	return all
}

// adoptStray puts a task that ran but that the graph never reached under the root.
//
// `--summary` refuses to describe an `internal: true` task, so a root whose deps hide behind
// one — `dev` → `dev:all` → {`dev:backend`, `site:dev`} — resolves to a graph that stops at
// the internal task, while the output plainly carries the tasks beyond it. Grafted rather
// than left floating, because everything reads the graph: the rows, what counts as an
// ancestor that stays open, and what may run alongside what. Left outside it, the root sat
// Pending under two live servers and each server's lines closed the other as finished.
//
// Hung off the root as its deps, which is to say concurrently with each other: what their
// real parent ran them as cannot be known from here, and closing a task that is still going
// is the worse mistake — a stray taken as concurrent is closed by the run ending instead.
//
// Only once a graph has arrived. Before that, or with none, every task would be a stray,
// and the run is in the flat mode that has no nesting to graft onto.
func (r *Run) adoptStray(name string) {
	if len(r.Graph.Edges) == 0 || name == r.Root {
		return
	}
	if _, known := r.Tasks[name]; known {
		return
	}
	if slices.Contains(r.Graph.Reachable(r.Root), name) {
		return
	}
	if r.Graph.Deps == nil {
		r.Graph.Deps = map[string][]string{}
	}
	r.Graph.Edges[r.Root] = append(r.Graph.Edges[r.Root], name)
	r.Graph.Deps[r.Root] = append(r.Graph.Deps[r.Root], name)
	if _, ok := r.Graph.Edges[name]; !ok {
		r.Graph.Edges[name] = nil
	}
}

func (r *Run) ancestorsOf(name string) map[string]bool {
	out := map[string]bool{}
	r.collectAncestors(name, out)
	return out
}

func (r *Run) collectAncestors(name string, out map[string]bool) {
	for _, parent := range r.Graph.Names() {
		for _, c := range r.Graph.Edges[parent] {
			if c != name {
				continue
			}
			if !out[parent] {
				out[parent] = true
				// Walk up. Depth here is tiny, so the repeated scan is fine.
				r.collectAncestors(parent, out)
			}
			break
		}
	}
}

func (r *Run) fail(name string) {
	now := time.Now()
	if t, ok := r.Tasks[name]; ok {
		t.Status = Failed
		t.close(now)
	}
	// A failing task fails everything that invoked it.
	for parent := range r.ancestorsOf(name) {
		if t, ok := r.Tasks[parent]; ok {
			t.Status = Failed
			t.close(now)
		}
	}
}

// settle resolves whatever is still Running once the process is gone.
func (r *Run) settle(exit int) {
	now := time.Now()
	for _, t := range r.Tasks {
		switch t.Status {
		case Running:
			// Only call it good if the run itself succeeded; on a failure with no named
			// culprit, an unfinished task is not a pass.
			if exit == 0 {
				t.Status = Ok
			} else {
				t.Status = Failed
			}
			t.close(now)
		case Pending:
			// Never produced a line and never opened as an ancestor: not reached.
			t.Status = Skipped
		default:
			// Already settled one way or the other.
		}
	}
}

type provisionalLine struct {
	task  string
	index int
}

// pushLine appends to a task's buffer, dropping the oldest block once it is full. It
// returns the index the line landed at.
//
// provisional holds an index into this same slice, so anything that shifts the slice has
// to shift that index with it. Without the fixup the next partial write edits whichever
// line has slid into that slot — output you are still reading, silently rewritten, with
// nothing on screen to say it happened.
func (r *Run) pushLine(task string, line Line) (int, bool) {
	t, ok := r.Tasks[task]
	if !ok {
		return 0, false
	}
	if len(t.Lines) >= MaxLines {
		t.Lines = append([]Line(nil), t.Lines[DropBlock:]...)
		t.Dropped += DropBlock
		if r.provisional != nil && r.provisional.task == task {
			r.provisional.index = max(0, r.provisional.index-DropBlock)
		}
	}
	t.Lines = append(t.Lines, line)
	return len(t.Lines) - 1, true
}

// dropProvisional discards an unterminated line that turned out to belong to somewhere
// else, taking it back out of the task it was guessed into.
//
// A Partial carries no `[name]`: the read boundary can fall anywhere in the stream, so the
// fragment is attributed to whatever spoke last, and on a run's very first read there is
// nothing to go on but the root. When the newline finally arrives carrying a different tag
// the guess is simply wrong, and what it leaves behind is a truncated copy of another
// task's line sitting in a task that never printed it — permanently, since the supersede
// path only ever matched a fragment against its own guess. PendingPrompt reads that stale
// fragment too, so one ending in `:` or `?` had the run view insisting a task was waiting
// for input for as long as the run lasted.
func (r *Run) dropProvisional() {
	p := r.provisional
	r.provisional = nil
	if p == nil {
		return
	}
	t, ok := r.Tasks[p.task]
	if !ok || p.index >= len(t.Lines) {
		return
	}
	t.Lines = append(t.Lines[:p.index], t.Lines[p.index+1:]...)
}
