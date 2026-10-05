package cmd

// `--json` is the machine-readable form of the print-and-exit commands, for a front end
// that is not this terminal — an editor plugin, a CI step, a dashboard.
//
// The engine is already separate from the screen: `internal/run`, `store`, `task` and the
// rest know nothing about Bubble Tea. So this is not a second implementation of anything.
// It is the same run, the same archive and the same discovery, addressed to a program.
//
// Three shapes, each pairing with a flag that already existed:
//
//   - `--list --json` — the tasks, with what the archive knows about how each went
//   - `--run <task> --json` — the run as newline-delimited JSON, one event per line, ending
//     when the run does. `--events <path>` is the same stream from inside the TUI, for a
//     host that is showing the terminal rather than drawing the run itself.
//   - `--timeline <task> --json` — one task's stored runs
//
// The streaming form is deliberately one process per run rather than a long-lived daemon.
// The archive on disk is already the shared state, so separate processes see each other's
// history for free, and a caller that wants several runs at once has a process table to
// multiplex them with.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/romanidis/taskui/internal/events"
	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/store"
	"github.com/romanidis/taskui/internal/task"
)

// listing is `--list --json`.
type listing struct {
	Project string     `json:"project"`
	Tasks   []taskJSON `json:"tasks"`
}

type taskJSON struct {
	// Name is the full colon path, which is what every other command takes as an argument.
	Name      string   `json:"name"`
	Desc      string   `json:"desc,omitempty"`
	Aliases   []string `json:"aliases,omitempty"`
	Dangerous bool     `json:"dangerous,omitempty"`
	// Taskfile and Line are where it is written, for a caller that wants to open it.
	Taskfile string `json:"taskfile,omitempty"`
	Line     int    `json:"line,omitempty"`
	UpToDate bool   `json:"up_to_date,omitempty"`
	// Last is how the archive says it went, absent when it has never run here.
	Last *outcomeJSON `json:"last,omitempty"`
}

type outcomeJSON struct {
	Ok   bool  `json:"ok"`
	When int64 `json:"when_unix"`
}

// printTaskList is `--list --json`.
//
// The locations and up-to-date flags come from `task --list-all --json`, which is slow
// enough on a workspace full of `sources:` globs that the TUI fetches it in the background.
// Here there is no frame to get out of the way of, so it is waited for — and if the call
// fails, the listing goes out without those two fields rather than not at all.
func printTaskList(out io.Writer, root string, tasks []task.Task) error {
	outcomes := store.LastOutcomes(store.StateDir(), root)
	details, err := task.Details(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taskui: %v\n", err)
	}

	page := listing{Project: root, Tasks: make([]taskJSON, 0, len(tasks))}
	for _, t := range tasks {
		d, known := details[t.Name]
		if known {
			t = t.With(d)
		}
		entry := taskJSON{
			Name:      t.Name,
			Desc:      t.Desc,
			Aliases:   t.Aliases,
			Dangerous: t.Dangerous,
		}
		if known {
			entry.Taskfile, entry.Line, entry.UpToDate = t.Where.File, t.Where.Line, d.UpToDate
		}
		if o, ok := outcomes[t.Name]; ok {
			entry.Last = &outcomeJSON{Ok: o.Ok, When: o.WhenUnix}
		}
		page.Tasks = append(page.Tasks, entry)
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(page)
}

// printTimelineJSON is `--timeline <task> --json`: the same points the TUI's timeline draws.
func printTimelineJSON(out io.Writer, root, taskName string) error {
	points := store.Timeline(store.StateDir(), root, taskName)
	if len(points) == 0 {
		return fmt.Errorf("no stored runs of %q in this project", taskName)
	}
	type pointJSON struct {
		RunID      string `json:"run_id"`
		Root       string `json:"root"`
		When       int64  `json:"when_unix"`
		Status     string `json:"status"`
		DurationMs int64  `json:"duration_ms"`
		Lines      int    `json:"lines"`
		Commit     string `json:"commit,omitempty"`
	}
	page := make([]pointJSON, 0, len(points))
	for _, p := range points {
		page = append(page, pointJSON{
			RunID: p.RunID, Root: p.Root, When: p.WhenUnix,
			Status: p.Status.String(), DurationMs: p.DurationMs, Lines: p.Lines, Commit: p.Commit,
		})
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(page)
}

// --- the run stream ---------------------------------------------------------------------

// streamRun is `--run <task> --json`: the run as it happens, one event per line.
//
// The shapes and the diffing live in internal/events, which the TUI's `--events` uses too:
// one protocol with two ways out, rather than two protocols that drift.
func streamRun(out io.Writer, dir, target string, argv []string) error {
	r, err := run.Start(dir, target, argv, false, false)
	if err != nil {
		return err
	}
	sink := events.New(nopCloser{out})
	// The consumer of this form is drawing the run, so it wants the output too. A TUI with
	// a host attached does not: the terminal in front of you is already showing it.
	sink.Lines = true

	// A front end that started this stream stops it by signalling the process — `jobstop`
	// in Neovim, ^C at a shell.
	defer stopOnSignal(r)()

	deltas := events.NewDeltas()
	deltas.Start(sink, r, dir)
	r.Wait(func() { deltas.Flush(sink, r) })

	exit := r.ExitCode()
	// Saved like any other run: a run is a run whichever front end started it, and one that
	// left nothing behind could not be searched, diffed or timelined afterwards.
	saved, err := store.Save(store.StateDir(), dir, r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "taskui: not saved: %v\n", err)
	}
	deltas.Finish(sink, r, saved)
	if exit != 0 {
		return exitWith(exit)
	}
	return nil
}

// stopOnSignal stops r when taskui is interrupted or terminated, and returns the function
// that stops listening.
//
// Every headless run needs it. Without a handler a signal kills taskui and leaves `task` and
// everything under it running, because the child is a session leader of its own: the
// property that lets the group be reaped is the same one that lets it survive us. Only
// `--run --json` had one, so `kill` on a plain `--run` left its `sleep` behind.
func stopOnSignal(r *run.Run) func() {
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-stopping:
				// A second ask is louder, as a second `x` is. Still registered after the
				// first, the handler swallowed every Ctrl-C after it.
				if r.Cancelled() {
					r.Kill()
				} else {
					r.Cancel()
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(stopping)
		close(done)
	}
}

// nopCloser lets a plain writer stand in for the closer a sink owns: stdout is not ours to
// close.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
