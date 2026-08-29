# Releasing

GitHub releases only, cut by the `Release` action in `buildbuddy.yaml` on every push to
`trunk` plus a pushed `v*` tag:

```bash
bazel run --config=ci --stamp -c opt //tools:release -- --dest dist
```

`//tools:release` (`release.sh`) builds `//service/cmd:release` optimized and
version-stamped, writing into `--dest`: four binaries, `SHA256SUMS`, `install.sh`, then `gh
release create --generate-notes`. Named by `version.sh` — a trunk commit ships as a
pseudo-version, a `vX.Y.Z` tag ships under the tag; an already-published version is
skipped, not failed. The tag filter (`v[0-9]+.[0-9]+.[0-9]+`) is **GitHub's filter-pattern
dialect, not a regex** — `+` means "one or more of the preceding char", `.` is literal.

`install.sh.tmpl` renders to `install.sh`, sums baked in at render time; picks
`grpcview_<goos>_<goarch>` from `uname`, installs into the first writable of
`/usr/local/bin`, `~/.local/bin` via a temp-name rename (avoids `ETXTBSY`). `grpcview
uninstall` deletes only binaries by default; `--purge` also deletes `wsroot.ConfigRoot()`
(trust list, cached descriptor blobs, run history — **not a cache**,
`service/wsroot/wsroot.go`) and `wsroot.CacheRoot()` (disposable).

Versions stamp into `cli.version`: an exact `vX.Y.Z` tag on HEAD wins, else a Go
pseudo-version, dirty worktree gets `+dirty`. `.bazelrc` omits `--stamp` — unstamped builds
leave `cli.version` at `dev`.
