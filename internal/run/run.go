// Package run runs a task and splits its output back apart, one bucket per task.
//
// `task` writes a single stream. We drive it with `--output prefixed`, which tags every
// output line with the task that produced it, and pair that with the execution graph from
// the graph package — the stream says who spoke, the graph says who called whom.
//
// Colour needs care. `--output prefixed` makes go-task pipe every command through its own
// prefixing writer, so a command's stdout is a pipe regardless of what taskui does —
// measured: isatty reports false inside prefixed mode even when go-task itself is on a
// pty. Tools that auto-detect therefore turn colour off, and no amount of pty gets it
// back. Forcing it by environment does: `CARGO_TERM_COLOR=always` restores cargo and
// clippy's colour through the pipe intact.
//
// The pty is still worth having — it keeps go-task's own output coloured and stops the
// usual switch to block buffering when stdout is not a terminal — it just is not what
// makes the tools colour.
package run

import (
	"sort"
	"strings"
	"time"

	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/shellwords"
	"github.com/romanidis/taskui/internal/task"
)

// Stored is a finished run being rebuilt from the archive.
type Stored struct {
	// ID is the archive directory this came out of. Carried so a stored run can be told
	// apart from the archive entry it *is* — diffing a run against itself is a diff of
	// nothing, and finding that out by producing it is worse than not offering it.
	ID              string
	Root            string
	Args            []string
	Force           bool
	Interactive     bool
	Started         time.Time
	Graph           graph.Graph
	Tasks           map[string]*TaskRun
	Order           []string
	Exit            int
	Duration        time.Duration
	RedactedSecrets int
}

// Run is one invocation of a task, and everything known about it so far.
//
// It belongs to whoever calls Poll — the UI, or a headless loop — and only that goroutine
// reads or writes it, with two exceptions built to be shared. proc is the child: a signal
// handler can stop the run from a goroutine of its own, the capture goroutine starts the
// child and reaps it, and a stop leaves a goroutine behind to take the group. events is
// what the capture goroutine fills and Poll drains, which is how everything it learns
// reaches the run without its ever being handed the run.
type Run struct {
	Root string
	// Args are the extra argv passed after the task name — `NAME=backend`, `-- -p ingest`.
	Args []string
	// Interactive means it ran with `--output interleaved` so the task could ask questions.
	Interactive bool
	// Force means it ran with `--force`, ignoring go-task's up-to-date checks.
	Force bool
	Graph graph.Graph
	Tasks map[string]*TaskRun
	// Order lists tasks in the order they first produced output.
	Order []string
	// Exit is set once the process is gone.
	Exit    int
	HasExit bool
	// Started is when this run began. Public so a caller can tell one run from another.
	Started     time.Time
	Duration    time.Duration
	HasDuration bool
	// RedactedSecrets is the number of secrets being masked out of this run's output.
	RedactedSecrets int
	// Sent is what has been typed at the task, echoed back so you can see the keystroke
	// landed. Under `--output prefixed` a task may produce nothing for a long time after
	// answering, and without this there is no way to tell "sent and waiting" from "not
	// sent at all".
	Sent string

	// stored marks a run loaded from the archive rather than executed here.
	stored   bool
	storedID string

	proc process

	// provisional is where the not-yet-terminated line lives, so the next read replaces it
	// rather than stacking up a copy per 8KB chunk.
	provisional *provisionalLine
	lastOutput  time.Time
	active      string
	hasActive   bool
	// events is what the capture goroutine sends, and nil for a run with none behind it.
	events  *queue
	drained bool
	// names is how the project spells its tasks; nil for a run with no project behind it,
	// which leaves every name as it arrived.
	names task.Names
	// labels are the other names go-task prints a task under: its `label:` and `prefix:`.
	labels []task.Label
}

// Start runs `task <root>` in dir. It returns immediately; call Poll to drain.
//
// interactive swaps `--output prefixed` for `--output interleaved`, which is the only way
// a prompt ever reaches us: go-task's prefixer is itself line-based, so a `Proceed? (y/n) `
// with no newline is held inside it forever and the run just looks hung. Measured — under
// prefixed the prompt never appears at all.
//
// The cost is per-line attribution. Interleaved output still carries go-task's
// `task: [name] <cmd>` announcements, so lines are attributed to whichever task last
// spoke, which is correct for a sequential run and wrong under parallel `deps:`.
// Interactive runs are inherently sequential, so that trade is worth making — but only
// when asked for.
func Start(dir, root string, args []string, interactive, force bool) (*Run, error) {
	r := &Run{
		Root:        root,
		Args:        append([]string(nil), args...),
		Interactive: interactive,
		Force:       force,
		Graph:       graph.New(),
		Tasks:       map[string]*TaskRun{},
		Started:     time.Now(),
		lastOutput:  time.Now(),
		events:      &queue{},
	}

	// Handed what it shares with the run and nothing more: the process it starts and reaps,
	// and the queue it fills. Everything else on a Run belongs to whoever calls Poll, and a
	// goroutine that is never given the run cannot reach any of it.
	go capture(&r.proc, r.events, dir, root, r.argv())
	return r, nil
}

