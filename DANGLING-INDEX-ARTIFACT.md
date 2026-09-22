# Forge builds can hold a dangling fusion-index artifact reference

## Summary

A `CIBuild`/app-build row in forge's DB can end up pointing at a `fusion-index` artifact ID that
no longer exists. Once that happens, forge has no way to detect or repair it: `GitWatcher`'s
version-skip logic treats the build as permanently done (`status: SUCCESS`, watcher `phase:
Active`) and never re-triggers or re-publishes, even though the actual artifact a caller would
try to fetch from `fusion-index` is gone. Nothing currently reconciles the two systems back into
agreement.

This surfaced from `fusion-wizard`'s e2e testing against this cluster's real `fusion-forge` /
`fusion-index` / `fusion-weave` (2026-09-22), but the underlying bug is in forge's own
build/GitWatcher lifecycle, independent of the wizard.

## Live repro (as of 2026-09-22, this cluster)

```
$ curl -s http://127.0.0.1:18080/api/v1/appbuilds?name=simple-streamlit | python3 -m json.tool
{
  "items": [
    {
      "id": 47,
      "name": "simple-streamlit",
      "version": "0.1.0",
      "status": "SUCCESS",
      "buildType": "app",
      "indexArtifactId": 1401,
      "indexArtifactVersion": "0.1.0",
      "ciBuildName": "forge-app-47",
      "repoUrl": "http://fusion-testbed-gitea.fusion.svc.cluster.local:3000/gitea/simple-streamlit.git",
      "repoRef": "main",
      "createdAt": "2026-06-02T11:04:28.919954Z",
      "updatedAt": "2026-09-22T11:16:31.657591Z"
    }
  ]
}

$ curl -s -w '\nHTTP:%{http_code}\n' http://127.0.0.1:18081/api/v1/artifacts/1401
{"error":"artifact not found"}
HTTP:404
```

Build `47` has existed since 2026-06-02 and is the build the testbed's own long-running
`GitWatcher` (`simple-streamlit`, helm-managed by `fusion-testbed`, watching the same repo) relies
on. Its own reconcile loop checked again at `2026-09-22T11:20:51Z`, right after the artifact was
deleted, and reported `phase: Active`, `lastBuiltVersion: 0.1.0` — it never noticed the artifact
underneath it is gone, and never will until a new commit lands on the watched repo.

## How the artifact got deleted (context, not the bug itself)

`fusion-wizard` (`../fusion-wizard`, a sibling project) ran its own `GitWatcher`
(`wizard-e2e-streamlit`) against the same repo as part of an e2e test. Its `waitBuild` step called
forge, forge returned the pre-existing build `47` (name+version match), and the wizard's own
ledger recorded the `fusion-index` artifact side as a resource it had just **created** (disposition
`Created`, sole ref, `managed: true`) — see `fusion-wizard`'s
[design invariants](../fusion-wizard/CLAUDE.md) ("Delete upstream only when the last ref is gone
AND `managed: true`"). When the wizard rolled that run back (its own rollback API, exercised
deliberately as a test), it correctly followed its own rules and deleted the artifact it believed
it owned — artifact `1401`.

Whether the wizard's ledger *should* have recognized artifact `1401` as pre-existing (and thus
`managed: false`, never delete) is a `fusion-wizard`-side question, tracked there, not here. What
this file is about is what happened on forge's side once the artifact was gone: **nothing** — no
error, no retry, no self-healing, and the build's own DB row still points at the dead ID.

## Root cause in this codebase

`internal/controller/gitwatcher_controller.go`, the `Reconcile` skip logic around lines 143–171:

