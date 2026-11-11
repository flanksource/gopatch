# Gopatch effectiveness analysis

This standalone development module analyzes retained agent histories. It is checked in CI but is not included in gopatch releases.

From the repository root:

```bash
GOWORK=off make build-hack test-hack lint-hack
GOWORK=off go -C hack/gopatch-effectiveness run . --help
GOWORK=off go -C hack/gopatch-effectiveness run . --mode prepare --sql-dir . --output ../../.tmp/gopatch-effectiveness --since 2026-10-01T00:00:00Z --before 2026-11-01T00:00:00Z
```

Analysis requires `GAVEL_DB_DSN` and the PostgreSQL `psql` client on PATH. SQL queries run in read-only transactions. Native-history modes also require access to the retained transcript files. `--sql-dir` is relative to the process working directory; use `.` when running from this module. Select the intended time window explicitly. Output files can contain sensitive session content and belong in an untracked artifact directory.
