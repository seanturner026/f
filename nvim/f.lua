-- f: nvim side of `f` feature workspaces (review tabs, comments for the agent).
-- Reads <feature>/.context/feature.json, which the f CLI owns.
--
--   :FComment [note]   comment on the current line / visual selection   <leader>pc
--   :FComments         list pending comments (quickfix)                 <leader>pl
--   :FSend             send all to the agent (via Herdr) and clear      <leader>ps
--   :FClear            discard all
--   :FReview [repo]    live CodeDiff for one repo (or a tab per repo)
--   :FReviewToggle     in a review tab: whole feature ↔ since the agent's last run <leader>pr
--   :FReview pr        in a review tab: pick the whole feature or one stacked PR <leader>pp
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

-- Root and repos of the feature that contains the cwd.
local function feature_repos()
  local info = feature_info(nil)
  if not info then
    return nil, nil
  end
  return info.root, info.repos or {}
end

-- Tab label: the repo name for :FReview tabs, else the focused buffer's name.
local function tab_label(tab)
  local ok, repo = pcall(vim.api.nvim_tabpage_get_var, tab, "f_repo")
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

-- What a review tab compares against.
local FEATURE = "origin/HEAD..." -- everything since the branch left origin's default branch
local LAST_RUN = "refs/worktree/pi-last" -- the code before the agent's last run (f checkpoint)

-- open_codediff opens CodeDiff for each repo against rev, one at a time
-- (CodeDiff opens its tab asynchronously, and concurrent launches get mixed
-- up), tagging each new tab with its repo and rev; then calls done().
local function open_codediff(root, repos, rev, done)
  local function open(i)
    local repo = repos[i]
    if not repo then
      return done and done()
    end
    local before = {}
    for _, t in ipairs(vim.api.nvim_list_tabpages()) do
      before[t] = true
    end
    vim.cmd("CodeDiff --repo " .. vim.fn.fnameescape(root .. "/" .. repo) .. " " .. rev)
    local waited = 0
    local function poll()
      for _, t in ipairs(vim.api.nvim_list_tabpages()) do
        if not before[t] then
          vim.api.nvim_tabpage_set_var(t, "f_repo", repo)
          vim.api.nvim_tabpage_set_var(t, "f_rev", rev)
          -- CodeDiff fixes the merge-base when it opens; remember it so a
          -- rebase (or a sync reset) can be noticed and the tab reopened.
          local mb = vim.system({ "git", "-C", root .. "/" .. repo, "merge-base", "origin/HEAD", "HEAD" }, { text = true }):wait()
          if mb.code == 0 then
            vim.api.nvim_tabpage_set_var(t, "f_base", vim.trim(mb.stdout))
          end
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

-- Close tab if it's an untouched empty one (nvim's starting tab).
local function close_if_empty(tab)
  if not vim.api.nvim_tabpage_is_valid(tab) or #vim.api.nvim_list_tabpages() < 2 then
    return
  end
  local buf = vim.api.nvim_win_get_buf(vim.api.nvim_tabpage_get_win(tab))
  if vim.api.nvim_buf_get_name(buf) == "" and not vim.bo[buf].modified then
    vim.cmd("tabclose " .. vim.api.nvim_tabpage_get_number(tab))
  end
end

local function git(dir, ...)
  return vim.system({ "git", "-C", dir, ... }, { text = true }):wait()
end

-- has_changes: anything for CodeDiff to show vs FEATURE (committed, uncommitted
-- or untracked). Unsure (e.g. no origin/HEAD) counts as yes, so CodeDiff reports it.
local function has_changes(dir)
  local base = git(dir, "merge-base", "origin/HEAD", "HEAD")
  if base.code ~= 0 then
    return true
  end
  if git(dir, "diff", "--quiet", vim.trim(base.stdout)).code ~= 0 then
    return true
  end
  return vim.trim(git(dir, "ls-files", "--others", "--exclude-standard").stdout or "") ~= ""
end

local WAIT_MS = 3000 -- how often a waiting review tab checks for changes

