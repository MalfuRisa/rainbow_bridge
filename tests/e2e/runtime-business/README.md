# Runtime business API verification

This suite verifies the public runtime contract with two synthetic business sites A and B.
It uses only test data created in a temporary SQLite instance and never copies a user
database, backup, cookie, token, or password into the repository.

## Run

Prerequisites: Go toolchain, Node 22, Playwright Chromium already installed for
`tests/e2e` (`npx playwright install chromium`), and Windows PowerShell.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File script/test-runtime-business.ps1
```

The script builds the current working tree with a distinguishable version tag, starts:

- `127.0.0.1:18140` with prefix `/rainbow-bridge` (main instance)
- `127.0.0.1:18141` with no prefix (separate root-path instance and database)
- `127.0.0.1:18142` and `127.0.0.1:18143` as tiny business pages for Chromium

All paths and logs are under `.local-qa/runtime-business/<run_id>/` (ignored by git).
The runner stops only the processes it started. Each stop first checks `/api/v1/version`
against the run's build tag.

Optional manual demo mode:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File script/test-runtime-business.ps1 -KeepRunning
```

`-KeepRunning` leaves the instances alive and prints their PIDs and exact `Stop-Process`
commands. The generated Postman environment points at that live instance. Without
`-KeepRunning`, the environment file is still written but the instance is stopped; rerun
with `-KeepRunning` before importing it for a live demo.

Useful switches:

- `-SkipBuild` reuses the binary under the current run ID directory.
- `-RunId <id>` names a repeatable run directory.

## Artifacts

Each run writes:

- `artifacts/summary.json` — RT01–RT30 matrix, request counts, status, defects
- `artifacts/requests.jsonl` — method/path/status/timing; no Cookie/CSRF/Authorization
- `artifacts/evidence/RTxx.json` — per-case assertions and actual error bodies
- `artifacts/fixtures.json` — synthetic scope/version/snapshot map
- `artifacts/browser-a.png`, `artifacts/browser-b.png` — Chromium screenshots
- `artifacts/postman/runtime-business.postman_environment.json` — actual non-secret IDs
- instance logs in the sibling `instance-base/` and `instance-root/` directories

No password, Cookie, CSRF token, session header, or Authorization header is written to
artifacts.

## Postman

Import both files:

- `tests/e2e/runtime-business/postman/runtime-business.postman_collection.json`
- `artifacts/postman/runtime-business.postman_environment.json` from a real run

Clear Postman's Cookie jar before the demo. The collection is `No Auth` and sends no
Cookie, Authorization, or CSRF header. The banner request uses the exact absolute
`image_url`; do not prepend `api_root` to it again.

If the instances were stopped, the collection is documentation only. Restart with
`-KeepRunning` and regenerate the environment before presenting.

## Three-minute demo

1. Send **A default active config** and **B default active config**; compare markers and image URLs.
2. Send **A fixed version**; point out `selection_mode=fixed` and no active revision fields.
3. In a separate administrator shell (not in this collection), activate another A version.
4. Re-send **A default active config**; the marker changes without restarting the service.
5. Send **Banner image**; show PNG bytes and A/B screenshots. No business headers or login are sent.

## Scope

This is a bounded single-instance runtime verification. It does not claim K8s multi-Pod,
multiple database engines, hot restore, or permission isolation acceptance. Product
defects are recorded in `summary.json` and the PRD; this suite does not modify business
source code to make a failing expectation pass.