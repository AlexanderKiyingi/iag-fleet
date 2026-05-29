# Vendored copy of shared/platform-go

This directory is a **committed snapshot** of `shared/platform-go` from the
`IAG_multi_backend` meta-repo. Standalone Railway builds copy from here — they
do **not** clone the private meta-repo at build time.

## Refresh from meta-repo

From `iag-fleet` repo root:

```bash
sh scripts/sync-platform-go.sh
```

Or set `IAG_PLATFORM_GO_SRC` to an explicit path. Commit the updated tree after
syncing when platform-go changes.
