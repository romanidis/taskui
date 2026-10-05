package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/romanidis/taskui/internal/app"
	"github.com/romanidis/taskui/internal/diff"
	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/pivot"
	"github.com/romanidis/taskui/internal/search"
	"github.com/romanidis/taskui/internal/store"
	"github.com/romanidis/taskui/internal/task"
	"github.com/romanidis/taskui/internal/theme"
)

// archiveCommand runs whichever of the archive-reading flags was given, and reports whether
// one was. They are grouped here rather than inline because they share a shape — read the
// store, print, exit — and because rootRun is a dispatcher, not a list of every command.
func archiveCommand(cmd *cobra.Command, root string) (bool, error) {
	out := cmd.OutOrStdout()
	switch {
	case opts.timeline != "" && opts.asJSON:
		return true, printTimelineJSON(out, root, opts.timeline)
	case opts.timeline != "":
		return true, printTimeline(out, root, opts.timeline)
	case opts.diffTask != "":
		return true, printDiff(out, root, opts.diffTask)
	case opts.flaky:
		return true, printFlaky(out, root)
	// Not paired with --run: that combination runs the task first, and is handled with the
	// rest of --run once the Taskfile has been read.
	case opts.quickfix && opts.runTask == "":
		return true, printQuickfix(out, root, opts.searchTask)
	}
	return false, nil
}

// projectCommand runs whichever of the Taskfile-reading print-and-exit flags was given, and
// reports whether one was. The counterpart of archiveCommand, grouped for the same reason:
// they share a shape — read the project, print, exit — and rootRun is a dispatcher rather
// than a list of every command.
func projectCommand(cmd *cobra.Command, root string, tasks []task.Task, config theme.Config) (bool, error) {
	out := cmd.OutOrStdout()
	switch {
	case opts.list && opts.asJSON:
		return true, printTaskList(out, root, tasks)
	case opts.list:
		printTaskListText(out, tasks)
		return true, nil
	case opts.dump != "":
		return true, dumpPivot(opts.dump, app.New(tasks, root).WithConfig(config))
	case opts.graph != "":
		// Checked against the listing first: go-task answers an unknown name with nothing,
		// which printed the name alone as a one-node graph and exited 0 — the same answer
		// as a real task that runs nothing else.
		if !listed(tasks, opts.graph) {
			return true, fmt.Errorf("no task called %s — `taskui --list` shows them", opts.graph)
		}
		printGraph(out, root, opts.graph)
		return true, nil
	// Like --graph, this reads the project rather than the archive. Gaps exit ExitFound
	// rather than ExitFailed: `task precommit` fails on either, and a script that wants to
	// tell "this Taskfile has a hole in it" from "there is no Taskfile here" can.
	case opts.lint:
		if printLint(out, root, tasks, opts.matrix) > 0 {
			return true, exitWith(ExitFound)
		}
		return true, nil
	}
	return false, nil
}

func printTaskListText(out io.Writer, tasks []task.Task) {
	for _, t := range tasks {
		if _, err := fmt.Fprintf(out, "%s\t%s\n", t.Name, t.Desc); err != nil {
			// Piping into `head` closes the pipe on us, which is not a failure.
			return
		}
	}
	fmt.Fprintf(out, "-- %d tasks\n", len(tasks))
}

// listThemes names what is available, because "yours shadows the built-in of the same
// name" is only a useful rule if you can watch it happen.
func listThemes(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	for _, name := range theme.ListThemes() {
		resolved, problems := theme.LoadTheme(name)
		note := ""
		if len(problems) > 0 {
			note = "  (" + problems[0] + ")"
		}
		fmt.Fprintf(out, "%-12s %s%s\n", name, resolved.Glyphs.Wordmark, note)
	}
	fmt.Fprintf(out, "\nyours go in %s\n", theme.ThemesDir())
	fmt.Fprintf(out, "start one with `taskui --dump-theme default > %s/mine.yaml`\n", theme.ThemesDir())
	return nil
}

