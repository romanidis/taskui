-- lua/taskui/cli.lua
--
-- The binary, addressed as a program rather than as a terminal.
--
-- Two answers that come back once: the task listing and the quickfix list. A
-- run is not asked for here. It is started in the terminal, which is the
-- interface, and what it does comes back over the socket events.lua listens
-- on. Nothing here knows what a buffer is — this module is the wire, and
-- everything above it is what the wire is for.

local config = require("taskui.config")

local M = {}

--- The argv for one invocation: the configured binary, the project directory
--- as the positional argument, then whatever was asked for.
---@param args string[]
---@param dir string|nil The project, when it is not the configured one.
---@return string[]
local function argv(args, dir)
  local cmd = { config.options.binary, dir or config.project() }
  vim.list_extend(cmd, args)
  return cmd
end

--- Reports whether the binary can be found at all, so the failure is "install
--- taskui" rather than a stack trace from vim.system.
---@return boolean
function M.available()
  return vim.fn.executable(config.options.binary) == 1
end

--- Runs the binary and hands back its whole output, on the main loop.
---@param args string[]
---@param on_done fun(code: integer, stdout: string, stderr: string)
---@param dir string|nil
local function collect(args, on_done, dir)
  vim.system(argv(args, dir), { text = true }, function(res)
    vim.schedule(function()
      on_done(res.code, res.stdout or "", res.stderr or "")
    end)
  end)
end

--- Fetches the task listing.
---@param on_done fun(tasks: table[]|nil, err: string|nil)
function M.list(on_done)
  collect({ "--list", "--json" }, function(code, stdout, stderr)
    if code ~= 0 and stdout == "" then
      on_done(nil, vim.trim(stderr) ~= "" and vim.trim(stderr) or ("taskui exited " .. code))
      return
    end
    local ok, page = pcall(vim.json.decode, stdout)
    if not ok or type(page) ~= "table" or type(page.tasks) ~= "table" then
      on_done(nil, "could not read the task listing")
      return
    end
    on_done(page.tasks, nil)
  end)
end

--- Fetches the quickfix entries for the last stored run, already parsed into
--- the shape setqflist takes.
---
--- The binary resolves the paths, which is the whole point of asking it rather
--- than parsing the output here: `order_test.go:88` is relative to a directory
--- only the run knows, and Neovim would resolve it against its own.
---@param task string|nil Narrow to one task's output.
---@param on_done fun(items: table[], err: string|nil)
---@param dir string|nil The project whose archive to read, when it is not the configured one.
function M.quickfix(task, on_done, dir)
  local args = { "--quickfix" }
  if task and task ~= "" then
    vim.list_extend(args, { "--task", task })
  end
  collect(args, function(code, stdout, stderr)
    if code ~= 0 and stdout == "" then
      on_done({}, vim.trim(stderr) ~= "" and vim.trim(stderr) or ("taskui exited " .. code))
      return
    end
    local items = {}
    for line in vim.gsplit(stdout, "\n", { trimempty = true }) do
      local file, lnum, col, text = line:match("^(.-):(%d+):(%d+): (.*)$")
      if file then
        table.insert(items, {
          filename = file,
          lnum = tonumber(lnum),
          col = tonumber(col),
          text = text,
          type = "E",
        })
      end
    end
    on_done(items, nil)
  end, dir)
end

return M
