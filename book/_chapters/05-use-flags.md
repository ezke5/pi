---
order: 5
title: USE flags
summary: Turn optional features on or off, and let pi notice when that changes.
---
A USE flag is a named feature, such as `wayland` or `zstd`. Set them globally in `pi.ini`:

```ini
[use]
flags = wayland zstd -systemd
package_use_file = package.use
```

A leading `-` turns a flag off. Per-package overrides live in the `package.use` file, one package per line:

```text
dwl wayland -x11
```

### The use hash

`pi` condenses the effective flags for a package into a short fingerprint called `use_hash`. It is stored inside the built package and in the index. Two builds with the same version but different flags have different hashes, so they are never confused.

### Using flags inside a recipe

```sh
build() {
  if use_enabled wayland; then
    echo "building with wayland"
  fi
}
```

`use_enabled` and `use_disabled` are shell helpers provided to every recipe. The raw lists are also available as `PI_USE_FLAGS` and `PI_USE_DISABLED`.

> Change a flag, run `pi install` again, and `pi` notices the hash changed and rebuilds. Change nothing, and it skips the work. That is the subject of the next chapter.
{: .note}
