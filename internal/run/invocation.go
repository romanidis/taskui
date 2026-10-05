package run

import "github.com/romanidis/taskui/internal/shellwords"

// Invocation is what go-task is asked to do: one task, the arguments after it, and the two
// ways taskui can change how it runs.
//
// A value of its own because every start has to be repeatable. `r` runs a task again the way
// it ran, watch mode does the same on every save, ⇧F re-runs failures with the arguments they
// failed with, and a question about production has to remember exactly what it is asking
// about until it is answered. Each of those once carried the four fields loose, and each
// spelled the command line out for itself.
type Invocation struct {
	// Task is the task invoked, which the run's tree grows from.
	Task string
	// Args are the extra argv passed after the task name — `NAME=backend`, `-- -p ingest`.
	Args []string
	// Force runs it with `--force`, ignoring go-task's up-to-date checks.
	Force bool
	// Interactive swaps `--output prefixed` for `--output interleaved`, which is the only way
	// a prompt ever reaches us: go-task's prefixer is itself line-based, so a
	// `Proceed? (y/n) ` with no newline is held inside it forever and the run just looks hung.
	// Measured — under prefixed the prompt never appears at all.
	//
	// The cost is per-line attribution. Interleaved output still carries go-task's
	// `task: [name] <cmd>` announcements, so lines are attributed to whichever task last
	// spoke, which is correct for a sequential run and wrong under parallel `deps:`.
	// Interactive runs are inherently sequential, so that trade is worth making — but only
	// when asked for.
	Interactive bool
}

// Command is the command line as somebody would type it, for the header and the history list.
func (inv Invocation) Command() string {
	force := ""
	if inv.Force {
		force = " --force"
	}
	if len(inv.Args) == 0 {
		return "task " + inv.Task + force
	}
	return "task " + inv.Task + force + " " + shellwords.Join(inv.Args)
}

// Rerun is task started again the way this invocation ran: with its force, so a task go-task
// thinks is up to date does not decline, and its interactivity, so one waiting on a prompt
// does not hang again. Only the task invoked gets the arguments back — a task it reached was
// never given them.
func (inv Invocation) Rerun(task string) Invocation {
	again := Invocation{Task: task, Force: inv.Force, Interactive: inv.Interactive}
	if task == inv.Task {
		again.Args = inv.Args
	}
	return again
}

// argv is what go-task is invoked with: the output mode, the task, then the flags.
func (inv Invocation) argv() []string {
	mode := "prefixed"
	if inv.Interactive {
		mode = "interleaved"
	}
	argv := []string{"--output", mode, inv.Task}
	// `--force` before the user's own arguments: theirs may include a `--` separator,
	// after which everything is CLI_ARGS rather than a flag.
	if inv.Force {
		argv = append(argv, "--force")
	}
	// Passed through verbatim, already split shell-style: `--` and `NAME=value` are just
	// argv entries to go-task.
	return append(argv, inv.Args...)
}
