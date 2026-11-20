---
order: 8
title: The root filesystem with an
summary: Create a minimal root and step inside it safely.
---
`an` handles the root filesystem itself.

```sh
an --config pi.ini bootstrap
an --config pi.ini chroot
```

### bootstrap

Creates a small, standard directory skeleton if it is missing: `usr/bin`, `usr/lib`, `usr/lib64`, `etc`, `dev`, `proc`, `sys`, `run`, `root`, `tmp`, `var`, and a few more. It also writes minimal `/etc/passwd` and `/etc/group` files, but never overwrites ones that already exist. It deliberately does **not** create `/lib64` or `/bin`: those belong to the packages you install.

### chroot

Runs a command inside the root. With no command it starts `/bin/bash --noprofile --norc`, or `/bin/sh` if bash is absent.

Before running, `an` checks that the program exists inside the root and that its dynamic loader (for example `/lib64/ld-linux-x86-64.so.2`) exists there too, and tells you exactly what is missing instead of a vague "no such file".

It then mounts `/dev`, `/dev/pts`, `/dev/shm`, `/proc`, and `/sys`, copies the host's `resolv.conf` for networking, and runs your command. When the command ends, or fails, or you press Ctrl-C, it unmounts only what it mounted, in reverse order.

> `chroot` needs administrator rights and always refuses `/` as the root. Use `--dry-run` first to see what it would do.
{: .warn}
