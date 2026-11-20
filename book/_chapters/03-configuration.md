---
order: 3
title: Configuration with pi.ini
summary: One small INI file tells pi where everything lives.
---
`pi` looks for `pi.ini` in the current folder, then `.pi.ini`, then `~/.config/pi/config.ini`. Pass `--config PATH` to choose one explicitly. Command-line flags always override the file.

```ini
[paths]
ports   = /tmp/pi-e2e-test/ports     ; folders of recipes
work    = /tmp/pi-e2e-test/work      ; temporary build area
temp    = /tmp/pi-e2e-test/temp
cache   = /tmp/pi-e2e-test/pkg       ; downloaded sources
binpkgs = /tmp/pi-e2e-test/pkg       ; finished .pkg.tar.zst files
index   = /tmp/pi-e2e-test/pkg/index.json
root    = /tmp/pi-e2e-test/rootfs    ; where packages are installed
arch    = x86_64

[build]
jobs = 2
strip = true
keep_work = false
clean_junk = true
download_timeout = 10m

[use]
flags = demo
```

### The sections

- **`[paths]`** answers "where?". `root` is the target filesystem; `pi` refuses `/` unless you add `--allow-host-root`.
- **`[build]`** controls compilation: parallel `jobs`, whether to `strip` binaries, whether to keep the work folder (`keep_work`), and whether to delete known junk files such as `usr/share/info/dir` (`clean_junk`). `download_timeout` accepts `10m` or a number of seconds.
- **`[use]`** sets global USE flags, covered in chapter 5.

### Safe practice

Point `root` at a throwaway folder while learning. Nothing in this book needs your real system.

> Both `an` and `pi` read the same file, so one `pi.ini` describes your whole setup.
{: .note}
