package task

import (
	"encoding/json"
	"maps"
	"os"

	"go.yaml.in/yaml/v3"
)

// Project is what a run needs to know about the project beyond the task it was asked for.
// All of it comes from one `task --list-all --json` call, since that is a process spawn
// either way.
type Project struct {
	// Names resolves every spelling go-task accepts to the one taskui lists.
	Names Names
	// Env is the static `env:` the Taskfiles declare at their top level.
	//
	// It exists because `--summary`, which the redactor otherwise harvests from, prints a
	// task's own `env:`, its `dotenv:` values and the Taskfile's `sh:` entries, but never
	// the Taskfile's plain `env:` values. That is the most natural place for a token that
	// every task needs, and every command that echoes it would print it unmasked. Measured
	// on go-task 3.53.1.
	//
	// Only string values are kept. A `sh:` entry is a command, not the value it produces,
	// and a `{{.VAR}}` template is left as written: neither is the secret itself.
	Env map[string]string
	// Files is the Taskfile each task is written in, by the name Names gives it. Its
	// directory is where the task runs unless it says otherwise, which is what a path it
	// prints is relative to.
	Files map[string]string
}

// Names maps a task's name and aliases, as go-task spells them, to the name Discover lists
// it by.
//
// One task has three spellings in play during a run. The listing says `dev` for what
// go-task calls `dev:default`; `--summary` repeats whatever the caller wrote, so an edge can
// say `b` for `build`; and output is tagged with the real name. Everything a run keys by
// task has to agree on one of them, or `build` prints under a row nobody drew.
type Names map[string]string

// Canonical is name as Discover lists it, or name itself for a task the listing does not
// know — an `internal:` one, which is never listed and so never renamed.
func (n Names) Canonical(name string) string {
	if c, ok := n[name]; ok {
		return c
	}
	return name
}

// ReadProject reads the project's names, Taskfile env and where each task is written. Best
// effort throughout: a
// listing or a Taskfile that cannot be read contributes nothing, the same way an
// unresolvable graph leaves a run without nesting rather than stopping it.
func ReadProject(dir string) Project {
	// `--no-status` because computing `up_to_date` is what makes this listing slow, and
	// nothing here reads it.
	out, err := Ask(dir, "--list-all", "--json", "--no-status").Output()
	if err != nil {
		return Project{}
	}
	var listing struct {
		Location string `json:"location"`
		Tasks    []struct {
			Name     string   `json:"name"`
			Aliases  []string `json:"aliases"`
			Location struct {
				Taskfile string `json:"taskfile"`
			} `json:"location"`
		} `json:"tasks"`
	}
	if json.Unmarshal(out, &listing) != nil {
		return Project{}
	}

	listed := make([]Task, 0, len(listing.Tasks))
	files := []string{listing.Location}
	seen := map[string]bool{listing.Location: true}
	for _, t := range listing.Tasks {
		listed = append(listed, Task{Name: t.Name, Aliases: t.Aliases})
		if f := t.Location.Taskfile; !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	names := namesOf(listed)

	env := map[string]string{}
	for _, f := range files {
		maps.Copy(env, topLevelEnv(f))
	}
	defined := map[string]string{}
	for _, t := range listing.Tasks {
		if name := names.Canonical(t.Name); defined[name] == "" {
			defined[name] = t.Location.Taskfile
		}
	}
	return Project{Names: names, Env: env, Files: defined}
}

// namesOf builds the resolver from go-task's own listing.
//
// A name beats an alias, because that is how go-task resolves them: with a root-level
// `dev` beside an included `dev:default` aliased `dev`, `task dev` runs the root one. And
// the `dev:default` that loses that collision keeps its own name rather than taking one
// that already belongs to another task — Discover leaves it out, and sending its output to
// the root `dev` would put two tasks' lines under one row.
func namesOf(listed []Task) Names {
	taken := map[string]bool{}
	for _, t := range listed {
		taken[t.Name] = true
	}
	n := Names{}
	for _, t := range listed {
		named, ok := canonical(t)
		if !ok {
			continue
		}
		if named.Name != t.Name && taken[named.Name] {
			continue
		}
		for _, a := range named.Aliases {
			if !taken[a] {
				n[a] = named.Name
			}
		}
		n[t.Name] = named.Name
	}
	return n
}

func topLevelEnv(file string) map[string]string {
	blob, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var doc struct {
		Env map[string]any `yaml:"env"`
	}
	if yaml.Unmarshal(blob, &doc) != nil {
		return nil
	}
	env := map[string]string{}
	for k, v := range doc.Env {
		if s, ok := v.(string); ok {
			env[k] = s
		}
	}
	return env
}
