-- pi_comment: nvim helpers for `f` features.
--
--   :PiComment [note]   comment on the current line / visual selection   <leader>pc
--   :PiComments         list pending comments (quickfix)                 <leader>pl
--   :PiSend             send all to pi (via Herdr) and clear             <leader>ps
--   :PiClear            discard all
--   :PiReview [repo]    live CodeDiff for one repo (or a tab per repo)
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

-- Repos (git worktrees) in the feature that contains the cwd.
local function feature_repos()
  local root = feature_of(nil)
  if not root then
    return nil, nil
  end
  local repos = {}
  for name, kind in vim.fs.dir(root) do
    if kind == "directory" and vim.uv.fs_stat(root .. "/" .. name .. "/.git") then
      table.insert(repos, name)
    end
  end
  table.sort(repos)
  return root, repos
end

-- Tab label: the repo name for :PiReview tabs, else the focused buffer's name.
local function tab_label(tab)
  local ok, repo = pcall(vim.api.nvim_tabpage_get_var, tab, "pi_repo")
  if ok then
    return repo
  end
  local name = vim.api.nvim_buf_get_name(vim.api.nvim_win_get_buf(vim.api.nvim_tabpage_get_win(tab)))
  return name == "" and "[No Name]" or vim.fs.basename(name)
end

function M.tabline()
  local parts, current = {}, vim.api.nvim_get_current_tabpage()
  for i, tab in ipairs(vim.api.nvim_list_tabpages()) do
    local hl = tab == current and "%#TabLineSel#" or "%#TabLine#"
    table.insert(parts, ("%s%%%dT %s "):format(hl, i, tab_label(tab)))
  end
  return table.concat(parts) .. "%#TabLineFill#%T"
end

-- One CodeDiff tab per repo (feature changes vs origin's default branch),
-- labelled with the repo name; the empty starting tab is closed once they're up.
-- :PiReview [repo]: with a repo (as in f's Herdr repo tabs), just that repo;
-- without, every repo in the feature, one nvim tab each.
local function review(opts)
  local root, repos = feature_repos()
  if not root then
    return vim.notify("PiReview: not inside an f feature", vim.log.levels.WARN)
  end
  local only = opts and opts.args or ""
  if only ~= "" then
    if not vim.tbl_contains(repos, only) then
      return vim.notify("PiReview: no repo " .. only .. " in this feature", vim.log.levels.WARN)
    end
    repos = { only }
  else
    vim.o.tabline = "%!v:lua.require'pi_comment'.tabline()"
    vim.o.showtabline = 2
  end
  local first = vim.api.nvim_get_current_tabpage()
  -- CodeDiff opens its tab asynchronously, and concurrent launches get mixed
  -- up, so open one repo at a time and wait for its tab before the next.
  local function open(i)
    local repo = repos[i]
    if not repo then
      local buf = vim.api.nvim_win_get_buf(vim.api.nvim_tabpage_get_win(first))
      if #vim.api.nvim_list_tabpages() > 1 and vim.api.nvim_buf_get_name(buf) == "" and not vim.bo[buf].modified then
        vim.cmd("tabclose " .. vim.api.nvim_tabpage_get_number(first))
      end
      return
    end
    local before = {}
    for _, t in ipairs(vim.api.nvim_list_tabpages()) do
      before[t] = true
    end
    -- origin/HEAD... = everything since the branch left origin's default
    -- branch (committed + uncommitted), still live-updating.
    vim.cmd("CodeDiff --repo " .. vim.fn.fnameescape(root .. "/" .. repo) .. " origin/HEAD...")
    local waited = 0
    local function poll()
      for _, t in ipairs(vim.api.nvim_list_tabpages()) do
        if not before[t] then
          vim.api.nvim_tabpage_set_var(t, "pi_repo", repo)
          vim.cmd("redrawtabline")
          return open(i + 1)
        end
      end
      waited = waited + 50
      if waited >= 10000 then
        return open(i + 1) -- no changes / no tab for this repo
      end
      vim.defer_fn(poll, 50)
    end
    poll()
  end
  open(1)
end

M._repos = feature_repos -- for tests

function M.setup()
  vim.api.nvim_create_user_command("PiComment", add, { range = true, nargs = "*", desc = "Comment for pi" })
  vim.api.nvim_create_user_command("PiComments", list, { desc = "List pending pi comments" })
  vim.api.nvim_create_user_command("PiSend", send, { desc = "Send pi comments" })
  vim.api.nvim_create_user_command("PiClear", clear, { desc = "Discard pi comments" })
  vim.api.nvim_create_user_command("PiReview", review, {
    nargs = "?",
    desc = "Live CodeDiff for a feature repo (or all)",
    complete = function()
      return select(2, feature_repos()) or {}
    end,
  })

  vim.keymap.set("n", "<leader>pc", "<cmd>PiComment<cr>", { desc = "[P]i [C]omment" })
  vim.keymap.set("x", "<leader>pc", ":PiComment<cr>", { desc = "[P]i [C]omment" })
  vim.keymap.set("n", "<leader>ps", "<cmd>PiSend<cr>", { desc = "[P]i [S]end comments" })
  vim.keymap.set("n", "<leader>pl", "<cmd>PiComments<cr>", { desc = "[P]i [L]ist comments" })
end

M._message = message -- for tests
M._pi_target = pi_target

return M