// argv is what go-task is invoked with: the output mode, the task, then the flags.
func (r *Run) argv() []string {
	mode := "prefixed"
	if r.Interactive {
		mode = "interleaved"
	}
	argv := []string{"--output", mode, r.Root}
	// `--force` before the user's own arguments: theirs may include a `--` separator,
	// after which everything is CLI_ARGS rather than a flag.
	if r.Force {
		argv = append(argv, "--force")
	}
	// Passed through verbatim, already split shell-style: `--` and `NAME=value` are just
	// argv entries to go-task.
	return append(argv, r.Args...)
}

func (r *Run) Finished() bool  { return r.HasExit }
func (r *Run) Cancelled() bool { return r.proc.cancelled.Load() }

// Outcome is how the run as a whole is going: Running until it ends, then Ok or Failed by
// its exit status. A run's answer is the same kind as its tasks', and it was worked out by
// hand wherever one was wanted.
func (r *Run) Outcome() Status {
	switch {
	case !r.HasExit:
		return Running
	case r.Exit == 0:
		return Ok
	default:
		return Failed
	}
}

// ExitCode is the run's exit status, or -1 while it has none.
func (r *Run) ExitCode() int {
	if r.HasExit {
		return r.Exit
	}
	return -1
}

// Killed is true once SIGKILL has gone out. There is nothing louder left to try, so the UI
// stops offering to stop it harder.
func (r *Run) Killed() bool { return r.proc.killed.Load() }

// over reports whether there is no longer a process to stop, safely from any goroutine.
//
// Not Finished: HasExit is written by Poll on the UI goroutine, and a stop can come from a
// signal handler on another. For a live run the capture goroutine's own record is the
// answer, and it is also the more accurate one — the process is gone the moment it is
// reaped, not a poll later when the UI hears about it. A run with no capture behind it,
// stored or built for a test, has only HasExit, and only one goroutine to read it from.
func (r *Run) over() bool {
	if r.events == nil {
		return r.HasExit
	}
	return r.proc.reaped.Load()
}

// IsStored is true when this Run came off disk rather than off a pty. The run view uses it
// to avoid implying a stored run is still doing something.
func (r *Run) IsStored() bool { return r.stored }

// StoredID is the archive id a stored run came from, and empty for a live one.
func (r *Run) StoredID() string { return r.storedID }

