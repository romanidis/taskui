/*
Copyright © 2026 Dmitry Romanidis
Licensed under the MIT licence. See LICENSE.
*/

// Package cmd is taskui's command line: everything that can happen before, instead of, or
// alongside the TUI.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/romanidis/taskui/internal/app"
	"github.com/romanidis/taskui/internal/events"
	"github.com/romanidis/taskui/internal/search"
	"github.com/romanidis/taskui/internal/shellwords"
	"github.com/romanidis/taskui/internal/task"
	"github.com/romanidis/taskui/internal/theme"
)

// Stamped by the linker at release time; see the Taskfile and .goreleaser.yaml.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type options struct {
	list       bool
	dump       string
	graph      string
	runTask    string
	screenshot string
	args       string
	last       bool
	configPath string
	dumpConfig bool
	searchFor  string
	timeline   string
	diffTask   string
	flaky      bool
	lint       bool
	matrix     bool
	quickfix   bool
	asJSON     bool
	events     string
	searchTask string
	since      string
	keys       string
	themeName  string
	listThemes bool
	dumpTheme  string
	colour     bool
	phase      int
}

var opts options

// v is the Viper the config is read through. Kept package-level so cobra's OnInitialize
// can fill it before Run.
var v = viper.New()

var rootCmd = &cobra.Command{
	Use:   "taskui [directory]",
	Short: "Fold, pivot and search your Taskfile",
	Long: `A folding, searchable front end for go-task.

Browse the tasks in a Taskfile, run them, and keep their output around to fold
and search afterwards — live and across previous runs.`,
	Example: `  taskui                        browse the Taskfile here
  taskui ~/src/myrepo           …or somewhere else
  taskui --search 'FAIL|error'  grep every stored run, from anywhere
  taskui --search FAIL --task test --since 2d
  taskui --run all              run headlessly and print the captured tree
  taskui --timeline test        how ` + "`test`" + ` has been going, run after run
  taskui --diff test            what changed since ` + "`test`" + ` last passed
  taskui --flaky                tasks that went both ways at one commit
  taskui --dump-config          print every colour at its default`,
	Args:          cobra.MaximumNArgs(1),
	Version:       versionString(),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          rootRun,
}

// versionString prefers what the linker stamped, and falls back to what the build itself
// knows. A `go install github.com/romanidis/taskui@latest` gets no ldflags, so without
// this every such binary calls itself "dev" — including the ones built from a tag, which
// is the one case where the version is not in doubt.
func versionString() string {
	v, c, d := version, commit, date
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = strings.TrimPrefix(info.Main.Version, "v")
		}
		for _, s := range info.Settings {
			switch {
			case s.Key == "vcs.revision" && c == "none" && s.Value != "":
				c = s.Value
				if len(c) > 7 {
					c = c[:7]
				}
			case s.Key == "vcs.time" && d == "unknown" && s.Value != "":
				d = s.Value
			}
		}
	}
	return fmt.Sprintf("%s (commit %s, built %s)", v, c, d)
}

