-- lua/taskui/init.lua
--
-- The public surface: setup, the verbs `:TaskUI` dispatches to, and a
-- statusline component.
--
-- The plugin hosts the terminal UI rather than reimplementing it. Everything
-- you look at is taskui's own — the pivots, the folds, the peek windows, the
-- archive — and what Neovim adds is what a terminal cannot do from inside
-- itself: land on the failing line, keep the failure in the statusline while
-- you fix it, and open files in the editor that is already open.

local cli = require("taskui.cli")
local config = require("taskui.config")

-- How long to leave an esc alone before sending what follows it. See M.run.
local escGap = 200
local events = require("taskui.events")
local term = require("taskui.term")

local M = {}

--- Merges user options over the defaults.
---@param opts Taskui.Config|nil
function M.setup(opts)
  config.setup(opts)
end

--- Opens the terminal, or focuses it if it is already up.
function M.open()
  term.open()
end

--- Hides it. Runs carry on: closing the window is not stopping anything.
function M.close()
  term.close()
end

function M.toggle()
  term.toggle()
end

--- Stops taskui and every run it owns.
function M.stop()
  term.stop()
end

--- Runs a task by name, by typing it into the terminal the way you would: the
--- filter, the name, enter. The tool decides what that means — a task already
--- running is shown rather than started twice — which is the point of driving
--- the real thing rather than a second implementation of it.
---@param name string
function M.run(name)
  if not name or name == "" then
    vim.notify("taskui: which task?", vim.log.levels.WARN)
    return
  end
  local started = term.job ~= nil
  term.open()
  vim.defer_fn(function()
    -- Find rather than filter: the search prompt opens filtering in the picker,
    -- and ⇥ switches it to finding, which lands the cursor *on* the task the
    -- name says, opening whatever folds hid it — where a filter narrows the list
    -- and leaves the cursor on its first match, which for `build` beside
    -- `app:build` is the wrong one.
    --
    -- The key comes from config so a taskui `keys: search:` has somewhere to be
    -- answered; it is the one taskui binding this plugin has to know, because
    -- reaching a task means typing at the terminal.
    --
    -- esc first, so this means the same thing whatever was on screen.
    -- esc goes on its own, and the rest only after the terminal has given up
    -- waiting for what might follow it. A terminal encodes Alt+x as esc then x,
    -- so an esc written in the same breath as the next key arrives as that
    -- chord rather than as two presses — the search key is swallowed and the task
    -- name is typed at the picker instead, where `e` opens an editor. Measured
    -- against the real binary through a pty: 50ms still merges, 80ms does not,
    -- so this leaves a wide margin over a threshold that is not ours to pin.
    --
    -- It went unnoticed for as long as the jump key was `t`, because the merge
    -- ate the esc and the first letter of `test` is a `t` — the name typed its
    -- own way into the prompt.
    term.send("\27")
    vim.defer_fn(function()
      term.send(config.options.search_key .. "\t" .. name .. "\r\r")
    end, escGap)
  end, started and 80 or 500)
end

--- Opens a task's own definition in the Taskfile.
---@param name string|nil
function M.edit(name)
  cli.list(function(tasks, err)
    if err then
      vim.notify("taskui: " .. err, vim.log.levels.WARN)
      return
    end
    for _, task in ipairs(tasks) do
      if task.name == name and task.taskfile then
        term.open_file(task.taskfile, task.line, 1)
        return
      end
    end
    vim.notify(("taskui: no location for %s"):format(name or "that task"), vim.log.levels.WARN)
  end)
end

--- Fills the quickfix list from the last stored run and opens it.
---@param name string|nil Narrow to one task's output.
function M.quickfix(name)
  cli.quickfix(name ~= "" and name or nil, function(items, err)
    if err then
      vim.notify("taskui: " .. err, vim.log.levels.WARN)
      return
    end
    if #items == 0 then
      vim.notify("taskui: nothing to put in the quickfix list")
      return
    end
    local title = "taskui" .. (name and (" · " .. name) or "")
    vim.fn.setqflist({}, " ", { title = title, items = items })
    vim.cmd("copen")
  end)
end

--- Every task name, for command completion, by project. Fetched in the
--- background, so the first `<Tab>` is empty rather than slow — a completion
--- that blocks the editor for four seconds is worse than one that arrives a
--- moment later.
---@type table<string, string[]>
local names = {}

--- The projects whose listing is on its way.
---@type table<string, boolean>
local listing = {}

--- Answered from the last listing, and asked again in the background every time,
--- so a `<Tab>` after editing the Taskfile sees the edit: filled once, the list
--- went stale for the rest of the session. One listing at a time, and each
--- replaces the last — a `<Tab>` pressed while the first was still on its way
--- started another, and both appended to one list, so every name came back
--- twice.
---@return string[]
function M.task_names()
  local project = config.project()
  if not listing[project] and cli.available() then
    listing[project] = true
    cli.list(function(tasks)
      listing[project] = nil
      if not tasks then
        return
      end
      local fresh = {}
      for _, task in ipairs(tasks) do
        table.insert(fresh, task.name)
      end
      table.sort(fresh)
      names[project] = fresh
    end)
  end
  return names[project] or {}
end

--- A statusline component: what is running, or what just failed.
---
--- Short by design — `✗ backend:test 7.1s` — because a statusline is read out
--- of the corner of the eye while you work on the thing that failed. It is fed
--- by the events, so it keeps saying so with the terminal closed.
---@return string
function M.status()
  local run = events.newest
  for _, candidate in pairs(events.runs) do
    if candidate.status == "running" then
      run = candidate
      break
    end
  end
  if not run then
    return ""
  end
  local glyph = "▶"
  if run.status == "ok" then
    glyph = "✓"
  elseif run.status == "failed" then
    glyph = "✗"
  end
  local took = events.duration(run.duration_ms):gsub("^ in", "")
  return vim.trim(("%s %s%s"):format(glyph, run.root, took))
end

return M
