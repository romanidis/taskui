package app

import (
	"fmt"

	"github.com/romanidis/taskui/internal/keys"
	"github.com/romanidis/taskui/internal/search"
	"github.com/romanidis/taskui/internal/store"
)

// HistoryScope is how much of the archive the history list shows.
//
// Three rungs rather than the on/off it started as, because there is a real answer between
// them. The archive is keyed by the directory a run happened in; a git worktree is a
// different directory holding the same project, so the day you branch, every task's history
// starts again from nothing — while "all projects" swings too far the other way and mixes
// in the repositories you were not asking about.
type HistoryScope int

const (
	// ScopeProject is the directory taskui was opened in, and the default.
	ScopeProject HistoryScope = iota
	// ScopeRepo is every checkout of this repository — the worktrees, and the main one.
	ScopeRepo
	// ScopeEverywhere is every project in the archive.
	ScopeEverywhere
)

func (s HistoryScope) String() string {
	switch s {
	case ScopeRepo:
		return "this repo"
	case ScopeEverywhere:
		return "all projects"
	default:
		return "this project"
	}
}

// OpenLastRun opens the most recent stored run for this project, as `--last` does.
func (a *App) OpenLastRun() bool {
	here := a.Root
	for _, m := range a.archive.List() {
		if !store.SameDir(m.Dir, here) {
			continue
		}
		a.History = []store.Manifest{m}
		a.HistoryCursor = 0
		a.OpenStoredRun()
		return true
	}
	return false
}

// OpenHistory loads the archive and switches to it.
func (a *App) OpenHistory() {
	// A kept search still narrows the list. Reloaded bare, every run came back under a
	// header still reading `/FAIL`, and ⏎ opened runs the search never matched filtered to it.
	a.applyHistorySearch()
	a.HistoryCursor = 0
	a.HistoryOffset = 0
	a.Screen = ScreenHistory
	if len(a.History) == 0 {
		a.Status = "no stored runs yet — run something and it will land here"
	} else {
		a.Status = ""
	}
}

func (a *App) reloadHistory() {
	all := a.archive.List()
	if a.HistoryScope == ScopeEverywhere {
		a.History = all
		return
	}
	a.History = nil
	for _, m := range all {
		if a.inHistoryScope(m) {
			a.History = append(a.History, m)
		}
	}
}

// inHistoryScope is whether a stored run belongs in the list at the current scope.
//
// The repository rung falls back to the directory when either side has no repo recorded:
// manifests written before `Repo` existed have none, and answering "not the same repo" for
// a run made in this very directory would lose history the narrow scope always showed.
func (a *App) inHistoryScope(m store.Manifest) bool {
	if store.SameDir(m.Dir, a.Root) {
		return true
	}
	return a.HistoryScope == ScopeRepo && a.repoDir() != "" && m.Repo == a.repoDir()
}

func (a *App) repoDir() string {
	if !a.repoRead {
		a.repoRead = true
		a.repo = store.RepoOf(a.Root)
	}
	return a.repo
}

func (a *App) BeginHistorySearch() {
	a.HistorySearching = true
	a.HistoryQuery = ""
	a.HistoryHits = map[string]int{}
	a.Status = ""
	a.reloadHistory()
}

func (a *App) ClearHistorySearch() {
	a.HistorySearching = false
	a.HistoryQuery = ""
	a.HistoryHits = map[string]int{}
	a.reloadHistory()
	a.HistoryCursor = 0
}

// applyHistorySearch greps the archive and keeps only the runs that matched.
//
// This is the "when did this start failing" question — the one thing you could not ask
// before runs were stored, and the reason the archive exists at all.
func (a *App) applyHistorySearch() {
	a.HistoryHits = map[string]int{}
	a.reloadHistory()
	if a.HistoryQuery == "" {
		a.HistoryCursor = 0
		return
	}
	query, err := search.NewQuery(a.HistoryQuery)
	if err != nil {
		// Half-typed regex: leave the list alone rather than emptying it.
		return
	}
	results, _ := search.InStore(a.archive, query, 200, search.Scope{})
	for _, r := range results {
		a.HistoryHits[r.Manifest.ID] = len(r.Hits)
	}
	kept := a.History[:0]
	for _, m := range a.History {
		if _, ok := a.HistoryHits[m.ID]; ok {
			kept = append(kept, m)
		}
	}
	a.History = kept
	a.HistoryCursor = 0
}

