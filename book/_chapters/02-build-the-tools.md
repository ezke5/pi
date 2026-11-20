---
order: 2
title: Build the tools
summary: Compile all three programs and run the test suite.
---
You need Go, GNU make, and the `zstd` command. Nothing else.

```sh
make build     # produces bin/pi, bin/an, bin/pi-index
make test      # go test ./...
make verify    # build + test + run each binary's help
```

`make verify` is the one command to trust: if it passes, the programs build, the tests pass, and each binary starts.

### Build one tool at a time

```sh
make pi
make an
make pi-index
```

### Where files go

The `bin/` folder holds the compiled programs. It is not meant to be committed; keep it untracked (or listed in `.gitignore`) so a rebuild never shows up as a change.

> If a build fails with "read-only file system" about a Go cache, your environment is blocking writes to the Go module cache. Point `GOCACHE` and `GOMODCACHE` at a writable folder and run again.
{: .note}
