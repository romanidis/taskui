-- Configuration for `task lint:lua`. The plugin runs inside Neovim, whose API
-- arrives as the vim global rather than through require. Writable, because
-- that is how much of the API is used: `vim.g`, `vim.wo` and `vim.env` are set
-- rather than called, and the tests replace `vim.notify` to hear what it says.
std = "luajit"
globals = { "vim" }
