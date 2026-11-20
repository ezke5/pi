---
order: 1
title: The three tools
summary: What pi, an, and pi-index each do, and why they are separate.
---
This handbook teaches a small toolchain for building and installing packages into a minimal Linux root filesystem. It is made of three programs, each with one job.

| Program | Job |
| --- | --- |
| `pi` | Build packages from recipes, install them into a root, verify them, remove them. |
| `an` | Prepare and enter a root filesystem: `bootstrap` and `chroot`. |
| `pi-index` | Scan a folder of built packages and write an `index.json` catalogue. |

### Why separate programs

`pi` never mounts anything and never enters a chroot. `an` never builds or installs packages. Keeping them apart means the program that unpacks files into your root cannot also run commands inside it, and each tool stays small enough to read in an afternoon.

### The shape of a package

A package is a single file named `name-version-release.pkg.tar.zst`: a tar archive compressed with zstd. Inside are the files to install, plus two metadata entries: `.PKGINFO` (name, version, USE flags) and `.FOOTPRINT` (a list of every path with its mode). Every entry is owned by `root:root`, so a package built by an ordinary user installs with correct ownership.

> Read the chapters in order the first time. Each one uses only ideas introduced before it.
{: .note}