local function is_worktree(dir)
  return vim.uv.fs_stat(dir .. "/.git") ~= nil
end

-- wait_for_changes shows a placeholder in the current tab until repo has
-- changes, then opens its CodeDiff and closes the placeholder. A repo f is
-- still setting up (f setup) has no worktree yet: it waits for that first.
local function wait_for_changes(root, repo)
  local dir = root .. "/" .. repo
  vim.cmd("enew")
  local buf, tab = vim.api.nvim_get_current_buf(), vim.api.nvim_get_current_tabpage()
  vim.bo[buf].buftype, vim.bo[buf].bufhidden, vim.bo[buf].swapfile = "nofile", "wipe", false
  local function show()
    local text
    if is_worktree(dir) then
      local upstream = vim.trim(git(dir, "rev-parse", "--abbrev-ref", "origin/HEAD").stdout or "")
      text = repo .. ": no changes vs " .. (upstream ~= "" and upstream or "origin/HEAD") .. " yet."
    else
      text = repo .. ": setting up its worktree (f is fetching)."
    end
    vim.bo[buf].modifiable = true
    vim.api.nvim_buf_set_lines(buf, 0, -1, false, { "", "  " .. text, "  This tab switches to CodeDiff once there are changes." })
    vim.bo[buf].modifiable, vim.bo[buf].modified = false, false
  end
  show()
  local ready = is_worktree(dir)
  vim.api.nvim_tabpage_set_var(tab, "f_repo", repo)
  vim.cmd("redrawtabline")

  local timer = vim.uv.new_timer()
  timer:start(WAIT_MS, WAIT_MS, vim.schedule_wrap(function()
    if not vim.api.nvim_tabpage_is_valid(tab) or not vim.api.nvim_buf_is_valid(buf) then
      return timer:close()
    end
    if not is_worktree(dir) then
      return
    end
    if not ready then
      ready = true
      show()
    end
    if not has_changes(dir) then
      return
    end
    timer:close()
    open_codediff(root, { repo }, FEATURE, function()
      if vim.api.nvim_tabpage_is_valid(tab) and #vim.api.nvim_list_tabpages() > 1 then
        vim.cmd("tabclose " .. vim.api.nvim_tabpage_get_number(tab))
      end
    end)
  end))
end

-- watch_bases reopens a whole-feature review tab when its merge-base moves
-- (a rebase onto newer main, f sync): CodeDiff computed the base once, so it
-- would otherwise show everything main gained since as changes.
local base_timer, reopening = nil, false
local function watch_bases()
  if base_timer then
    return
  end
  local function var(t, name)
    local ok, v = pcall(vim.api.nvim_tabpage_get_var, t, name)
    return ok and v or nil
  end
  base_timer = vim.uv.new_timer()
  base_timer:start(WAIT_MS, WAIT_MS, vim.schedule_wrap(function()
    local root = feature_of(nil)
    if reopening or not root then
      return
    end
    for _, t in ipairs(vim.api.nvim_list_tabpages()) do
      local repo, base = var(t, "f_repo"), var(t, "f_base")
      if repo and base and var(t, "f_rev") == FEATURE then
        local dir = root .. "/" .. repo
        local mb = git(dir, "merge-base", "origin/HEAD", "HEAD")
        local now = mb.code == 0 and vim.trim(mb.stdout) or nil
        if now and now ~= base then
          reopening = true
          vim.api.nvim_tabpage_set_var(t, "f_base", now)
          local function close_old()
            if vim.api.nvim_tabpage_is_valid(t) and #vim.api.nvim_list_tabpages() > 1 then
              vim.cmd("tabclose " .. vim.api.nvim_tabpage_get_number(t))
            end
            reopening = false
          end
          if has_changes(dir) then
            open_codediff(root, { repo }, FEATURE, close_old)
          else -- e.g. reset to main after a merge: wait for the next change
            vim.cmd("tabnew")
            wait_for_changes(root, repo)
            close_old()
          end
          return -- one at a time
        end
      end
    end
  end))
end

local review_pick -- below; :FReview pr opens it

