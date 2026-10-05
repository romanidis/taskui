package run

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Line is one captured line, kept twice on purpose: Raw still has its escape sequences for
// rendering, Plain is what search runs over. Searching the raw bytes would miss matches
// wherever a colour change lands mid-word.
type Line struct {
	Raw   string
	Plain string
	// IsCommand is true for go-task's own `task: [name] <cmd>` echo, which is structure
	// rather than output and is worth rendering differently.
	IsCommand bool
}

// Restored rebuilds a line from the two stored halves. IsCommand is not persisted because
// it is derivable — it is exactly go-task's `task: [name] …` echo — and a marker in the
// `.txt` file would make the archive worse to grep.
func Restored(raw, plain string) Line {
	return Line{Raw: raw, Plain: plain, IsCommand: strings.HasPrefix(plain, "task: [")}
}

func newLine(raw string, isCommand bool) Line {
	return Line{Raw: raw, Plain: ansi.Strip(raw), IsCommand: isCommand}
}

// CommandText is a command echo with go-task's prefix taken off: `task: [test] go test
// ./...` becomes `go test ./...`.
//
// The prefix is an artifact of how the output is captured — `--output prefixed` is what
// tags every line with the task that printed it — rather than anything the command said.
// Whoever is showing the line already knows which task it belongs to, so restating it costs
// fifteen columns of a build log to say nothing.
//
// A line that is not a command echo comes back unchanged: there is nothing to strip, and a
// caller that has to check first is a caller that will forget to.
func CommandText(l Line) string {
	if !l.IsCommand {
		return l.Plain
	}
	text := strings.TrimPrefix(l.Plain, "task: ")
	if _, rest, ok := strings.Cut(text, "] "); ok {
		return rest
	}
	return text
}

// MaxLines caps the output kept per task.
//
// A run used to be a thing that ended, so the buffers could grow as far as they liked. A
// tailed container log does not end, and at a few hundred bytes a line an afternoon of one
// is gigabytes of resident memory. Old lines are dropped from the front and counted, so
// the view can say what it threw away rather than quietly rewriting history.
const MaxLines = 20_000

// DropBlock is how much goes at once. Draining a single line from the front of a
// 20,000-element slice on every line that arrives is quadratic; doing it once per 4,000 is
// not.
const DropBlock = 4_000

type TaskRun struct {
	Status Status
	// Note is why this task did not do what you expected, in go-task's own words — "up to
	// date", "precondition not met". It is attributed here rather than left where it was
	// printed: go-task announces a skip with no `[name]` prefix, so the line lands in the
	// *parent's* output, several rows away from the `⏸` it explains. A skipped task with no
	// reason beside it is the whole reason `⇧R` exists.
	Note  string
	Lines []Line
	// Dropped counts the lines that fell off the front of Lines to stay under MaxLines.
	Dropped  int
	started  time.Time
	duration time.Duration
	settled  bool
}

// Output is what the task printed, a line at a time, stripped of escapes: the live
// counterpart of what the archive reads back.
func (t *TaskRun) Output() []string {
	out := make([]string, 0, len(t.Lines))
	for _, l := range t.Lines {
		out = append(out, l.Plain)
	}
	return out
}

// Elapsed is time on the clock: the final figure once the task has finished, and a ticking
// one while it is still going. Showing nothing until a task completes means the only live
// timing on screen is the total, which is the least useful of them — during a slow build
// what you want to know is which step is taking it.
func (t *TaskRun) Elapsed() (time.Duration, bool) {
	if t.settled {
		return t.duration, true
	}
	if t.started.IsZero() {
		return 0, false
	}
	return time.Since(t.started), true
}

// CommandStatus is how the command echoed on line i went.
//
// go-task announces a command before running it and says nothing when it returns, so the
// verdict is read off the shape of what came after: another echo means this one finished,
// and the last echo carries the task's own status — a task that failed failed in its final
// command, and one still going is still inside it.
//
// A failure the Taskfile swallows (`ignore_error:`) is reported as a success, which is
// what it is from outside: go-task went on to the next command.
func (t *TaskRun) CommandStatus(i int) Status {
	if i < 0 || i >= len(t.Lines) || !t.Lines[i].IsCommand {
		return Pending
	}
	for _, l := range t.Lines[i+1:] {
		if l.IsCommand {
			return Ok
		}
	}
	return t.Status
}

// UnderCommand says how the line at i sits under the command that produced it: whether
// there is one above it in this task at all, and whether it is the last line before the next
// command starts.
//
// It is what lets output be drawn as a command's output rather than as loose lines. The
// answer is positional, like [TaskRun.CommandStatus] and for the same reason: go-task
// announces a command and never announces its end, so the end is where the next one begins.
func (t *TaskRun) UnderCommand(i int) (bool, bool) {
	if i < 0 || i >= len(t.Lines) || t.Lines[i].IsCommand {
		return false, false
	}
	under := false
	for j := i - 1; j >= 0; j-- {
		if t.Lines[j].IsCommand {
			under = true
			break
		}
	}
	if !under {
		return false, false
	}
	// The last line of the group is the one the next command follows, or the last line
	// there is.
	return true, i+1 >= len(t.Lines) || t.Lines[i+1].IsCommand
}

// Duration is the settled figure only, for the archive.
func (t *TaskRun) Duration() time.Duration {
	if t.settled {
		return t.duration
	}
	return 0
}

func RestoredTask(status Status, lines []Line, duration time.Duration) *TaskRun {
	return &TaskRun{Status: status, Lines: lines, duration: duration, settled: true}
}

func newTaskRun() *TaskRun { return &TaskRun{Status: Pending} }

func (t *TaskRun) close(now time.Time) {
	if !t.started.IsZero() {
		t.duration = now.Sub(t.started)
		t.settled = true
	}
}
