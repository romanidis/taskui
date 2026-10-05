package app

import (
	"fmt"
	"strings"

	"github.com/romanidis/taskui/internal/pivot"
)

// previewFrom is the width at which the list has room for a preview beside it. Below it the
// list keeps the whole width, and `d` shows the same description on a screen of its own.
const previewFrom = 140

// previewWidth is how much of a frame the preview takes, or 0 when there is no room for one.
// Two fifths, and never so much that a description is set in a column wider than it reads
// comfortably in.
func previewWidth(width int) int {
	if width < previewFrom {
		return 0
	}
	return min(72, width*2/5)
}

// drawPicker is the list, and on a wide terminal the task under the cursor described beside
// it.
//
// What a task is used to be a keypress away on a screen of its own, which covered the list
// you were choosing from. With the room, it is simply there: what the row does, where it is
// written, how it went, and what it will run, following the cursor. The commands come from
// the coverage walk, which already asked go-task about every task, so moving the cursor
// starts no process.
func (a *App) drawPicker(width, height int) []string {
	side := previewWidth(width)
	if side == 0 {
		return a.drawTree(width, height)
	}
	listWidth := width - side - 1
	list := a.drawTree(listWidth, height)
	pane := a.drawPreview(side, height)
	divider := line{styled("│", fg(a.Theme.Colors.Rule))}.render(1, false, a.Theme.Colors.Selection)
	blank := strings.Repeat(" ", listWidth)

	out := make([]string, 0, height)
	for i := range height {
		row := blank
		if i < len(list) {
			row = list[i]
		}
		out = append(out, row+divider+pane[i])
	}
	return out
}

// drawPreview describes what the cursor is on: a task, or the group it is a header for.
func (a *App) drawPreview(width, height int) []string {
	t := a.Theme
	room := max(0, width-3)
	var lines []line
	if node := a.SelectedNode(); node != nil {
		title := line{plain(" "), styled(node.Label, fgBold(t.Colors.Accent))}
		switch {
		case node.Task != pivot.NoTask:
			name := a.Tasks[node.Task].Name
			title[1] = styled(name, fgBold(t.Colors.Accent))
			lines = append(lines, title, line{})
			for _, l := range a.describeTask(name, a.summaries[name], room) {
				lines = append(lines, append(line{plain(" ")}, l...))
			}
			// The walk has not landed: say so rather than show a task with nothing to run.
			if _, known := a.summaries[name]; !known && a.reaches.running() {
				lines = append(lines, line{}, line{styled("   reading what it runs…", fg(t.Colors.Dim))})
			}
		default:
			count := fmt.Sprintf("   %d %s", node.Count, plural(node.Count, "task", "tasks"))
			lines = append(lines, title, line{}, line{styled(count, fg(t.Colors.Dim))})
			if names := a.Reaches[node.Key]; len(names) > 0 {
				lines = append(lines, line{
					styled("   run by     ", fg(t.Colors.Dim)),
					styled(clip(strings.Join(names, ", "), max(8, room-13)), fg(t.Colors.Alias)),
				})
			}
		}
	}

	out := make([]string, 0, height)
	for i := range height {
		var l line
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, l.render(width, false, t.Colors.Selection))
	}
	return out
}
