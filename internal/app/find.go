package app

import (
	"slices"

	"github.com/sahilm/fuzzy"
)

func (a *App) BeginJump() {
	a.Jumping = true
	a.JumpQuery = ""
	a.JumpMatches = nil
	a.JumpIdx = 0
	a.jumpOrigin = a.Cursor
	a.jumpNode = -1
	if a.Cursor >= 0 && a.Cursor < len(a.PickerRows) {
		if tree := a.PickerRows[a.Cursor].Tree; tree >= 0 && tree < len(a.Rows) {
			a.jumpNode = a.Rows[tree].Node
		}
	}
	a.Status = ""
}

// backToJumpOrigin puts the cursor on the row the jump started from.
func (a *App) backToJumpOrigin() {
	if a.jumpNode >= 0 {
		for i, row := range a.Rows {
			if row.Node == a.jumpNode {
				a.Cursor = a.pickerIndexOfTree(i)
				return
			}
		}
	}
	a.Cursor = min(a.jumpOrigin, max(0, len(a.PickerRows)-1))
}

func (a *App) PushJump(c rune) {
	a.JumpQuery += string(c)
	a.applyJump()
}

func (a *App) PopJump() {
	a.JumpQuery = withoutLastRune(a.JumpQuery)
	a.applyJump()
}

// AcceptJump keeps the cursor where the jump left it.
func (a *App) AcceptJump() {
	a.Jumping = false
	a.JumpQuery = ""
}

// CancelJump puts the cursor back where it started.
func (a *App) CancelJump() {
	a.Jumping = false
	a.JumpQuery = ""
	a.JumpMatches = nil
	a.backToJumpOrigin()
}

func (a *App) JumpStep(delta int) {
	if len(a.JumpMatches) == 0 {
		return
	}
	n := len(a.JumpMatches)
	a.JumpIdx = ((a.JumpIdx+delta)%n + n) % n
	a.gotoMatch()
}

func (a *App) applyJump() {
	if a.JumpQuery == "" {
		a.JumpMatches = nil
		a.backToJumpOrigin()
		return
	}
	a.JumpMatches = a.matchingTasks(a.JumpQuery)
	// Land first on the task the query names outright, by its name or an alias, and otherwise
	// on the first in tree order. `build` beside `app:build` landed on `app:build`, which sorts
	// first — and the Neovim plugin's `:TaskUI run build` is a jump and a ⏎, so it ran the
	// wrong task.
	a.JumpIdx = 0
	for i, ti := range a.JumpMatches {
		if t := a.Tasks[ti]; t.Name == a.JumpQuery || slices.Contains(t.Aliases, a.JumpQuery) {
			a.JumpIdx = i
			break
		}
	}
	a.gotoMatch()
}

// gotoMatch moves to the current match, opening whatever folds hide it. The tree itself is
// left alone — that is the whole difference from the filter.
func (a *App) gotoMatch() {
	if a.JumpIdx >= len(a.JumpMatches) {
		return
	}
	a.Rebuild(a.JumpMatches[a.JumpIdx])
}

// matchingTasks lists tasks matching query, fuzzily, over the full colon path and the
// aliases — so `blint` finds `backend:lint` and `t` finds the `test` alias.
//
// Shared by the filter and the jump so the two can never disagree about what counts as a
// match. Results keep tree order rather than score order — the tree is the organisation,
// and resorting it by score would destroy the grouping the user is looking at.
func (a *App) matchingTasks(query string) []int {
	haystack := make([]string, 0, len(a.Tasks))
	owner := make([]int, 0, len(a.Tasks))
	for i, t := range a.Tasks {
		haystack = append(haystack, t.Name)
		owner = append(owner, i)
		for _, alias := range t.Aliases {
			haystack = append(haystack, alias)
			owner = append(owner, i)
		}
	}

	// Smart case, as nucleo and fzf do it: a lowercase query matches loosely, and any
	// uppercase in the query makes the whole thing case-sensitive. The library only offers
	// the case-insensitive half, so the exact pass is layered on top of it.
	exact := hasUpper(query)
	matched := map[int]bool{}
	for _, m := range fuzzy.FindNoSort(query, haystack) {
		if exact && !subsequence(query, haystack[m.Index]) {
			continue
		}
		matched[owner[m.Index]] = true
	}

	var out []int
	for i := range a.Tasks {
		if matched[i] {
			out = append(out, i)
		}
	}
	return out
}

func hasUpper(s string) bool {
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			return true
		}
	}
	return false
}

// subsequence is the case-sensitive half of smart case.
func subsequence(pattern, target string) bool {
	p := []rune(pattern)
	if len(p) == 0 {
		return true
	}
	at := 0
	for _, c := range target {
		if c == p[at] {
			at++
			if at == len(p) {
				return true
			}
		}
	}
	return false
}

func (a *App) handleJumpKey(k Key) {
	switch {
	case k.kind == keyEsc:
		a.CancelJump()
	case k.kind == keyEnter:
		a.AcceptJump()
	case k.kind == keyBackspace:
		a.PopJump()
	case k.kind == keyDown, k.kind == keyTab:
		a.JumpStep(1)
	case k.kind == keyUp, k.kind == keyBackTab:
		a.JumpStep(-1)
	case k.typed():
		a.PushJump(k.ch)
	}
}
