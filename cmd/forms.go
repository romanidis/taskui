package cmd

import (
	"errors"
	"slices"
	"strings"

	"github.com/romanidis/taskui/internal/task"
)

// opensPicker reports whether this invocation ends in the interactive picker. It is the only
// place an offer to write a Taskfile belongs: `--list`, `--run` and the other print-and-exit
// flags are asked by scripts as often as by people, and at a terminal they used to get the
// offer instead of the answer.
func opensPicker() bool {
	return !opts.list && opts.dump == "" && opts.graph == "" && !opts.lint &&
		opts.runTask == "" && opts.screenshot == ""
}

// listed reports whether name is a task go-task would run by that name: one in the list, by
// its name or an alias, or a namespace's default spelled out in full — `dev:default` is
// listed as `dev`, and go-task still accepts the long form.
func listed(tasks []task.Task, name string) bool {
	short := strings.TrimSuffix(name, ":default")
	for _, t := range tasks {
		if t.Name == name || t.Name == short || slices.Contains(t.Aliases, name) {
			return true
		}
	}
	return false
}

// matrixFormOK rejects `--matrix` on its own. Like `--json` it is a form another flag is
// printed in rather than a command, and launching the TUI at somebody who asked for a table
// is worse than saying so.
// formsOK rejects the flags that only mean something beside another one, given without it.
func formsOK() error {
	for _, check := range []func() error{jsonFormOK, matrixFormOK, narrowingFormOK} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// narrowingFormOK rejects `--since` and `--task` with nothing to narrow. On their own they
// were ignored, so `--since bogus --list` exited 0 and a typo in either passed unnoticed.
func narrowingFormOK() error {
	switch {
	case opts.since != "" && opts.searchFor == "":
		return errors.New("--since narrows --search; on its own there is nothing for it to narrow")
	case opts.searchTask != "" && opts.searchFor == "" && !opts.quickfix:
		return errors.New("--task narrows --search or --quickfix; on its own there is nothing " +
			"for it to narrow")
	}
	return nil
}

func matrixFormOK() error {
	if opts.matrix && !opts.lint {
		return errors.New("--matrix is the full-table form of --lint; on its own there is " +
			"nothing for it to be the form of")
	}
	return nil
}

// jsonFormOK rejects the two ways `--json` can be asked for and mean nothing.
func jsonFormOK() error {
	if !opts.asJSON {
		return nil
	}
	switch {
	case opts.quickfix:
		return errors.New("--quickfix is already a machine-readable form, and a different " +
			"one: file:line:col: message, for an editor's error list. Pick one")
	case !opts.list && opts.runTask == "" && opts.timeline == "":
		return errors.New("--json is the machine-readable form of --list, --run or " +
			"--timeline; on its own there is nothing for it to be the form of")
	}
	return nil
}