// ToggleHistoryScope widens the list a rung, and wraps back to this project from the top.
//
// The repository rung is skipped where it would be the same list twice: outside a checkout
// there is no repository to widen to, and in an ordinary clone with no worktrees it holds
// exactly what this project already holds.
func (a *App) ToggleHistoryScope() {
	a.HistoryScope = a.nextHistoryScope()
	keep := ""
	if a.HistoryCursor < len(a.History) {
		keep = a.History[a.HistoryCursor].ID
	}
	a.applyHistorySearch()
	// Stay on the same run across the widening, as the pivot does in the picker.
	a.HistoryCursor = 0
	for i, m := range a.History {
		if m.ID == keep {
			a.HistoryCursor = i
			break
		}
	}
	a.Status = ""
}

// nextHistoryScope is the rung ToggleHistoryScope widens to.
func (a *App) nextHistoryScope() HistoryScope {
	switch a.HistoryScope {
	case ScopeProject:
		// The repository rung only shows something new when the archive holds runs from this
		// repository made somewhere other than here: another worktree.
		if repo := a.repoDir(); repo != "" {
			for _, m := range a.archive.List() {
				if m.Repo == repo && !store.SameDir(m.Dir, a.Root) {
					return ScopeRepo
				}
			}
		}
		return ScopeEverywhere
	case ScopeRepo:
		return ScopeEverywhere
	default:
		return ScopeProject
	}
}

func (a *App) SetFilterContext(delta int) {
	next := clamp(a.FilterContext+delta, 0, 20)
	if next == a.FilterContext {
		return
	}
	a.FilterContext = next
	if a.FilterMatches {
		a.RebuildRunRows()
		a.jumpToHit()
	}
	a.Status = fmt.Sprintf("%d lines of context", a.FilterContext)
}

func (a *App) HistoryMoveCursor(delta int) {
	if len(a.History) == 0 {
		return
	}
	a.HistoryCursor = clamp(a.HistoryCursor+delta, 0, len(a.History)-1)
}

// OpenStoredRun reopens the run under the cursor. It lands in the same run view a live run
// uses — same folding, same search — because it is the same structure, just read off disk.
func (a *App) OpenStoredRun() {
	if a.HistoryCursor >= len(a.History) {
		return
	}
	manifest := a.History[a.HistoryCursor]
	r, err := a.archive.Load(manifest)
	if err != nil {
		a.Status = fmt.Sprintf("could not read that run: %v", err)
		return
	}
	seq, why := a.claimStoredSlot()
	if why != "" {
		a.Status = why
		return
	}
	a.retire(a.Run)
	a.slot = newSlot(r, seq)
	a.Following = false
	a.SavedTo = a.archive.RunDir(manifest.ID)
	a.Screen = ScreenRun
	a.ClearSearch()
	a.Status = ""
	// Open the failure straight away: reopening a run is nearly always about the thing
	// that broke.
	if failed := a.FailedTasks(); len(failed) > 0 {
		a.runFolds[failed[0]] = FoldFull
		a.RebuildRunRows()
		a.cursorToTask(failed[0])
	} else {
		a.RebuildRunRows()
	}

	// Arriving from a cross-run search: land with the same query applied, so the run opens
	// on the thing you were looking for rather than making you retype it. Only for a run the
	// search found — the timeline opens runs through here too, and one it never matched
	// opened filtered down to nothing.
	if a.HistoryQuery != "" && a.HistoryHits[manifest.ID] > 0 {
		a.SearchInput = a.HistoryQuery
		a.FilterMatches = true
		a.ApplySearch()
	}
}

func (a *App) handleHistoryKey(k Key) bool {
	if a.HistorySearching {
		switch {
		case k.kind == keyEsc:
			a.ClearHistorySearch()
		case k.kind == keyEnter:
			// Keep the query; it carries into the run you open.
			a.HistorySearching = false
		case k.kind == keyBackspace:
			a.HistoryQuery = withoutLastRune(a.HistoryQuery)
			a.applyHistorySearch()
		case k.kind == keyDown:
			a.HistoryMoveCursor(1)
		case k.kind == keyUp:
			a.HistoryMoveCursor(-1)
		case k.typed():
			a.HistoryQuery += string(k.ch)
			a.applyHistorySearch()
		}
		return false
	}

	act := func() keys.Action { return a.action(k, ScreenHistory) }

	switch {
	case k.kind == keyEsc:
		a.Screen = ScreenPicker
		a.Status = ""

	// Widen to every project, or narrow back to this one.
	case act() == keys.AllProjects:
		a.ToggleHistoryScope()

	case act() == keys.Search:
		a.BeginHistorySearch()

	case k.kind == keyEnter:
		a.OpenStoredRun()
	}
	return false
}
