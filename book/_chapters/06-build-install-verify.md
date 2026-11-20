---
order: 6
title: Build, install, verify
summary: The everyday commands, and how smart-skip avoids pointless work.
---
```sh
pi build hello        # recipe -> binpkgs/hello-1.0.0-1.pkg.tar.zst
pi install hello      # package -> into the configured root
pi verify             # check every installed file
pi list               # installed packages
pi remove hello       # take it out again
```

Add `--dry-run` to any of them to see what would happen without changing anything.

### Smart skip

Before building or installing, `pi` asks one question: *is this exact version, with this exact use hash, already installed and intact?* If so it prints a clear message and does nothing:

```text
already installed with matching USE hash ...; skipping (use --rebuild to reinstall)
```

It rebuilds or reinstalls when the version changes, when the use hash changes, or when you pass `--rebuild`.

### What "intact" means

`pi verify` and smart-skip read the package's footprint and check each path:

- the file exists and can be opened;
- the mode matches (a `0555` library that became `0644` is reported as drift);
- symlinks still point where they should, and their chains resolve inside the root, which catches a broken `lib64/ld-linux-x86-64.so.2`;
- when run as root, ownership is still `0:0`.

If a check fails, `pi install` says why and repairs the package.

### Replacing files safely

Installing writes each file to a temporary name and renames it into place. Read-only files (`0555`, `0444`) are replaced cleanly, and a running program is never half-overwritten. Installs and removals take a lock, so two `pi` processes cannot corrupt the database at once.

> `--keep-work` leaves the build folder after a successful build, which helps when debugging a recipe. `--no-strip` skips stripping.
{: .note}