// TaskNames lists every task with a bucket, sorted — the deterministic stand-in for Rust's
// ordered map.
func (r *Run) TaskNames() []string {
	out := make([]string, 0, len(r.Tasks))
	for k := range r.Tasks {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SendInput sends keystrokes to the running task. `wrangler` and friends ask questions;
// without this the only answer taskui can give is to kill them.
func (r *Run) SendInput(bytes []byte) bool {
	if r.Finished() || !r.proc.write(bytes) {
		return false
	}

	// Printable characters as themselves; control keys as something readable.
	var b strings.Builder
	b.WriteString(r.Sent)
	for _, c := range bytes {
		switch {
		case c == '\r' || c == '\n':
			b.WriteRune('⏎')
		case c == 0x7f:
			b.WriteRune('⌫')
		case c == 0x03:
			b.WriteString("^C")
		case c == 0x04:
			b.WriteString("^D")
		case c == '\t':
			b.WriteRune('⇥')
		case c >= 0x21 && c <= 0x7e, c == ' ':
			b.WriteByte(c)
		default:
			b.WriteRune('·')
		}
	}
	// Only the tail matters; this is a receipt, not a transcript.
	sent := []rune(b.String())
	if len(sent) > 40 {
		sent = sent[len(sent)-40:]
	}
	r.Sent = string(sent)
	return true
}

// SilentFor is how long the task has produced nothing.
//
// A run under `--output prefixed` that is blocked on a prompt looks exactly like one that
// is slow: go-task's prefixer holds the unterminated question, so nothing arrives. Silence
// is the only signal there is.
func (r *Run) SilentFor() time.Duration { return time.Since(r.lastOutput) }

// PendingPrompt is the unterminated tail, if the task is sitting on one.
func (r *Run) PendingPrompt() (string, bool) {
	if r.provisional == nil {
		return "", false
	}
	t, ok := r.Tasks[r.provisional.task]
	if !ok || r.provisional.index >= len(t.Lines) {
		return "", false
	}
	text := strings.TrimRight(t.Lines[r.provisional.index].Plain, "\r")
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

// Complete is how many of a task's buffered lines are final: all of them, less the
// unterminated one still growing at the end.
//
// That one is replaced in place as it grows, or taken away when it turns out to belong to
// another task, so anything that reads the buffer as append-only — the host's event stream
// — has to stop short of it. Counting it had the stream send `half of a li` and never the
// line it grew into, and a fragment moved to another task left the count one past the end,
// so the task's next real line was never sent at all.
func (r *Run) Complete(name string) int {
	t, ok := r.Tasks[name]
	if !ok {
		return 0
	}
	if r.provisional != nil && r.provisional.task == name && r.provisional.index < len(t.Lines) {
		return r.provisional.index
	}
	return len(t.Lines)
}

// LooksLikeAPrompt guesses whether the tail is a question. Used only to nudge the user
// toward the input key — a wrong guess costs nothing but a missing hint.
func (r *Run) LooksLikeAPrompt() bool {
	text, ok := r.PendingPrompt()
	if !ok {
		return false
	}
	t := strings.TrimRight(text, " \t")
	lower := strings.ToLower(t)
	return strings.HasSuffix(t, "?") ||
		strings.HasSuffix(t, ":") ||
		strings.HasSuffix(t, ">") ||
		strings.HasSuffix(t, ")") ||
		strings.Contains(lower, "(y/n)") ||
		strings.Contains(lower, "[y/n]")
}

// Cancel stops the run, politely.
//
// Killing the `task` process alone is not enough: it is the shell commands beneath it that
// are doing the work, and they survive it — verified by killing taskui mid-run and
// watching `sleep` carry on. The pty puts the child in its own session, so the whole group
// can be signalled at once, which is what actually reaps the tree.
//
// Both signals sent here are catchable, deliberately: `docker compose up` takes SIGTERM as
// "stop the containers", and skipping that would leave the stack running while taskui
// reported it stopped. What happens when they are ignored is Kill's problem.
//
// A run still resolving its graph has no process yet, and needs none signalled: the capture
// goroutine checks this flag under the same lock it starts the child under, so a stop that
// lands first means the child is never started.
func (r *Run) Cancel() {
	// A run that is over has nothing left to stop, and marking it stopped would make one
	// that finished on its own read as cancelled.
	if r.over() {
		return
	}
	r.proc.stop()
}

// Kill insists, and stops waiting about it.
//
// SIGTERM and SIGHUP can both be caught, and plenty of things catch them: a shell script
// with a `trap`, a runtime waiting on a network call that is never going to return.
// SIGKILL cannot be, so it is what is left when the polite ones have been sent and the
// process is still there.
//
// Not what stopping does first. SIGKILL runs no cleanup handler, which on a compose stack
// means the containers stay up with nothing left to take them down — so this is a second,
// deliberate press rather than the opening move.
func (r *Run) Kill() { r.proc.kill() }

// Command is what was actually invoked, for the header and the history list.
func (r *Run) Command() string {
	force := ""
	if r.Force {
		force = " --force"
	}
	if len(r.Args) == 0 {
		return "task " + r.Root + force
	}
	return "task " + r.Root + force + " " + shellwords.Join(r.Args)
}

// FromStored rebuilds a finished run from the archive.
func FromStored(s Stored) *Run {
	return &Run{
		Root:            s.Root,
		Args:            s.Args,
		Interactive:     s.Interactive,
		Force:           s.Force,
		Graph:           s.Graph,
		Tasks:           s.Tasks,
		Order:           s.Order,
		Exit:            s.Exit,
		HasExit:         true,
		Started:         s.Started,
		Duration:        s.Duration,
		HasDuration:     true,
		RedactedSecrets: s.RedactedSecrets,
		stored:          true,
		storedID:        s.ID,
		lastOutput:      time.Now(),
	}
}

// Wait drains the run until it ends, calling tick after every poll: the loop a headless
// caller runs where the UI would have its own. One more poll after the exit takes whatever
// landed with it. tick may be nil.
func (r *Run) Wait(tick func()) {
	for {
		r.Poll()
		if tick != nil {
			tick()
		}
		if r.Finished() {
			break
		}
		time.Sleep(waitEvery)
	}
	r.Poll()
	if tick != nil {
		tick()
	}
}

// waitEvery is how often Wait polls.
const waitEvery = 20 * time.Millisecond

// Poll drains whatever the capture goroutine has produced. It returns true if anything
// changed, so the UI can skip redrawing when nothing has.
func (r *Run) Poll() bool {
	if r.events == nil || r.drained {
		return false
	}
	// Drain first, then apply: apply may retire the queue.
	batch := r.events.drain()
	for _, e := range batch {
		r.apply(e)
	}
	return len(batch) > 0
}
