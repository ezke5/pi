# pi

`pi` is a package manager starter for CRUX Linux. The repository contains two
applications: `pi` and `pi-index`. `pi install` prefers a checksum-verified
binary selected from the JSON repository index (then the local binary cache)
and falls back to the dependency-resolved PKGBUILD graph.

## Build

```sh
make build       # bin/pi and bin/pi-index
make test        # all unit and integration tests
make verify      # build, test, and execute both applications
make clean
```

The index helper incrementally merges archives without removing older versions:

```sh
bin/pi-index --packages ./pkg --index ./pkg/index.json \
  --base-url https://github.com/OWNER/REPO/releases/download/rolling
```

Use the published index and assets during installation with
`--index ./pkg/index.json` or `--repo-url
https://github.com/OWNER/REPO/releases/download/rolling`. `--source-build`
explicitly bypasses binary selection. A downloaded or cached indexed archive is
SHA-256 verified; failed downloads and checksum validation automatically fall
back to PKGBUILD source resolution.

## Safe examples

```sh
go run ./cmd/pi init
go run ./cmd/pi --dry-run --root /srv/crux-root --ports ./ports --work ./work --temp ./temp --cache ./pkg build demo
go run ./cmd/pi --root /srv/crux-root list
go run ./cmd/pi --root /srv/crux-root inspect demo
```

The target root defaults to `/` but `pi` refuses to operate on `/` unless
`--allow-host-root` is explicitly supplied. Use a dedicated CRUX rootfs for
build and install testing.

Global INI configuration is loaded from `pi.ini`, `.pi.ini`, or
`~/.config/pi/config.ini`. CLI flags override INI values.

`pi build` expands `pkgname`, `pkgver`, and `pkgrel` references in `source`
entries, then exports the recipe metadata variables (`pkgname`, `pkgver`,
`pkgrel`, `srcdir`, and `pkgdir`) to `build()` and `package()`, clears
`CONFIG_SITE`, and extracts standard source archives into `srcdir` while
retaining the staged archive. Use `--reuse` to accept cached sources; when
re-download mode is requested interactively, `pi` asks before replacing an
existing cached source. `pi install <name>` resolves the highest-version
matching package archive from the configured index/cache. The GitHub Actions
workflow builds both applications, updates `index.json`, and publishes the
index and package archives to the rolling GitHub Release with `gh`.
