---
order: 7
title: The package index
summary: Publish a catalogue of your packages with pi-index.
---
`pi-index` scans a folder of `.pkg.tar.zst` files and writes `index.json`.

```sh
bin/pi-index --packages ./pkg --index ./pkg/index.json --prune
```

| Flag | Meaning |
| --- | --- |
| `--packages DIR` | Folder to scan. |
| `--index FILE` | Index to create or update. |
| `--base-url URL` | Download address stored for each package (`http` or `https` only). |
| `--arch NAME` | Default architecture (`x86_64`). |
| `--prune` | Drop entries whose package file no longer exists. |

### Merging, not replacing

Running `pi-index` again adds new packages and keeps older versions. If the same file is rebuilt with different USE flags, the old entry for that file is replaced, so the index never points at a file that has changed underneath it.

### Using the index

```sh
pi --index ./pkg/index.json install hello
pi --repo-url https://example.org/repo install hello
```

`pi` prefers a checksum-verified binary from the index, then the local cache, and finally builds from the recipe. A download that fails or does not match its SHA-256 falls back to building.

> Large downloads retry up to three times with a growing pause, and use the generous `download_timeout` from `pi.ini`.
{: .note}