-- :FReview [repo]: with a repo (as in f's Herdr repo tabs), just that repo
-- (waiting for changes if there are none yet); without, every repo in the
-- feature, one nvim tab each.
local function review(opts)
  local root, repos = feature_repos()
  if not root then
    return vim.notify("FReview: not inside an f feature", vim.log.levels.WARN)
  end
  watch_bases()
  local only = opts and opts.args or ""
  if only == "pr" and not vim.tbl_contains(repos, "pr") then
    return review_pick()
  end
  if only ~= "" then
    if not vim.tbl_contains(repos, only) then
      return vim.notify("FReview: no repo " .. only .. " in this feature", vim.log.levels.WARN)
    end
    repos = { only }
    if not is_worktree(root .. "/" .. only) or not has_changes(root .. "/" .. only) then
      return wait_for_changes(root, only)
    end
  else
    repos = vim.tbl_filter(function(r) -- skip repos f is still setting up
      return is_worktree(root .. "/" .. r)
    end, repos)
    vim.o.tabline = "%!v:lua.require'f'.tabline()"
    vim.o.showtabline = 2
  end
  local first = vim.api.nvim_get_current_tabpage()
  open_codediff(root, repos, FEATURE, function()
    close_if_empty(first)
  end)
end

-- :FReviewToggle (<leader>pr) in a review tab: switch between the whole
-- feature and what the agent's last run changed.
local function review_toggle()
  local tab = vim.api.nvim_get_current_tabpage()
  local ok_repo, repo = pcall(vim.api.nvim_tabpage_get_var, tab, "f_repo")
  if not ok_repo then
    return vim.notify("FReview: not a review tab", vim.log.levels.WARN)
  end
  local root = feature_of(nil)
  local ok_rev, rev = pcall(vim.api.nvim_tabpage_get_var, tab, "f_rev")
  local next_rev = (ok_rev and rev == LAST_RUN) and FEATURE or LAST_RUN
  if next_rev == LAST_RUN then
    local res = vim.system({ "git", "-C", root .. "/" .. repo, "rev-parse", "-q", "--verify", LAST_RUN }):wait()
    if res.code ~= 0 then
      return vim.notify("FReview: the agent hasn't changed code in " .. repo .. " yet", vim.log.levels.WARN)
    end
  end
  open_codediff(root, { repo }, next_rev, function()
    if vim.api.nvim_tabpage_is_valid(tab) then
      vim.cmd("tabclose " .. vim.api.nvim_tabpage_get_number(tab))
    end
    vim.notify(repo .. ": " .. (next_rev == LAST_RUN and "since the agent's last run" or "whole feature"))
  end)
end

-- Open PRs of repo in the feature, from state.json (f keeps it fresh), bottom
-- of the stack first.
local function open_prs(root, repo)
  local fd = io.open(root .. "/.context/state.json")
  if not fd then
    return {}
  end
  local ok, state = pcall(vim.json.decode, fd:read("*a"))
  fd:close()
  local prs = {}
  for _, p in ipairs(ok and type(state.prs) == "table" and state.prs or {}) do
    if p.repo == repo and (p.status == "open" or p.status == "draft") and p.head and p.base then
      table.insert(prs, p)
    end
  end
  table.sort(prs, function(a, b)
    return (a.stack_pos or 0) == (b.stack_pos or 0) and a.number < b.number or (a.stack_pos or 0) < (b.stack_pos or 0)
  end)
  return prs
end

-- branch_ref: a local branch if there is one (stack branches), else origin's.
local function branch_ref(dir, name)
  if git(dir, "rev-parse", "-q", "--verify", "refs/heads/" .. name).code == 0 then
    return name
  end
  return "origin/" .. name
end

-- pr_target: what CodeDiff compares for p. The checked-out PR diffs the work
-- tree (live); others their committed head. Both from where the branch left its base.
local function pr_target(dir, p, current)
  local head = p.head == current and "HEAD" or branch_ref(dir, p.head)
  local mb = git(dir, "merge-base", branch_ref(dir, p.base), head)
  if mb.code ~= 0 then
    return nil
  end
  local base = vim.trim(mb.stdout)
  return p.head == current and base or base .. " " .. head, head
end

-- :FReview pr (<leader>pp) in a review tab: pick what the tab shows, the
-- whole feature or one PR of the repo's stack. Nothing is checked out.
function review_pick()
  local tab = vim.api.nvim_get_current_tabpage()
  local ok_repo, repo = pcall(vim.api.nvim_tabpage_get_var, tab, "f_repo")
  if not ok_repo then
    return vim.notify("FReview: not a review tab", vim.log.levels.WARN)
  end
  local root = feature_of(nil)
  local dir = root .. "/" .. repo
  local _, shown = pcall(vim.api.nvim_tabpage_get_var, tab, "f_rev")
  local current = vim.trim(git(dir, "branch", "--show-current").stdout or "")
  local prs = open_prs(root, repo)
  local items = { { text = "whole feature (vs " .. FEATURE:gsub("%.%.%.$", "") .. ")", rev = FEATURE, preview = { text = "Everything this feature changed in " .. repo .. "." } } }
  for _, p in ipairs(prs) do
    local rev, head = pr_target(dir, p, current)
    if rev then
      local stat = vim.trim(git(dir, "diff", "--stat", p.head == current and rev or rev:gsub(" ", "..")).stdout or "")
      table.insert(items, {
        text = ("%s#%d %s%s"):format(p.stack_pos and p.stack_pos > 0 and ("%d/%d "):format(p.stack_pos, #prs) or "", p.number, p.title or "", p.head == current and "  (checked out)" or ""),
        rev = rev,
        preview = { text = table.concat({ "#" .. p.number .. " " .. (p.title or ""), p.url or "", p.base .. " → " .. (head == "HEAD" and p.head .. " (work tree, live)" or p.head), "", stat ~= "" and stat or "no changes" }, "\n") },
      })
    end
  end
  for _, it in ipairs(items) do
    it.text = (it.rev == shown and "● " or "  ") .. it.text
  end

  local function go(item)
    if not item or item.rev == shown then
      return
    end
    open_codediff(root, { repo }, item.rev, function()
      if vim.api.nvim_tabpage_is_valid(tab) then
        vim.cmd("tabclose " .. vim.api.nvim_tabpage_get_number(tab))
      end
      vim.notify(repo .. ": " .. vim.trim(item.text:gsub("^●", "")))
    end)
  end

  local ok_snacks, Snacks = pcall(require, "snacks")
  if ok_snacks and Snacks.picker then
    return Snacks.picker.pick({
      title = "review " .. repo,
      items = items,
      format = "text",
      preview = "preview",
      layout = { preset = "default" },
      confirm = function(picker, item)
        picker:close()
        go(item)
      end,
    })
  end
  vim.ui.select(items, { prompt = "review " .. repo, format_item = function(it) return it.text end }, go)
end

M._repos = feature_repos -- for tests

function M.setup()
  vim.api.nvim_create_user_command("FComment", add, { range = true, nargs = "*", desc = "Comment for the agent" })
  vim.api.nvim_create_user_command("FComments", list, { desc = "List pending comments" })
  vim.api.nvim_create_user_command("FSend", send, { desc = "Send comments to the agent" })
  vim.api.nvim_create_user_command("FClear", clear, { desc = "Discard pending comments" })
  vim.api.nvim_create_user_command("FReview", review, {
    nargs = "?",
    desc = "Live CodeDiff for a feature repo (or all)",
    complete = function()
      return select(2, feature_repos()) or {}
    end,
  })
  vim.api.nvim_create_user_command("FReviewToggle", review_toggle, { desc = "Review: whole feature ↔ last run" })

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
  vim.keymap.set("n", "<leader>pr", "<cmd>FReviewToggle<cr>", { desc = "Review: feature ↔ last run" })
  vim.keymap.set("n", "<leader>pp", "<cmd>FReview pr<cr>", { desc = "Review: pick a stacked PR" })
end


return M
