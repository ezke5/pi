# pi

`pi` is a source-first package manager starter for CRUX Linux.

## Build

```sh
go build ./...
go test ./...
```

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

`pi build` exports the recipe metadata variables (`pkgname`, `pkgver`,
`pkgrel`, `srcdir`, and `pkgdir`) to `build()` and `package()`, clears
`CONFIG_SITE`, and extracts standard source archives into `srcdir` while
retaining the staged archive. Use `--reuse` to accept cached sources; when
re-download mode is requested interactively, `pi` asks before replacing an
existing cached source. `pi install <name>` resolves the highest-version
matching package archive from the configured cache.