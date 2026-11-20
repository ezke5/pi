---
order: 4
title: Writing a recipe
summary: A PKGBUILD describes how to build a package and where to stage its files.
---
A recipe is a shell-style file named `PKGBUILD`, stored at `ports/<name>/PKGBUILD`.

```sh
pkgname=hello
pkgver=1.0.0
pkgrel=1
depends=()
makedepends=()

build() {
  printf '#!/bin/sh\necho hello\n' > "$srcdir/hello"
}

package() {
  mkdir -p "$pkgdir/usr/bin"
  install -m 0755 "$srcdir/hello" "$pkgdir/usr/bin/hello"
}
```

### The variables `pi` gives you

| Variable | Meaning |
| --- | --- |
| `srcdir` | Where sources are unpacked. `build()` starts here. |
| `pkgdir` | The **staging root**. Install files here, never into the real system. |
| `pkgname`, `pkgver`, `pkgrel` | Taken from the recipe. |
| `JOBS`, `CFLAGS`, `LDFLAGS` | From `[build]` in `pi.ini`. |

The staging variable is **`pkgdir`**, in lowercase. It is not `DESTDIR` and not `PKG`. When a project's own installer wants `DESTDIR`, pass it explicitly:

```sh
package() {
  make DESTDIR="$pkgdir" install
}
```

### What pi does around your functions

1. Starts from a clean environment (host `LD_LIBRARY_PATH`, `DESTDIR`, and similar are removed).
2. Runs `build()`, then, if you defined `package()`, empties `pkgdir` so build leftovers cannot leak in, then runs `package()`.
3. Removes junk files, strips binaries, and checks symlinks.
4. Refuses to package an empty `pkgdir`.

### Symlinks must be relative

A link such as `bin/bash -> /usr/bin/bash` is rejected at build time with a suggestion. Write it as `../usr/bin/bash` instead. The same rule is enforced again at install time, so a bad package is stopped before it is ever shared.

> Dependencies listed in `depends` are needed to run the package. `makedepends` are needed only to build it, and are not installed when you install a ready-made binary.
{: .note}
