-- pi_comment: collect review comments in nvim, send them to this feature's pi.
--
--   :PiComment [note]   comment on the current line / visual selection
--   :PiComments         list pending comments (quickfix)
--   :PiSend             send all to pi (via Herdr) and clear
--   :PiClear            discard all
--
-- Works inside an `f` feature: <base>/.features/<feature>/<repo>/...
-- pi is found via `herdr agent list`: the pi agent whose cwd is the feature root.

local M = {}

local ns = vim.api.nvim_create_namespace("pi_comment")
local comments = {} -- { file, l1, l2, code, ft, note, buf, mark }

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

-- feature root and name for a path (or the cwd).
local function feature_of(path)
  local p = path or (vim.fn.getcwd() .. "/")
  local root, name = p:match("^(.-/%.features/([^/]+))/")
  if not root then
    root, name = p:match("^(.-/%.features/([^/]+))$")
  end
  return root, name
end

-- Herdr target for the feature's pi: the pane of the pi agent running in the
-- feature root (names get dropped when pi restarts, so find it by cwd).
local function pi_target(root)
  local res = vim.system({ "herdr", "agent", "list" }, { text = true }):wait()
  if res.code ~= 0 then
    return nil, vim.trim(res.stderr or "herdr agent list failed")
  end
  local ok, data = pcall(vim.json.decode, res.stdout)
  local agents = ok and data.result and data.result.agents or {}
  for _, a in ipairs(agents) do
    if a.agent == "pi" and (a.cwd == root or a.foreground_cwd == root) then
      return a.pane_id
    end
  end
  return nil, "no pi running in " .. root
end

local function add(opts)
  local buf = vim.api.nvim_get_current_buf()
  local path = buf_path(buf)
  if not path then
    return vim.notify("PiComment: comment on the real file (right side of diffview)", vim.log.levels.WARN)
  end
  local root = feature_of(path)
  if not root then
    return vim.notify("PiComment: not inside an f feature", vim.log.levels.WARN)
  end
  local l1, l2 = opts.line1, opts.line2

  local function save(note)
    if not note or vim.trim(note) == "" then
      return
    end
    local mark = vim.api.nvim_buf_set_extmark(buf, ns, l2 - 1, 0, {
      virt_lines = { { { "  💬 " .. note, "DiagnosticHint" } } },
    })
    table.insert(comments, {
      file = path:sub(#root + 2),
      l1 = l1,
      l2 = l2,
      code = table.concat(vim.api.nvim_buf_get_lines(buf, l1 - 1, l2, false), "\n"),
      ft = vim.bo[buf].filetype,
      note = vim.trim(note),
      buf = buf,
      mark = mark,
    })
  end

  if opts.args ~= "" then
    save(opts.args)
  else
    vim.ui.input({ prompt = "comment for pi: " }, save)
  end
end

local function clear()
  for _, c in ipairs(comments) do
    pcall(vim.api.nvim_buf_del_extmark, c.buf, ns, c.mark)
  end
  comments = {}
end

local function message()
  local out = { "Review comments:", "" }
  for i, c in ipairs(comments) do
    local range = c.l1 == c.l2 and tostring(c.l1) or (c.l1 .. "-" .. c.l2)
    vim.list_extend(out, {
      ("%d. %s:%s"):format(i, c.file, range),
      "```" .. c.ft,
      c.code,
      "```",
      "→ " .. c.note,
      "",
    })
  end
  return table.concat(out, "\n")
end

local function send()
  if #comments == 0 then
    return vim.notify("PiComment: nothing to send")
  end
  local root = feature_of(buf_path(0) or nil) or feature_of(nil)
  if not root then
    return vim.notify("PiComment: not inside an f feature", vim.log.levels.WARN)
  end
  local msg, n = message(), #comments
  local function fail(why)
    vim.fn.setreg("+", msg)
    vim.notify("PiComment: send failed (copied to clipboard): " .. why, vim.log.levels.ERROR)
  end
  local target, err = pi_target(root)
  if not target then
    return fail(err)
  end
  vim.system({ "herdr", "agent", "prompt", target, msg }, { text = true }, function(res)
    vim.schedule(function()
      if res.code == 0 then
        clear()
        vim.notify(("PiComment: sent %d to pi"):format(n))
      else
        fail(vim.trim(res.stderr or ""))
      end
    end)
  end)
end

local function list()
  local items = {}
  for _, c in ipairs(comments) do
    table.insert(items, { bufnr = c.buf, lnum = c.l1, end_lnum = c.l2, text = c.note })
  end
  vim.fn.setqflist({}, " ", { title = "pi comments", items = items })
  vim.cmd(#items > 0 and "copen" or "cclose")
end

function M.setup()
  vim.api.nvim_create_user_command("PiComment", add, { range = true, nargs = "*", desc = "Comment for pi" })
  vim.api.nvim_create_user_command("PiComments", list, { desc = "List pending pi comments" })
  vim.api.nvim_create_user_command("PiSend", send, { desc = "Send pi comments" })
  vim.api.nvim_create_user_command("PiClear", clear, { desc = "Discard pi comments" })

  vim.keymap.set("n", "<leader>pc", "<cmd>PiComment<cr>", { desc = "[P]i [C]omment" })
  vim.keymap.set("x", "<leader>pc", ":PiComment<cr>", { desc = "[P]i [C]omment" })
  vim.keymap.set("n", "<leader>ps", "<cmd>PiSend<cr>", { desc = "[P]i [S]end comments" })
  vim.keymap.set("n", "<leader>pl", "<cmd>PiComments<cr>", { desc = "[P]i [L]ist comments" })
end

M._message = message -- for tests
M._pi_target = pi_target

return M