func init() {
	cobra.OnInitialize(initConfig)

	f := rootCmd.Flags()
	f.BoolVar(&opts.list, "list", false, "print the tasks and exit — useful for checking discovery without a terminal")
	f.StringVar(&opts.dump, "dump", "", "print a pivot fully expanded and exit: domain|verb|file|…")
	f.StringVar(&opts.graph, "graph", "", "print the execution graph reachable from a task and exit")
	f.StringVar(&opts.runTask, "run", "", "run a task headlessly and print the captured tree")
	f.StringVar(&opts.screenshot, "screenshot", "", "render one frame to stdout and exit, e.g. 90x30")
	f.StringVar(&opts.args, "args", "", "arguments for --run, split shell-style: --args '-- -p ingest'")
	f.BoolVar(&opts.last, "last", false, "open the most recent run for this project instead of the picker")
	f.StringVar(&opts.configPath, "config", "", "config file (default is "+theme.ConfigPath()+")")
	f.BoolVar(
		&opts.dumpConfig,
		"dump-config",
		false,
		"print an annotated config.yaml with every colour at its default, and exit",
	)
	f.StringVar(&opts.searchFor, "search", "", "search stored runs and exit")
	f.StringVar(&opts.timeline, "timeline", "", "print how one task has gone, run after run")
	f.StringVar(&opts.diffTask, "diff", "", "print what changed in one task since it last passed")
	f.BoolVar(&opts.quickfix, "quickfix", false,
		"print the last run's failures as file:line:col: message, for an editor's error list")
	f.BoolVar(&opts.asJSON, "json", false,
		"machine-readable form of --list, --run (newline-delimited events) or --timeline")
	f.StringVar(&opts.events, "events", "",
		"report what the runs are doing to this unix socket or file, as newline-delimited JSON")
	f.BoolVar(&opts.flaky, "flaky", false, "print tasks that both passed and failed at one commit")
	f.BoolVar(&opts.lint, "lint", false,
		"print the namespaces an aggregate task claims and does not reach, and exit")
	f.BoolVar(&opts.matrix, "matrix", false,
		"with --lint: print the whole aggregate-by-namespace table rather than only the gaps")
	f.StringVar(&opts.searchTask, "task", "", "narrow --search or --quickfix to one task's output")
	f.StringVar(&opts.since, "since", "", "narrow --search to runs newer than this: 90m, 2d, 3w")
	f.StringVar(
		&opts.keys,
		"keys",
		"",
		// No backquotes: pflag takes the first backquoted word for the value's placeholder,
		// which listed this flag as `--keys ^d`.
		"keys to play before a --screenshot, as if typed: ^d is a control chord, 0x09 0x0a 0x1b are ⇥ ⏎ esc, everything else is itself",
	)
	f.StringVar(&opts.themeName, "theme", "", "look to use — see --list-themes")
	f.BoolVar(
		&opts.colour,
		"colour",
		false,
		"keep the colour in a --screenshot, for looking at a theme rather than diffing it",
	)
	f.BoolVar(&opts.colour, "color", false, "alias for --colour")
	f.IntVar(&opts.phase, "phase", 0, "which animation frame a --screenshot is taken at, for themes that move")
	f.BoolVar(&opts.listThemes, "list-themes", false, "list every theme, built-in and yours, and exit")
	f.StringVar(
		&opts.dumpTheme,
		"dump-theme",
		"",
		"print a theme fully resolved, ready to edit into your own, and exit",
	)

	rootCmd.SetVersionTemplate("taskui {{.Version}}\n")

	// Last, because it needs every flag above to exist.
	registerCompletions()
}

// errReadingConfig is what went wrong reading the config file, kept for rootRun to report.
var errReadingConfig error

// initConfig points Viper at the config file and binds `TASKUI_*` to the same keys.
//
// A config that cannot be read at all is held rather than printed. Printed here, it went
// to stderr a moment before the TUI took the screen, so nobody saw it, and taskui ran on
// the defaults with exit status 0 while the manual promised 1.
func initConfig() {
	errReadingConfig = theme.Setup(v, opts.configPath)
}

