package app

import (
	"strings"

	"github.com/romanidis/taskui/internal/search"
)

// refreshSearch re-runs the current query against the buffers, which have grown since last
// time.
func (a *App) refreshSearch() {
	if a.Search == nil || a.Run == nil {
		return
	}
	a.SearchHits = search.InRun(a.Run, a.Search)
	if a.SearchIdx >= len(a.SearchHits) {
		a.SearchIdx = max(0, len(a.SearchHits)-1)
	}
}

// ApplySearch compiles what has been typed and jumps to the first hit.
func (a *App) ApplySearch() {
	if a.SearchInput == "" {
		// Keep the prompt and its mode; only the compiled query goes away, so backspacing
		// to empty shows the whole run again rather than dropping you out.
		wasFiltering := a.FilterMatches
		stillTyping := a.Searching
		a.ClearSearch()
		a.Searching = stillTyping
		a.FilterMatches = wasFiltering && stillTyping
		a.RebuildRunRows()
		return
	}
	q, err := search.NewQuery(a.SearchInput)
	if err != nil {
		// A half-typed regex is the normal state during incremental search, so this is
		// reported quietly rather than treated as a failure.
		a.SearchError = firstLine(err.Error())
		a.Search = nil
		a.SearchHits = nil
		return
	}
	a.SearchError = ""
	a.Search = q
	a.SearchIdx = 0
	a.Following = false
	a.refreshSearch()
	a.RebuildRunRows()
	a.jumpToHit()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "bad pattern"
	}
	return s
}

func (a *App) ClearSearch() {
	a.Search = nil
	a.Searching = false
	a.SearchInput = ""
	a.SearchHits = nil
	a.SearchIdx = 0
	a.SearchError = ""
	a.FilterMatches = false
	a.RebuildRunRows()
}

func (a *App) SearchStep(delta int) {
	if len(a.SearchHits) == 0 {
		return
	}
	n := len(a.SearchHits)
	a.SearchIdx = ((a.SearchIdx+delta)%n + n) % n
	a.jumpToHit()
}

// jumpToHit puts the cursor on the current hit, opening whatever fold hides it.
func (a *App) jumpToHit() {
	if a.SearchIdx >= len(a.SearchHits) {
		return
	}
	hit := a.SearchHits[a.SearchIdx]
	a.expandTo(hit.Task)
	a.RebuildRunRows()
	for i, r := range a.RunRows {
		if !r.IsTask && r.Task == hit.Task && r.Index == hit.Index {
			a.RunCursor = i
			return
		}
	}
}

// ToggleFilterMatches turns the filtered view on and off.
//
// `f` with a query already running toggles it; `f` with nothing running opens the prompt
// already filtering, so typing narrows the run live instead of making you search first and
// convert afterwards.
func (a *App) ToggleFilterMatches() {
	if a.Search == nil {
		a.BeginFilter()
		return
	}
	a.FilterMatches = !a.FilterMatches
	a.Following = false
	a.RebuildRunRows()
	a.jumpToHit()
}

// BeginFilter opens the prompt in filter mode.
func (a *App) BeginFilter() {
	a.Searching = true
	a.FilterMatches = true
	a.SearchInput = ""
	a.SearchError = ""
	a.Status = ""
}

func (a *App) PushSearch(c rune) {
	a.SearchInput += string(c)
	a.ApplySearch()
}

func (a *App) PopSearch() {
	a.SearchInput = withoutLastRune(a.SearchInput)
	a.ApplySearch()
}
