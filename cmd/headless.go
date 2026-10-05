package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/romanidis/taskui/internal/app"
	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/store"
)

// runHeadless runs a task to completion and prints what the capture layer reconstructed:
// the execution tree, each task's status and duration, and its output indented beneath it.
func runHeadless(dir, target string, argv []string, quickfix bool) error {
	r, err := run.StartUnattended(dir, target, argv, false)
	if err != nil {
		return err
	}
	stop := stopOnSignal(r)
	r.Wait(nil)
	stop()

	if !quickfix {
		r.Graph.Walk(target, func(name string, depth int, repeat bool) bool {
			if repeat {
				return true
			}
			t, ok := r.Tasks[name]
			if !ok {
				return true
			}
			pad := strings.Repeat("  ", depth)
			secs := ""
			if d, ok := t.Elapsed(); ok {
				secs = fmt.Sprintf("%.2fs", d.Seconds())
			}
			// Ignore write errors throughout: piping into `head` closes the pipe on us.
			fmt.Printf("%s%s %s  %s\n", pad, t.Status.Glyph(), name, secs)
			for _, l := range t.Lines {
				marker := "│"
				if l.IsCommand {
					marker = "$"
				}
				// Raw, so colour shows on a terminal and survives `cat -v` inspection.
				fmt.Printf("%s  %s %s\n", pad, marker, l.Raw)
			}
			return true
		})
	}
	exit := r.ExitCode()
	// With --quickfix the output is a list an editor parses, so nothing else may go to
	// stdout: not the tree, not the exit line, not where it was saved.
	if !quickfix {
		fmt.Printf("\nexit %d  in %.2fs\n", exit, r.Duration.Seconds())
	}

	// Store it just as the TUI would: a run is a run whichever way it was started, and
	// `--run` output you cannot search later would be a trap.
	path, err := store.Save(store.StateDir(), dir, r)
	switch {
	case err != nil && quickfix:
		fmt.Fprintf(os.Stderr, "taskui: not saved: %v\n", err)
	case err != nil:
		fmt.Printf("not saved: %v\n", err)
		if exit != 0 {
			return exitWith(exit)
		}
		return nil
	case !quickfix:
		fmt.Printf("saved to %s  (%d secrets masked)\n", path, r.RedactedSecrets)
	}

	if quickfix {
		// Run and populate in one keystroke: the run just happened here, so its own
		// directory is what the references resolve against.
		writeQuickfix(os.Stdout, r, dir, opts.searchTask)
	}
	// `--run` means run this and be it. The tree above already ends in the exit line, so
	// there is nothing left to say — only a status to carry.
	if exit != 0 {
		return exitWith(exit)
	}
	return nil
}

// drive plays keys into a live run, then lets it finish.
//
// The plain screenshot path applies keys to a finished run, which cannot exercise anything
// interactive — an interactive run never finishes on its own, so waiting first is a
// deadlock. Keys are paced so the child has time to reach its prompt between them.
func drive(a *app.App, feed string) {
	deadline := time.Now().Add(30 * time.Second)
	pending := app.KeysFrom(feed)
	nextKey := time.Now().Add(400 * time.Millisecond)

	for {
		a.PollRun()
		a.RefreshLive()
		if time.Now().After(deadline) {
			break
		}
		if time.Now().After(nextKey) || time.Now().Equal(nextKey) {
			if len(pending) > 0 {
				a.HandleKey(pending[0])
				pending = pending[1:]
				nextKey = time.Now().Add(400 * time.Millisecond)
			} else if a.Run != nil && a.Run.Finished() {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.PollRun()
	a.RefreshLive()
}

// detailGrace is how long a one-frame render waits for the JSON listing. Generous, because
// the alternative is a frame that differs run to run depending on how the race went — which
// is the opposite of what `--screenshot` is for.
const detailGrace = 5 * time.Second

// coverGrace is the same budget for the coverage walk, which is a `task --summary` for every
// task rather than one call — dozens of process spawns on a Taskfile the check is worth
// running on at all.
const coverGrace = 20 * time.Second

// screenshotRun is `--run` with `--screenshot`: the run view of a real run, drawn once it
// is over, and the run's status to exit with.
func screenshotRun(out io.Writer, a *app.App) error {
	a.StartEnrichment()
	if err := a.StartRun(opts.runTask); err != nil {
		return err
	}
	// Starting a run no longer takes the screen — the picker keeps it and shows the run
	// under the task. This flag is documented as rendering the run view, so it asks for it.
	a.ResumeRun()
	started := a.Run
	a.AwaitDetails(detailGrace)
	drive(a, opts.keys)
	if err := screenshot(out, a, opts.screenshot, ""); err != nil {
		return err
	}
	// Still `--run`: the frame is what was asked to be seen, and the status is still the
	// task's. It used to be 0 whatever the task did.
	if started != nil && started.Outcome() == run.Failed {
		return exitWith(started.Exit)
	}
	return nil
}

func screenshot(out io.Writer, a *app.App, size, feed string) error {
	w, h, err := parseSize(size)
	if err != nil {
		return err
	}
	for _, k := range app.KeysFrom(feed) {
		a.HandleKey(k)
		// A key can start a run, and `⏎` now leaves you in the picker with that run
		// unfolded under its task — so the run is part of the frame, and every key after
		// it is aimed at what it printed. Let it finish before playing the next one, or
		// the fold key lands on a run with nothing in it yet.
		if a.RunInFlight() {
			drive(a, "")
		}
	}
	a.Phase = opts.phase
	if opts.colour {
		// Nothing here is a terminal, so lipgloss would otherwise decide there is no point
		// colouring anything. Saying otherwise is the whole request.
		lipgloss.SetColorProfile(termenv.TrueColor)
		_, _ = fmt.Fprintln(out, a.RenderFrame(w, h))
		return nil
	}
	for _, l := range a.RenderHeadless(w, h) {
		if _, err := fmt.Fprintln(out, l); err != nil {
			return nil //nolint:nilerr // piping into `head` closes the pipe on us
		}
	}
	return nil
}

func parseSize(size string) (int, int, error) {
	parts := strings.FieldsFunc(size, func(r rune) bool { return r == 'x' || r == 'X' })
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("--screenshot expects WxH, e.g. 90x30")
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}
