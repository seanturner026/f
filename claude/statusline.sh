#!/usr/bin/env bash
# pi-style footer: "~/dir (branch)" (outside features), then cache · cost · context · model • effort, then the f feature's PRs.
in=$(cat)
# \x1f, not tab: read merges empty tab-separated fields and shifts the rest.
IFS=$'\x1f' read -r dir root r w hit cost pct size model effort < <(jq -r '[
  .workspace.current_dir,
  .workspace.project_dir,
  (.context_window.current_usage.cache_read_input_tokens // 0),
  (.context_window.current_usage.cache_creation_input_tokens // 0),
  (if .prompt_cache.hit_ratio then (.prompt_cache.hit_ratio * 1000 | round / 10 | tostring) + "%" else "" end),
  (.cost.total_cost_usd // 0),
  (.context_window.used_percentage // 0),
  (.context_window.context_window_size // 0),
  .model.id,
  (.effort.level // "")
] | map(tostring) | join("\u001f")' <<<"$in")

human() { awk -v n="$1" 'BEGIN { if (n >= 1e6) printf "%.1fM", n/1e6; else if (n >= 1e3) printf "%dk", n/1e3; else printf "%d", n }'; }

branch=$(git -C "$dir" branch --show-current 2>/dev/null)
left=$(printf 'R%s W%s%s $%.3f %s%%/%s' "$(human "$r")" "$(human "$w")" "${hit:+ CH$hit}" "$cost" "$pct" "$(human "$size")")

dim=$'\033[2m' off=$'\033[0m'
# In a feature the prompt border already names it: skip the path line there.
[ -f "$root/.context/feature.json" ] || printf '%s%s%s%s\n' "$dim" "${dir/#$HOME/~}" "${branch:+ ($branch)}" "$off"
# Inline, not right-aligned: the status row is narrower than $COLUMNS, so padding pushed the model off-screen.
printf '%s%s   %s%s%s\n' "$dim" "$left" "$model" "${effort:+ • $effort}" "$off"

# PRs from f's local state (the f mod refreshes it from GitHub after each turn); as in agent/extensions/f.ts.
[ -f "$root/.context/feature.json" ] || exit 0
prs=$(cd "$root" && f prs --json 2>/dev/null | jq -r '
  def c(code; s): "\u001b[" + code + "m" + s + "\u001b[0m";
  def ci: [.checks[]?.state] as $s
    | if ($s | index("fail")) then " " + c("31"; "✗")
      elif ($s | index("pending")) then " " + c("36"; "⋯")
      elif ($s | length) > 0 then " " + c("32"; "✓") else "" end;
  (.prs // []) as $all
  | ([$all[] | select(.status == "open" or .status == "draft")
      | "\(.repo) #\(.number) " + (if .status == "open" then c("36"; "open") else c("2"; "draft") end) + ci]
     + ([$all[] | select(.status == "merged")] | length | if . > 0 then [c("32"; "\(.) merged")] else [] end))
  | if length > 0 then c("2"; "PRs: ") + join(c("2"; " · ")) else empty end')
# Exit 0 even with no PRs: Claude Code drops the whole status line when this script fails.
if [ -n "$prs" ]; then printf '%s\n' "$prs"; fi
