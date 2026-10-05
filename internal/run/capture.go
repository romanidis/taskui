package run

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/redact"
	"github.com/romanidis/taskui/internal/task"
)

// capture is the capture goroutine: it works out what the run will look like, then runs it,
// and reports all of it as events.
func capture(p *process, events *queue, dir, root string, argv []string, attended bool) {
	// Read alongside the graph: the names the graph and the output are spelled in, and the
	// Taskfile's own env, which the summaries leave out.
	var project task.Project
	var wg sync.WaitGroup
	wg.Go(func() { project = task.ReadProject(dir) })
	// Resolve first: the tree should be on screen, greyed out, before any output arrives to
	// fill it in. The same call yields the environment dump the redactor is built from, so
	// masking is in place before the first line.
	//
	// A graph we could not resolve is not fatal — we still capture output, just without the
	// nesting. Redaction then has only the Taskfile's own env and the arguments to go on,
	// and the run view says how many it found rather than implying output has been checked.
	g, summary := graph.ResolveDetailed(dir, root)
	wg.Wait()
	events.push(Naming{Names: project.Names, Labels: project.Labels})
	if len(g.Edges) > 0 {
		events.push(GraphReady{Graph: g})
	}
	redactor := redact.Harvest(summary, project.Env, argv)
	events.push(Redacting{N: redactor.Len()})

	switch err := drive(p, events, dir, argv, attended, redactor); {
	case errors.Is(err, errStoppedBeforeStart):
		events.push(Exited{Code: -1})
	case err != nil:
		events.push(LineEvent{Raw: fmt.Sprintf("taskui: could not start `task %s`: %v", root, err)})
		events.push(Exited{Code: -1})
	}
}

// drive runs `task` on a pty and relays what it prints, blocking until the child exits.
//
// Unattended, its input is /dev/null rather than the terminal, which is how go-task knows
// there is nobody to answer a prompt. The pty stays its controlling terminal, reached through
// stdout instead, so stopping the run still reaches the whole group.
func drive(p *process, events *queue, dir string, argv []string, attended bool, redactor *redact.Redactor) error {
	cmd := exec.Command("task", argv...)
	if !attended {
		null, err := os.Open(os.DevNull)
		if err != nil {
			return err
		}
		defer func() { _ = null.Close() }()
		cmd.Stdin = null
		cmd.SysProcAttr = &syscall.SysProcAttr{Ctty: 1}
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		// Commands run behind go-task's prefixing pipe, so tty detection fails for them.
		// These are the escape hatches the major toolchains honour regardless of tty.
		"CARGO_TERM_COLOR=always", // cargo, clippy
		"CLICOLOR_FORCE=1",        // git, BSD coreutils
		"FORCE_COLOR=1",           // the node ecosystem
	)

	master, err := p.start(cmd)
	if err != nil {
		return err
	}
	relay(master, events, redactor)

	// The pty is at EOF, which means nothing is holding the slave open any more: go-task
	// has gone, and so has anything it left behind that was attached to the terminal. A
	// run that was stopped got there because reapGroup took the group; one that ended on
	// its own got there by finishing.
	code := p.wait(cmd)
	_ = master.Close()
	events.push(Exited{Code: code})
	return nil
}

// relay reads the pty until it closes, passing what it prints on as events.
//
// The read that ends it fails, and that is not worth reporting: some systems say the other
// end of a pty has gone with an error rather than an EOF, and the exit status is the answer
// either way.
func relay(master io.Reader, events *queue, redactor *redact.Redactor) {
	buf := make([]byte, 8192)
	var pending []byte
	for {
		n, err := master.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			// A pty gives us CRLF; split on LF and drop the CR.
			for {
				at := indexByte(pending, '\n')
				if at < 0 {
					break
				}
				line := pending[:at]
				pending = pending[at+1:]
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				// Mask here, at the boundary: nothing unredacted is ever put on the
				// channel, so no later code path can leak what it never received.
				text := mask(redactor, applyOverwrites(string(line)))
				for _, event := range parseLine(text) {
					events.push(event)
				}
			}

			// A line with no end in sight — minified JS, a base64 blob, `\r`-only
			// progress — is broken up rather than held. Everything below reprocesses the
			// whole of pending on every read, so holding it made a 6MB line cost 46s.
			for len(pending) > maxPending {
				cut, ok := breakAt(redactor, pending)
				if !ok {
					break
				}
				for _, event := range parseLine(mask(redactor, applyOverwrites(string(pending[:cut])))) {
					events.push(event)
				}
				pending = pending[cut:]
			}

			// Whatever is left has no newline yet. Emit it anyway: a prompt never gets
			// one, and waiting for it means the run looks hung — all but a tail that
			// could be the start of a secret still arriving. Masking needs the whole
			// secret, so until it is all here its first half is just text, and it was
			// shown as such until the rest of the line caught up.
			if len(pending) > 0 {
				text := mask(redactor, applyOverwrites(string(pending)))
				if text = text[:len(text)-redactor.Unfinished(text)]; text != "" {
					events.push(partialOf(text))
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// mask redacts a line so that no secret survives in either of the forms a Line keeps.
//
// Masking the raw bytes alone is not enough: a colour escape landing inside a secret — grep
// highlighting the `sk-` it was asked for, under the FORCE_COLOR every run gets — hides it
// from the raw text, and stripping the escapes for Plain puts it back together, which is
// the form the `.txt` archive keeps. Such a line gives up its colour rather than its secret.
func mask(redactor *redact.Redactor, text string) string {
	if redactor.Len() == 0 {
		return text
	}
	text = redactor.Redact(text)
	if plain := ansi.Strip(text); redactor.Redact(plain) != plain {
		return redactor.Redact(plain)
	}
	return text
}

// maxPending is how long an unterminated line is allowed to grow before it is broken.
const maxPending = 16 << 10

// breakAt picks where to break an overlong unterminated line: as late as leaves every
// secret that starts before the break fully arrived, on a rune boundary, and never inside
// a secret. False when there is not yet room to break safely.
func breakAt(redactor *redact.Redactor, pending []byte) (int, bool) {
	want := len(pending) - redactor.Longest()
	for want > 0 && want < len(pending) && !utf8.RuneStart(pending[want]) {
		want--
	}
	if want <= 0 {
		return 0, false
	}
	cut := redactor.Cut(string(pending), want)
	return cut, cut > 0 && cut <= len(pending)
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
