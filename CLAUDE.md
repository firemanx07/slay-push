# CLAUDE.md

This project's agent instructions — architecture notes, repo layout, build/lint/test commands,
and code style — live in [`AGENTS.md`](AGENTS.md) so they're shared across every AI coding tool
used on this repo, not duplicated per tool. Read it before making changes.

Claude Code-specific notes:

- Git commits in this repo are authored as `firemanx07 <49232518+firemanx07@users.noreply.github.com>`.
  Set `GIT_AUTHOR_NAME`/`GIT_AUTHOR_EMAIL`/`GIT_COMMITTER_NAME`/`GIT_COMMITTER_EMAIL` on the
  `git commit` invocation; do not add a Claude co-author trailer.
- Before `git push`, switch the active `gh`/git credential to the `firemanx07` account (it owns
  this repo), then switch back to the default account afterward.
