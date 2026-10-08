---
description: Save a preference from this chat as a durable rule in my global AGENTS.md
argument-hint: "[what to remember]"
---
Turn a preference into a durable rule in my global instructions file:
/Users/sean/code/github/seanturner026/pi/agent/AGENTS.md

What to remember: ${@:-the communication or working-style preference I most recently expressed or approved in this conversation}

Steps:
1. Read the file.
2. Write the rule as one short, concrete, imperative bullet. If it helps, add a brief example (do / don't). No vague wording like "be clear".
3. Choose a section: Workflow, Code taste, or Communication. Add a new section only if none fits.
4. If an existing rule overlaps, merge or tighten it. Never duplicate.
5. Show me the exact diff and wait for my OK before editing.
6. After editing, commit just that file in its repo:
   `git -C /Users/sean/code/github/seanturner026/pi commit -m "AGENTS.md: <the rule, in a few words>" -- agent/AGENTS.md`
   (only this file, even if the repo has other changes).
7. Tell me to run /reload.