func rootRun(cmd *cobra.Command, args []string) error {
	if opts.dumpConfig {
		fmt.Print(theme.DumpConfig())
		return nil
	}

	if opts.listThemes {
		return listThemes(cmd)
	}

	if opts.dumpTheme != "" {
		text, problems := theme.DumpTheme(opts.dumpTheme)
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "taskui:", p)
		}
		if len(problems) > 0 {
			return errors.New("that theme did not load cleanly")
		}
		fmt.Print(text)
		return nil
	}

	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		root = dir
	}

	// `--json` is a form the other flags can be printed in, not a command of its own.
	// Saying so beats launching the TUI at somebody who is piping this into a program.
	if err := formsOK(); err != nil {
		return err
	}

	// Unreadable is not the same as wrong. A bad value in a file that parsed is reported in
	// the status bar and the rest of the file still applies; a file that did not parse, or
	// is not there when `--config` named it, has nothing in it to apply.
	if errReadingConfig != nil {
		return fmt.Errorf("could not read the config: %w", errReadingConfig)
	}
	// The flag beats the config file, so a look can be tried without committing to it — by
	// standing in for `theme:` itself, so the file's own `colors:` still land on top of it as
	// they do on a theme the file names. Swapped in afterwards, it replaced them too.
	if opts.themeName != "" {
		v.Set("theme", opts.themeName)
	}
	config := theme.FromViper(v)
	// And the project gets the last word on the two things that are about its own task list
	// — which never includes what your terminal looks like or what your keys do.
	config = config.WithProject(theme.LoadProject(root))

	// Searching the archive reads stored runs, not the project — it must work from
	// anywhere, including a directory with no Taskfile in it.
	if opts.searchFor != "" {
		scope := search.Scope{Task: opts.searchTask}
		if opts.since != "" {
			ago, err := parseSince(opts.since)
			if err != nil {
				return err
			}
			scope.Since = time.Now().Add(-ago)
		}
		return searchStored(opts.searchFor, scope)
	}

	// Like --search, these read the archive rather than the project — but unlike it they
	// are scoped to one project, so they need the root and not a Taskfile.
	if handled, err := archiveCommand(cmd, root); handled {
		return err
	}

	// Everything from here on needs a task list, and there is no list without a file. Asked
	// before go-task rather than after it fails, so that the answer is taskui's own — see
	// starter.go for why go-task's is the wrong one to pass on.
	if task.FindUp(root) == "" {
		if !opensPicker() {
			return noTaskfileHere(root)
		}
		created, err := offerStarter(root, config)
		if err != nil || !created {
			return err
		}
	}

	tasks, err := task.Discover(root)
	if err != nil {
		return err
	}

	if handled, err := projectCommand(cmd, root, tasks, config); handled {
		return err
	}

	if opts.runTask != "" {
		// `--run` alone prints the captured tree; paired with `--screenshot` it renders
		// the actual run view, which is how the TUI gets verified without a terminal.
		if opts.screenshot == "" {
			if opts.asJSON {
				return streamRun(cmd.OutOrStdout(), root, opts.runTask, shellwords.Split(opts.args))
			}
			return runHeadless(root, opts.runTask, shellwords.Split(opts.args), opts.quickfix)
		}
		return screenshotRun(cmd.OutOrStdout(), app.New(tasks, root).WithConfig(config))
	}

	a := app.New(tasks, root).WithConfig(config)

	// A host — an editor showing this terminal — asked to be told what the runs do. It is
	// the same stream `--run --json` writes, minus the output lines: whoever is looking at
	// this terminal can already see those.
	if opts.events != "" {
		sink, err := events.Open(opts.events)
		if err != nil {
			return fmt.Errorf("could not report events to %s: %w", opts.events, err)
		}
		defer func() { _ = sink.Close() }()
		a.SendEventsTo(sink)
	}

	if opts.last && !a.OpenLastRun() {
		return fmt.Errorf("no stored runs for this project yet")
	}

	if opts.screenshot != "" {
		a.StartEnrichment()
		a.StartCoverage()
		a.AwaitDetails(detailGrace)
		// Its own grace, and a longer one: this walk is a process spawn per node and the
		// listing's budget was set for one call.
		a.AwaitCoverage(coverGrace)
		return screenshot(cmd.OutOrStdout(), a, opts.screenshot, opts.keys)
	}

	// Where each task is written, and whether it is up to date. Started here rather than in
	// New because it shells out, and only the interactive path has a use for the answer.
	a.StartEnrichment()

	// And notice when that file changes underneath us — including when `e` is what changed
	// it. Interactive only: a one-shot has nothing to keep up to date.
	a.WatchTaskfile()

	// Which aggregates run which namespace, and what every task calls. The interactive path
	// only, and for a stronger reason than the listing: this is a `task --summary` for every
	// task, which is the most expensive thing taskui asks go-task for. A one-shot that
	// renders a frame and exits would pay all of it for one screen.
	a.StartCoverage()

	// No options: the alternate screen is a property of the frame now, and App.View sets it.
	program := tea.NewProgram(a)
	_, err = program.Run()
	return err
}