// searchStored greps every stored run, newest first, grouped by run and task.
func searchStored(pattern string, scope search.Scope) error {
	base := store.StateDir()
	query, err := search.NewQuery(pattern)
	if err != nil {
		return err
	}
	results, dropped := search.InStoreScoped(base, query, 50, scope)

	if len(results) == 0 {
		where := ""
		if scope.Task != "" {
			where += " in `" + scope.Task + "`"
		}
		if !scope.Since.IsZero() {
			where += " since " + scope.Since.Format("2006-01-02 15:04")
		}
		fmt.Printf("no matches for /%s/%s in %d stored runs\n", pattern, where, len(store.List(base)))
		return nil
	}

	total := 0
	for _, r := range results {
		mark := "✓"
		if r.Manifest.Failed() {
			mark = "✗"
		}
		fmt.Printf("\n%s %s  task %s  (%d hits)\n", mark, r.Manifest.ID, r.Manifest.Root, len(r.Hits))
		current := ""
		for _, hit := range r.Hits {
			if hit.Task != current {
				fmt.Printf("  %s\n", hit.Task)
				current = hit.Task
			}
			fmt.Printf("    %5d  %s\n", hit.LineNo, hit.Text)
			total++
		}
	}

	fmt.Printf("\n%d hits in %d runs\n", total, len(results))
	if dropped > 0 {
		// Never let a capped result read as a complete one.
		fmt.Printf("%d further hits not shown (per-run cap)\n", dropped)
	}
	return nil
}

// dumpPivot prints one grouping fully expanded.
//
// Takes the App rather than the pieces: the tasks it builds from are the App's own, the
// order is the one the UI would have used, and both the archive and the JSON listing are
// things it already knows how to fetch.
func dumpPivot(mode string, a *app.App) error {
	var chosen pivot.Pivot
	for _, p := range a.Pivots {
		if p.Name == mode {
			chosen = p
		}
	}
	if chosen.Name == "" {
		return fmt.Errorf("--dump expects one of %s, not %q",
			strings.Join(pivotNamesFor(a.Pivots), ", "), mode)
	}

	// The grouping and the ordering that are answers about *where a task is written* need
	// the JSON listing, which nothing on this path would otherwise fetch. A one-shot dump
	// has no first frame to be late for, so it waits rather than printing a flat list with no
	// hint that any grouping was missing.
	if mode == theme.FilePivot || a.Ordering().By == pivot.ByFile {
		a.StartEnrichment()
		a.AwaitDetails(detailGrace)
	}

	all := make([]int, len(a.Tasks))
	for i := range a.Tasks {
		all[i] = i
	}
	tree := pivot.Build(chosen, a.Tasks, all, a.Ordering())
	for _, row := range tree.Flatten(func(string) bool { return true }) {
		n := tree.Nodes[row.Node]
		glyph := " "
		// A group row that is also a task is marked, since the tree alone cannot show that
		// `backend:migrate` is runnable as well as foldable.
		runnable := " "
		count := ""
		if n.IsGroup() {
			glyph = "▾"
			count = fmt.Sprintf("  %d", n.Count)
			if n.Task != pivot.NoTask {
				runnable = "*"
			}
		}
		if _, err := fmt.Printf(
			"%s%s%s%s%s\n",
			strings.Repeat("  ", row.Depth),
			glyph,
			runnable,
			n.Label,
			count,
		); err != nil {
			return nil //nolint:nilerr // piping into `head` closes the pipe on us
		}
	}
	return nil
}

func printGraph(out io.Writer, root, rootTask string) {
	// Print the tree, marking revisits rather than expanding them twice.
	graph.Resolve(root, rootTask).Walk(rootTask, func(name string, depth int, repeat bool) bool {
		mark := ""
		if repeat {
			mark = "  (already shown)"
		}
		// Piping into `head` closes the pipe on us, which ends the walk and nothing else.
		_, err := fmt.Fprintf(out, "%s%s%s\n", strings.Repeat("  ", depth), name, mark)
		return err == nil
	})
}

// printTimeline is `--timeline`: one task's stored runs, newest first.
//
// Tab-separated and one run per line, like `--list`, so the answer to "when did this start
// failing" is available to a script and not only to a pair of eyes.
func printTimeline(out io.Writer, root, taskName string) error {
	points := store.Timeline(store.StateDir(), root, taskName)
	if len(points) == 0 {
		return fmt.Errorf("no stored runs of %q in this project", taskName)
	}
	for _, p := range points {
		status := "ok"
		if !p.Ok() {
			status = "failed"
		}
		fmt.Fprintf(out, "%s\t%s\t%dms\t%d lines\t%s\n",
			time.Unix(p.WhenUnix, 0).Format(time.RFC3339), status, p.DurationMs, p.Lines, p.Command())
	}
	fmt.Fprintf(out, "-- %d runs\n", len(points))
	return nil
}