```go
// Skip if version unchanged since last successful build.
if version != "" && version == watcher.Status.LastBuiltVersion {
    logger.Info("version unchanged — skipping", "watcher", req.Name, "version", version)
    watcher.Status.Message = fmt.Sprintf("version %s already built — skipping", version)
    _ = r.Status().Patch(ctx, &watcher, base)
    return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
}

// Check DB for an existing row with the same (name, version).
if existing, dbErr := r.DB.GetVenvBuildByNameAndVersion(ctx, name, version); dbErr == nil {
    switch existing.Status {
    case "SUCCESS":
        logger.Info("version already built in DB — skipping", "watcher", req.Name, "version", version)
        watcher.Status.LastBuiltVersion = version
        watcher.Status.ConsecutiveFailures = 0
        _ = r.Status().Patch(ctx, &watcher, base)
        return ctrl.Result{RequeueAfter: r.jitteredInterval(watcher.Name)}, nil
    ...
```

Both short-circuits — the CR-status check and the DB `(name, version)` check — only ask "have I
built this before successfully?". Neither ever asks fusion-index "does the artifact I built still
exist?". Once a build is `SUCCESS`, it is permanently considered done from forge's point of view,
regardless of what happens to the `fusion-index` side afterward.

This is the same *class* of problem forge already has a remedy for in the CIBuild-CR direction
(`POST /api/v1/builds/zombie-cleanup`, documented in `CLAUDE.md`, for when a `CIBuild` CR is
deleted outside the API and the DB row is left stuck) — but there is no equivalent for "the
fusion-index side of a completed build disappeared." `CLAUDE.md` also already documents a related
symptom one layer up: "Retriggering a build for the same version requires manual fusion-index
cleanup ... if it leaves the version behind the next trigger returns 409 conflict — delete
manually: `curl -X DELETE .../artifacts/{id}/versions/{ver}`" — that's the operator-facing
workaround for essentially this same desync, today done by hand.

## Why it matters

- Any consumer that resolves `app.<name>` from `fusion-index` (weave, another forge build,
  `fusion-wizard`) will get a real 404 for something forge is confidently reporting as built and
  `Active`. There's no error surfaced anywhere in forge's own status to explain why.
- The watcher will never repair itself: it only re-evaluates once `HEAD` changes (a new commit).
  A repo that isn't being actively developed can sit "successfully built" but factually
  unavailable indefinitely.
- Any external system that deletes a `fusion-index` artifact as part of its own cleanup
  (ref-counted or not — `fusion-wizard` is the concrete case here, but it's not the only plausible
  one) will silently desync forge without any signal back to forge that it happened.

## Possible directions (not prescribing one — pick this up fresh)

1. **Verify before skipping**: on the "already built" fast paths, have the GitWatcher reconciler
   confirm the artifact/version still exists in `fusion-index` before trusting `SUCCESS`/
   `LastBuiltVersion`; fall through to a rebuild if it's gone.
2. **Extend zombie-cleanup**: generalize `POST /api/v1/builds/zombie-cleanup` (or add a sibling
   endpoint) to also detect and repair builds whose `indexArtifactId` no longer resolves, not just
   builds whose `CIBuild` CR vanished.
3. **A cross-service delete hook**: give forge (or require callers to use) a single endpoint that
   deletes a build *and* keeps its own DB state honest, so other services performing artifact
   cleanup (e.g. `fusion-wizard`'s rollback) have a correct way to tell forge "this got deleted,"
   instead of `fusion-index` and forge silently drifting apart. This would also address the
   already-documented 409-on-retrigger workaround more permanently.

## Pointers

- `internal/controller/gitwatcher_controller.go` — the skip logic above.
- `internal/db/db.go` — `GetVenvBuildByNameAndVersion` and friends.
- `CLAUDE.md` (this repo) — existing "zombie-cleanup" and "retriggering requires manual
  fusion-index cleanup" notes, both adjacent to this bug.
- `../fusion-wizard/CLAUDE.md` — "Known open risks: Forge has no per-build delete" (the
  wizard-side note that prompted this investigation) and the ledger/rollback design invariants
  that produced the repro above.
- Live in this cluster right now (may have changed by the time you read this): build `47`,
  `fusion-index` artifact `1401`, namespace `fusion`, repo
  `http://fusion-testbed-gitea.fusion.svc.cluster.local:3000/gitea/simple-streamlit.git`.
