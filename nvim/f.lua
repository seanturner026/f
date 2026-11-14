-- f: nvim side of `f` feature workspaces: comments for the agent while editing.
-- Reviews are tuicr (`f review` in each repo tab). Reads <feature>/.context/feature.json.
--
--   :FComment [note]   comment on the current line / visual selection   <leader>pc
--   :FComments         list pending comments (quickfix)                 <leader>pl
--   :FSend             send all (and tuicr's) to the agent and clear     <leader>ps
--   :FClear            discard all
--
-- Works inside an `f` feature: <base>/.features/<feature>/<repo>/...
-- The agent is found via `herdr agent list`: the one whose cwd is the feature root.

-- f records who called it in its traces (f trace stats).
local F_ENV = { F_CALLER = "nvim" }

local M = {}

local ns = vim.api.nvim_create_namespace("f_comment")

-- Real file path for a buffer; nil for diff tools' old-side / scratch buffers
-- (diffview://, codediff://, "CodeDiff 1.2", ...).
local function buf_path(buf)
  local name = vim.api.nvim_buf_get_name(buf)
  if name == "" or name:match("^%a+://") or vim.bo[buf].buftype ~= "" then
    return nil
  end
  local path = vim.fn.fnamemodify(name, ":p")
  if vim.fn.filereadable(path) == 0 then
    return nil
  end
  return path
end

-- feature_info finds the feature containing path (or the cwd) by walking up
-- to its .context/feature.json, which f writes (feature.Info).
local function feature_info(path)
  local start = path and vim.fs.dirname(path) or vim.fn.getcwd()
  local ctx = vim.fs.find(".context", { upward = true, type = "directory", path = start })[1]
  if not ctx then
    return nil
  end
  local fd = io.open(ctx .. "/feature.json")
  if not fd then
    return nil
  end
  local ok, info = pcall(vim.json.decode, fd:read("*a"))
  fd:close()
  return ok and info or nil
end

-- feature root for a path (or the cwd).
local function feature_of(path)
  local info = feature_info(path)
  return info and info.root
end

-- f_cli runs `f args...` in the feature root (stdin optional).
local function f_cli(root, args, stdin)
  local res = vim.system(vim.list_extend({ "f" }, args), { cwd = root, stdin = stdin, text = true, env = F_ENV }):wait()
  return res.code == 0, res.stdout or "", vim.trim(res.stderr or "")
end

-- Pending comments, read straight from the file f writes (fast; f stays the
-- only writer).
local function load_comments(root)
  local fd = io.open(root .. "/.context/comments.json")
  if not fd then
    return {}
  end
  local ok, cs = pcall(vim.json.decode, fd:read("*a"))
  fd:close()
  return ok and type(cs) == "table" and cs or {}
end

-- Draw 💬 under each pending comment in buf.
local function render(buf)
  if not vim.api.nvim_buf_is_valid(buf) then
    return
  end
  local path = buf_path(buf)
  local info = path and feature_info(path)
  if not info then
    return
  end
  vim.api.nvim_buf_clear_namespace(buf, ns, 0, -1)
  local rel = path:sub(#info.root + 2)
  local count = vim.api.nvim_buf_line_count(buf)
  for _, c in ipairs(load_comments(info.root)) do
    if c.file == rel then
      pcall(vim.api.nvim_buf_set_extmark, buf, ns, math.min(c.line2, count) - 1, 0, {
        virt_lines = { { { "  💬 " .. c.note, "DiagnosticHint" } } },
      })
    end
  end
end

local function render_all()
  for _, buf in ipairs(vim.api.nvim_list_bufs()) do
    if vim.api.nvim_buf_is_loaded(buf) then
      render(buf)
    end
  end
end

local function add(opts)
  local buf = vim.api.nvim_get_current_buf()
  local path = buf_path(buf)
  if not path then
    return vim.notify("FComment: comment on the real file (right side of the diff)", vim.log.levels.WARN)
  end
  local root = feature_of(path)
  if not root then
    return vim.notify("FComment: not inside an f feature", vim.log.levels.WARN)
  end
  local l1, l2 = opts.line1, opts.line2

  local function save(note)
    if not note or vim.trim(note) == "" then
      return
    end
    local comment = vim.json.encode({
      file = path:sub(#root + 2),
      line1 = l1,
      line2 = l2,
      code = table.concat(vim.api.nvim_buf_get_lines(buf, l1 - 1, l2, false), "\n"),
      lang = vim.bo[buf].filetype,
      note = vim.trim(note),
    })
    local ok, _, err = f_cli(root, { "comment", "add" }, comment)
    if not ok then
      return vim.notify("FComment: " .. err, vim.log.levels.ERROR)
    end
    render(buf)
  end

  if opts.args ~= "" then
    save(opts.args)
  else
    vim.ui.input({ prompt = "comment for the agent: " }, save)
  end
end

local function current_root()
  return feature_of(buf_path(0) or nil) or feature_of(nil)
end

local function clear()
  local root = current_root()
  if root then
    f_cli(root, { "comment", "clear" })
    render_all()
  end
end

-- Send pending comments to this feature's agent (f finds it via Herdr).
local function send()
  local root = current_root()
  if not root then
    return vim.notify("FComment: not inside an f feature", vim.log.levels.WARN)
  end
  vim.system({ "f", "comment", "send" }, { cwd = root, text = true, env = F_ENV }, function(res)
    vim.schedule(function()
      local out = vim.trim((res.stdout or "") .. (res.stderr or ""))
      vim.notify("FComment: " .. out, res.code == 0 and vim.log.levels.INFO or vim.log.levels.ERROR)
      render_all()
    end)
  end)
end

local function list()
  local root = current_root()
  if not root then
    return
  end
  local items = {}
  for _, c in ipairs(load_comments(root)) do
    table.insert(items, { filename = root .. "/" .. c.file, lnum = c.line1, end_lnum = c.line2, text = c.note })
  end
  vim.fn.setqflist({}, " ", { title = "agent comments", items = items })
  vim.cmd(#items > 0 and "copen" or "cclose")
end

function M.setup()
  vim.api.nvim_create_user_command("FComment", add, { range = true, nargs = "*", desc = "Comment for the agent" })
  vim.api.nvim_create_user_command("FComments", list, { desc = "List pending comments" })
  vim.api.nvim_create_user_command("FSend", send, { desc = "Send comments to the agent" })
  vim.api.nvim_create_user_command("FClear", clear, { desc = "Discard pending comments" })

  -- Pending comments survive restarts (.context/comments.json): draw them on open.
  vim.api.nvim_create_autocmd("BufWinEnter", {
    group = vim.api.nvim_create_augroup("f_comments", { clear = true }),
    callback = function(ev)
      render(ev.buf)
    end,
  })

  vim.keymap.set("n", "<leader>pc", "<cmd>FComment<cr>", { desc = "Comment for the agent" })
  vim.keymap.set("x", "<leader>pc", ":FComment<cr>", { desc = "Comment for the agent" })
  vim.keymap.set("n", "<leader>ps", "<cmd>FSend<cr>", { desc = "Send comments to the agent" })
  vim.keymap.set("n", "<leader>pl", "<cmd>FComments<cr>", { desc = "List pending comments" })
end

return M
