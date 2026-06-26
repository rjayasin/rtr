# CLAUDE.md

## Commit messages

Always use [Conventional Commit](https://www.conventionalcommits.org) prefixes
for commits in this repository. The release workflow (`.github/workflows/release.yml`)
runs `svu next`, which infers the version bump from these prefixes on every push
to `main`:

- `feat:` — new functionality → **minor** bump
- `fix:` — bug fix → **patch** bump
- `feat!:` / `fix!:` (or a `BREAKING CHANGE:` footer) → **major** bump
- `docs:`, `test:`, `chore:`, `ci:`, `refactor:`, etc. — no semantic bump
  (falls back to a patch release so every push still ships)

Choose the prefix that reflects the actual change so the automated version bump
is correct. For squash-merged PRs, the PR title becomes the commit message, so
title PRs with a Conventional Commit prefix too.
