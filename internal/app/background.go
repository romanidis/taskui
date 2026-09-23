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
	if a.detailCh != nil {
		return
	}
	a.enriching = true
	ch := make(chan map[string]task.Detail, 1)
	a.detailCh = ch
	root := a.Root
	go func() {
		details, err := task.Details(root)
		if err != nil {
			// A listing that cannot be had costs two conveniences and nothing else. Saying
			// so in the status bar would push a real message off it for a feature the user
			// has not asked for yet.
			close(ch)
			return
		}
		ch <- details
		close(ch)
	}()
}

// StartCoverage works out which aggregates reach which namespaces, in the background.
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
// `--summary` is a process spawn, an aggregate's graph is dozens of them, and a Taskfile
// with a dozen aggregates is dozens of dozens. Nothing on screen waits for it.
func (a *App) StartCoverage() {
	if a.reachCh != nil {
		return
	}
	a.covering = true
	ch := make(chan map[string][]string, 1)
	a.reachCh = ch
	root, tasks := a.Root, slices.Clone(a.Tasks)
	go func() {
		reach := func(name string) []string { return graph.Resolve(root, name).Reachable(name) }
		// No exemptions: `.taskui-cover` says which gaps are deliberate, and a gap is not
		// what this projection reads. A namespace is annotated with what reaches it.
		ch <- cover.BuildGrid(tasks, reach, nil).Reaches()
		close(ch)
	}()
}

// collectCoverage takes the answer if it has arrived. Non-blocking, like collectDetails: it
// is called from the poll loop, which must not wait for anything.
func (a *App) collectCoverage() bool {
	if a.reachCh == nil {
		return false
	}
	select {
	case reaches, ok := <-a.reachCh:
		a.reachCh = nil
		if !ok || len(reaches) == 0 {
			return false
		}
		a.Reaches = reaches
		return true
	default:
		return false
	}
}

// AwaitCoverage blocks until the walk lands or the grace runs out.
//
// The counterpart to AwaitDetails, and for the same reason: a screenshot is a picture of a
// loaded UI, and one taken mid-walk would show a different thing every time depending on how
// the race went.
func (a *App) AwaitCoverage(grace time.Duration) {
	if a.reachCh == nil {
		return
	}
	select {
	case reaches, ok := <-a.reachCh:
		a.reachCh = nil
		if ok {
			a.Reaches = reaches
		}
	case <-time.After(grace):
		// Leave the channel in place — the poll loop picks it up if this is not a one-frame
		// process after all.
	}
}

// collectDetails takes the JSON listing if it has arrived. Non-blocking: it is called from
// the poll loop, which must not wait for anything.
func (a *App) collectDetails() bool {
	if a.detailCh == nil {
		return false
	}
	select {
	case details, ok := <-a.detailCh:
		a.detailCh = nil
		if !ok || details == nil {
			return false
		}
		a.applyDetails(details)
		return true
	default:
		return false
	}
}

// AwaitDetails blocks until the listing lands or the grace runs out.
//
// For the paths that render one frame and exit. A screenshot is a picture of a loaded UI,
// and one taken before the listing arrived would show a different thing every time
// depending on how the race went — which is the opposite of what `--screenshot` is for.
func (a *App) AwaitDetails(grace time.Duration) {
	if a.detailCh == nil {
		return
	}
	select {
	case details, ok := <-a.detailCh:
		a.detailCh = nil
		if ok {
			a.applyDetails(details)
		}
	case <-time.After(grace):
		// Leave the channel in place — the poll loop will pick it up if this is not a
		// one-frame process after all.
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
