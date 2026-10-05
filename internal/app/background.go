package app

import (
	"slices"
	"time"

	"github.com/romanidis/taskui/internal/cover"
	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/task"
)

// StartEnrichment fetches the JSON listing in the background.
//
// Opt-in rather than automatic in New: it shells out to `task`, and the several hundred
// tests that build an App from a fixture have no Taskfile to shell out to and no interest
// in one.
func (a *App) StartEnrichment() {
	if a.details.running() {
		return
	}
	a.enriching = true
	root := a.Root
	a.details = begin(func() (map[string]task.Detail, bool) {
		details, err := task.Details(root)
		// A listing that cannot be had costs two conveniences and nothing else. Saying so in
		// the status bar would push a real message off it for a feature the user has not
		// asked for yet.
		return details, err == nil
	})
}

// StartCoverage works out which aggregates reach which namespaces, and what every task
// calls, in the background.
//
// The question the domain tree could not answer: standing in `backend`, is there something
// above that runs it? A root `fmt` that gathers every namespace's `fmt` is the whole reason
// the tree has a top level, and until now the only way to see the relationship was to pivot
// to `verb` and look at the fan-out, or to leave the program and run `--lint`.
//
// It is a graph walk and not a name match, for the reason `internal/cover` exists: xerum's
// `lint` never calls `api:lint`, it calls `api:check`, which reaches `api:tenant:lint` two
// levels down. Names would call that a miss.
//
// Opt-in for the same reason as StartEnrichment — it shells out, and the tests that build an
// App from a fixture have no Taskfile to shell out to. The cost is why it is a goroutine: a
// `--summary` is a process spawn, and the walk takes one for every task in the Taskfile.
// Nothing on screen waits for it.
//
// Every task rather than only the aggregates, because the graph it resolves is also what the
// danger check reads: `release` calling `deploy:prod` is a production run whether or not
// `release` is anything's aggregate. Resolving them together costs one `--summary` a task,
// where walking each aggregate on its own summarised what they share again for each.
func (a *App) StartCoverage() {
	if a.reaches.running() {
		return
	}
	a.covering = true
	root, tasks := a.Root, slices.Clone(a.Tasks)
	a.reaches = begin(func() (covered, bool) {
		names := make([]string, len(tasks))
		for i, t := range tasks {
			names[i] = t.Name
		}
		calls := graph.ResolveProject(root, names)
		// No exemptions: `.taskui-cover` says which gaps are deliberate, and a gap is not
		// what this projection reads. A namespace is annotated with what reaches it.
		return covered{reaches: cover.BuildGrid(tasks, calls.Reachable, nil).Reaches(), calls: calls}, true
	})
}

// covered is what the coverage walk finds: the aggregates that reach each namespace, and
// what every task calls, which it had to resolve to find them.
type covered struct {
	reaches map[string][]string
	calls   graph.Graph
}

// collectCoverage takes the answer if it has arrived. Non-blocking, like collectDetails: it
// is called from the poll loop, which must not wait for anything.
func (a *App) collectCoverage() bool {
	found, ok := a.reaches.take()
	if !ok {
		return false
	}
	a.calls = found.calls
	if len(found.reaches) == 0 {
		return false
	}
	a.Reaches = found.reaches
	return true
}

// AwaitCoverage blocks until the walk lands or the grace runs out.
//
// The counterpart to AwaitDetails, and for the same reason: a screenshot is a picture of a
// loaded UI, and one taken mid-walk would show a different thing every time depending on how
// the race went.
func (a *App) AwaitCoverage(grace time.Duration) {
	if found, ok := a.reaches.await(grace); ok {
		a.Reaches, a.calls = found.reaches, found.calls
	}
}

// collectDetails takes the JSON listing if it has arrived. Non-blocking: it is called from
// the poll loop, which must not wait for anything.
func (a *App) collectDetails() bool {
	details, ok := a.details.take()
	if !ok || details == nil {
		return false
	}
	a.applyDetails(details)
	return true
}

// AwaitDetails blocks until the listing lands or the grace runs out.
//
// For the paths that render one frame and exit. A screenshot is a picture of a loaded UI,
// and one taken before the listing arrived would show a different thing every time
// depending on how the race went — which is the opposite of what `--screenshot` is for.
func (a *App) AwaitDetails(grace time.Duration) {
	if details, ok := a.details.await(grace); ok {
		a.applyDetails(details)
	}
}

// applyDetails takes the listing, wherever it was collected from.
//
// Both collection paths land here, because both have to do the same two things afterwards
// and one of them originally did neither: the locations go onto the tasks themselves — a
// pivot is a function of a task, and the file pivot cannot be one if the file lives in a map
// beside it — and the tree is rebuilt, since it may have been built before any of this was
// known and the file pivot would have listed everything ungrouped and stayed that way.
func (a *App) applyDetails(details map[string]task.Detail) {
	if details == nil {
		return
	}
	a.Details = details
	for i := range a.Tasks {
		if d, ok := details[a.Tasks[i].Name]; ok {
			a.Tasks[i] = a.Tasks[i].With(d)
		}
	}
	// The listing is the only thing that knows where the tasks are actually written, so it
	// is also the moment an `includes:` file becomes watchable.
	a.rewatchTaskfile()
	a.Rebuild(a.SelectedTask())
}

// WhereIs is where a task is written, once the listing has arrived.
func (a *App) WhereIs(name string) (task.Where, bool) {
	d, ok := a.Details[name]
	if !ok || !d.Where.Ok() {
		return task.Where{}, false
	}
	return d.Where, true
}

// UpToDate is go-task's own answer to "would running this do anything".
//
// Only `sources:`/`generates:` — a task gated by `status:` reports false here and still
// skips, which is go-task's answer and not one to improve on locally.
func (a *App) UpToDate(name string) bool {
	d, ok := a.Details[name]
	return ok && d.UpToDate
}

// pending is a result being worked out on another goroutine, to be taken once it lands.
//
// Three background jobs have this shape — the JSON listing, the coverage walk and a
// Taskfile re-read — and each carried its own copy of the take that must not wait and the
// take that waits out a grace. The zero value has nothing pending.
type pending[T any] struct{ ch chan T }

// begin runs work on a goroutine of its own. A work that reports false has no answer to
// give, and taking it then reports nothing.
func begin[T any](work func() (T, bool)) pending[T] {
	ch := make(chan T, 1)
	go func() {
		if v, ok := work(); ok {
			ch <- v
		}
		close(ch)
	}()
	return pending[T]{ch: ch}
}

// running reports whether there is a result still to take.
func (p pending[T]) running() bool { return p.ch != nil }

// take is the result if it has landed, and never waits: it is called from the poll loop,
// which must not wait for anything. Once it has landed nothing is pending, answer or not.
func (p *pending[T]) take() (T, bool) {
	var zero T
	if p.ch == nil {
		return zero, false
	}
	select {
	case v, ok := <-p.ch:
		p.ch = nil
		return v, ok
	default:
		return zero, false
	}
}

// await is take, waiting up to grace for the result — for the paths that draw one frame and
// exit. Past the grace it is left pending, for the poll loop to take if the process turns
// out not to be a one-frame one after all.
func (p *pending[T]) await(grace time.Duration) (T, bool) {
	var zero T
	if p.ch == nil {
		return zero, false
	}
	select {
	case v, ok := <-p.ch:
		p.ch = nil
		return v, ok
	case <-time.After(grace):
		return zero, false
	}
}
