---
description: Save a preference from this chat as a durable rule in my global AGENTS.md
argument-hint: "[what to remember]"
---
Turn a preference into a durable rule in my global instructions file:
~/.pi/agent/AGENTS.md (usually a link into my dotfiles)

What to remember: ${@:-the communication or working-style preference I most recently expressed or approved in this conversation}

Steps:
1. Read the file.
2. Write the rule as one short, concrete, imperative bullet. If it helps, add a brief example (do / don't). No vague wording like "be clear".
3. Choose a section: Workflow, Code taste, or Communication. Add a new section only if none fits.
4. If an existing rule overlaps, merge or tighten it. Never duplicate.
5. Show me the exact diff and wait for my OK before editing.
6. After editing, if the file is a link into a git repo, commit just that file there:
   `f=$(realpath ~/.pi/agent/AGENTS.md); git -C "$(dirname "$f")" commit -m "AGENTS.md: <the rule, in a few words>" -- "$f"`
   (only this file, even if the repo has other changes).
7. Tell me to run /reload.