// printDiff is `--diff`: what changed in one task between its last run and the last one
// that passed.
//
// Unified-ish rather than exactly `diff -u`: there are no `@@` hunk headers, because the
// line numbers are on every row instead and a header that has to be cross-referenced with
// the rows below it is a worse answer than the rows carrying it themselves.
func printDiff(out io.Writer, root, taskName string) error {
	base := store.StateDir()
	points := store.Timeline(base, root, taskName)
	if len(points) == 0 {
		return fmt.Errorf("no stored runs of %q in this project", taskName)
	}
	newest := points[0]
	older, ok := store.LastGreen(base, root, taskName, newest.RunID, 0)
	against := "when it last passed"
	if !ok {
		older, ok = store.Previous(base, root, taskName, newest.RunID, 0)
		against = "the run before"
		switch {
		case !ok && len(points) > 1:
			return fmt.Errorf("the earlier runs of %q are remembered but their output is no longer "+
				"stored — only the last %d runs keep theirs", taskName, store.KeepRuns)
		case !ok:
			return fmt.Errorf("only one stored run of %q — nothing to compare it against", taskName)
		}
	}

	edits := diff.Lines(store.Output(base, older), store.Output(base, newest))
	stat := diff.Count(edits)
	fmt.Fprintf(out, "--- %s  (%s, %s)\n", taskName, against, app.Ago(older.WhenUnix))
	fmt.Fprintf(out, "+++ %s  (%s)\n", taskName, app.Ago(newest.WhenUnix))
	for _, e := range diff.Hunks(edits, 3) {
		switch {
		case diff.IsGap(e):
			fmt.Fprintln(out, "...")
		case e.Op == diff.Ins:
			fmt.Fprintln(out, "+"+e.Text)
		case e.Op == diff.Del:
			fmt.Fprintln(out, "-"+e.Text)
		default:
			fmt.Fprintln(out, " "+e.Text)
		}
	}
	fmt.Fprintf(out, "-- +%d -%d\n", stat.Added, stat.Removed)
	return nil
}

// printFlaky is `--flaky`: the tasks whose result did not depend on the code.
//
// Exits non-zero when it finds any, so it composes into a script the way a check should —
// `taskui --flaky || echo "look into it"`.
func printFlaky(out io.Writer, root string) error {
	flakes := store.Flaky(store.StateDir(), root)
	if len(flakes) == 0 {
		fmt.Fprintln(out, "-- no task has gone both ways at one commit")
		return nil
	}
	for _, f := range flakes {
		// The invocation is a column of its own rather than part of the task name: the name
		// is what went both ways, the arguments are what the run carried, and a task reached
		// from an aggregate never saw them on its own command line.
		name := f.Task
		if args := f.Invocation(); args != "" {
			name += "\t" + args
		} else {
			name += "\t"
		}
		fmt.Fprintf(out, "%s\t%s\t%d passed\t%d failed\t%s\n",
			name, f.Short(), f.Passed, f.Failed, app.Ago(f.LastUnix))
	}
	fmt.Fprintf(out, "-- %d flaky\n", len(flakes))
	return exitBecause(ExitFound, "%d %s went both ways at one commit",
		len(flakes), plural(len(flakes), "task", "tasks"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// parseSince reads a span. Go's own durations plus the units people actually use for an
// archive: `2d` and `3w` are the natural way to say how far back to look, and
// [time.ParseDuration] stops at hours.
func parseSince(text string) (time.Duration, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, nil
	}
	unit := trimmed[len(trimmed)-1]
	scale := time.Duration(0)
	switch unit {
	case 'd':
		scale = 24 * time.Hour
	case 'w':
		scale = 7 * 24 * time.Hour
	}
	if scale > 0 {
		n, err := strconv.Atoi(strings.TrimSpace(trimmed[:len(trimmed)-1]))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("--since %q: expected a number before %q", text, string(unit))
		}
		return time.Duration(n) * scale, nil
	}

	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("--since %q: try 90m, 2d or 3w", text)
	}
	if d < 0 {
		return 0, fmt.Errorf("--since %q: cannot look forwards", text)
	}
	return d, nil
}
