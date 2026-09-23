package app

import (
	"fmt"

	"github.com/romanidis/taskui/internal/keys"
)

func (a *App) historyHeader() line {
	t := a.Theme
	failed := 0
	for _, m := range a.History {
		if m.Failed() {
			failed++
		}
	}
	scope := a.HistoryScope.String()
	// The narrow scope names the directory rather than saying "this project", which is the
	// one rung where you already know what it means and the name is more use. The repository
	// rung cannot: the whole point of it is that it is more than one directory.
	if a.HistoryScope == ScopeProject {
		if base := baseName(a.Root); base != "" {
			scope = base
		}
	}

	state := []span{styled(scope, fg(t.Colors.Stored))}
	if a.HistoryQuery != "" {
		state = append(state, styled("   /"+a.HistoryQuery, fg(t.Colors.Search)))
	}
	state = append(state, styled(fmt.Sprintf("   %d runs   %d failed", len(a.History), failed), fg(t.Colors.Dim)))
	return a.header("history", state)
}

func (a *App) drawHistory(width, height int) []string {
	t := a.Theme
	if len(a.History) == 0 {
		return nil
	}
	a.HistoryCursor = clamp(a.HistoryCursor, 0, len(a.History)-1)
	a.HistoryOffset = scrollTo(a.HistoryOffset, a.HistoryCursor, len(a.History), height)

	out := make([]string, 0, height)
	for i := a.HistoryOffset; i < len(a.History) && len(out) < height; i++ {
		m := a.History[i]
		glyph, colour := t.Glyphs.StatusOk, t.Colors.StatusOk
		if m.Failed() {
			glyph, colour = t.Glyphs.StatusFailed, t.Colors.StatusFailed
		}
		lines := 0
		for _, e := range m.Tasks {
			lines += e.Lines
		}
		commandStyle := fg(t.Colors.Text)
		if m.Failed() {
			commandStyle = fg(t.Colors.StatusFailed)
		}
		l := line{
			styled(glyph+" ", fgBold(colour)),
			styled(padRight(Ago(m.StartedUnix), 10), fg(t.Colors.Dim)),
			// Cut as well as padded: a longer command pushed that row's duration and line
			// count out of the columns every other row keeps them in.
			styled(padRight(clip(m.Command(), 30), 30), commandStyle),
			styled(fmt.Sprintf("%8s  %6d lines", duration(millis(m.DurationMs)), lines), fg(t.Colors.Dim)),
		}
		// Only present when a cross-run search is narrowing the list.
		if n, ok := a.HistoryHits[m.ID]; ok {
			suffix := "s"
			if n == 1 {
				suffix = ""
			}
			l = append(l, styled(fmt.Sprintf("   %d hit%s", n, suffix), fg(t.Colors.Search)))
		}
		out = append(out, l.renderRow(width, i == a.HistoryCursor, t, a.Phase, 0, 1))
	}
	return out
}

func (a *App) historyFooter() line {
	t := a.Theme
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if a.HistorySearching {
		return line{
			plain(" "),
			styled("search runs: ", fg(t.Colors.Search)),
			plain(a.HistoryQuery),
			styled(t.Glyphs.Cursor, fg(t.Colors.Search)),
			styled(fmt.Sprintf("   %d runs matched", len(a.History)), fg(t.Colors.Dim)),
			styled("   ⏎ keep   esc clear", fg(t.Colors.Dim)),
		}
	}
	return a.statusBar(&keys.HistorySection)
}
