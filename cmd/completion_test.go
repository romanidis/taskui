package cmd

import (
	"testing"

	"github.com/spf13/pflag"
)

// Every flag whose value is a task name completes to one. `--task` was left out of the
// list, so the shell offered it file names.
func TestEveryTaskValuedFlagCompletesToTasks(t *testing.T) {
	for _, name := range []string{flagRun, flagGraph, flagTimeline, flagDiff, flagTask} {
		if _, ok := rootCmd.GetFlagCompletionFunc(name); !ok {
			t.Errorf("--%s has no completion", name)
		}
	}
}

// pflag reads the first backquoted word of a flag's help as the name of its value, so a
// backquote meant as emphasis turns into the placeholder: `--keys ^d`.
func TestNoFlagHelpNamesItsValueByAccident(t *testing.T) {
	rootCmd.Flags().VisitAll(func(f *pflag.Flag) {
		name, _ := pflag.UnquoteUsage(f)
		switch name {
		case "", "string", "int", "strings", "duration", "float":
		default:
			t.Errorf("--%s is listed as `--%s %s`", f.Name, f.Name, name)
		}
	})
}
