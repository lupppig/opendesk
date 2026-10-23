# QA

`pnpm qa` runs CI's checks on your machine, for the areas a branch changes, against the committed
HEAD. Run it before opening a pull request and after every follow-up commit.

## One-time setup

```bash
cd qa && pnpm install
```

It needs node 22.18 or newer, which runs the `.ts` scripts directly.

## The loop

```bash
git commit ...                   # QA vouches for a commit, never a dirty tree
pnpm qa                          # CI's checks for every area the branch changes
git push -u origin HEAD
```

Fix what fails, commit, run it again.

## `pnpm qa`

Runs, for the areas the branch changes since it left `main`, the checks CI runs: gofmt, go vet,
go build and go test for the touched packages; typecheck, lint and the related vitest files in
`web/`, `admin/` and `desktop/`; the site and docs builds; mix format, compile and test in
`realtime/`; terraform fmt and validate; the installer checks; this harness's own tests.
Database tests skip unless `OPENDESK_TEST_DATABASE_URL` is set, and the summary says so.
Logs go to `.artifacts/qa/logs/<sha>/`, and the result to `.artifacts/qa/latest.json`.

`--all` checks every area. `--dirty` allows uncommitted changes.

## The hook

`.claude/settings.json` runs `hooks/pretool.ts` before an agent's shell commands. It refuses
`gh pr merge --admin` and changes to rulesets or branch protection: those are the maintainer's.
