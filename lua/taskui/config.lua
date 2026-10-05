-- lua/taskui/config.lua
--
-- Configuration defaults and merging. One flat table: the plugin is a front
-- end over one binary, so there is nothing here that needs a section of its
-- own — and a nested option is one the user has to remember the path to.

local M = {}

---@class Taskui.Config
---@field binary string Executable name or absolute path.
---@field project string|nil Directory to open taskui in. Nil means the cwd.
---@field position "float"|"left"|"right"|"top"|"bottom"|"tab" Where the terminal opens.
---@field width integer Columns for a left or right split.
---@field height integer Rows for a top or bottom split.
---@field args string[] Extra arguments for the binary, e.g. { "--theme", "y2k" }.
---@field quickfix "on_failure"|"always"|"never" When a finished run fills the quickfix list.
---@field open_quickfix boolean Open the quickfix window when it is filled from a failure.
---@field notify boolean Say how a run went — for when the terminal is not the window you are in.
---@field keys table<string, string|false> The two keys the host owns inside the terminal; the rest belong to taskui.
---@field search_key string taskui's own search key, which `run()` types to reach a task. Set it if your config moves it.
M.defaults = {
  binary = "taskui",
  project = nil,
  -- A float, because taskui is a whole interface rather than a sidebar: it has
  -- its own header, footer and columns, and squeezing those into sixty columns
  -- beside a file is how you end up reimplementing it.
  position = "float",
  width = 80,
  height = 20,
  args = {},
  -- The quickfix list is the largest daily return, and a failed run is exactly
  -- the moment it is wanted — but filling it on a run that passed would clear
  -- whatever you were working through.
  quickfix = "on_failure",
  open_quickfix = false,
  notify = true,
  -- taskui's default search key. This is the one taskui binding the plugin has
  -- to know, because `run()` reaches a task by typing it into the terminal
  -- rather than through a socket, and a keystroke is only as stable as the
  -- keymap it goes to. It is here so that a `keys: search:` line in a taskui
  -- config has somewhere to be answered — without it, rebinding search silently
  -- stops `:TaskUI run` working, with the keystroke landing on whatever took the
  -- key.
  search_key = "/",
  keys = {
    -- The two keys the host owns, bound *inside* the terminal buffer only, so
    -- they cost nothing anywhere else. Everything else in there is taskui's:
    -- intercepting its keys would be taking them from the thing you asked for.
    --
    -- toggle is the same key you bound `:TaskUI` to. It has to be repeated
    -- here because a focused terminal is in terminal mode, where a normal-mode
    -- mapping never fires — the keystroke goes to the program, which is the
    -- whole point of a terminal and exactly wrong for the key that closes it.
    toggle = "<A-t>",
    close = "<C-q>",
  },
}

M.options = vim.deepcopy(M.defaults)

--- Merges user options over the defaults.
---@param opts Taskui.Config|nil
function M.setup(opts)
  opts = opts or {}
  -- taskui folded its jump key into the search prompt, where ⇥ finds rather
  -- than filters, so the old option has nothing left to name. Said rather than
  -- ignored: a setting that silently stopped mattering looks like one that works.
  if opts.jump_key ~= nil then
    vim.notify(
      "taskui: jump_key is now search_key — taskui's `/` prompt finds a task with ⇥",
      vim.log.levels.WARN
    )
    opts = vim.deepcopy(opts)
    opts.jump_key = nil
  end
  M.options = vim.tbl_deep_extend("force", vim.deepcopy(M.defaults), opts)
end

--- The project directory a command should run in: what was configured, or
--- wherever Neovim is, which is what every other tool in the editor assumes.
---@return string
function M.project()
  return M.options.project or vim.fn.getcwd()
end

return M
