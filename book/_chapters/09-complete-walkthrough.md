---
order: 9
title: A complete walkthrough
summary: Everything together, in a throwaway folder that cannot hurt your system.
---
Every path below lives under `/tmp/pi-e2e-test`.

### 1. Create the folders and configuration

```sh
mkdir -p /tmp/pi-e2e-test/{ports/hello,work,temp,pkg,rootfs}
cat > /tmp/pi-e2e-test/pi.ini <<'EOF'
[paths]
ports = /tmp/pi-e2e-test/ports
work = /tmp/pi-e2e-test/work
temp = /tmp/pi-e2e-test/temp
cache = /tmp/pi-e2e-test/pkg
binpkgs = /tmp/pi-e2e-test/pkg
index = /tmp/pi-e2e-test/pkg/index.json
root = /tmp/pi-e2e-test/rootfs
arch = x86_64
EOF
```

### 2. Write a recipe

```sh
cat > /tmp/pi-e2e-test/ports/hello/PKGBUILD <<'EOF'
pkgname=hello
pkgver=1.0.0
pkgrel=1
package() {
  mkdir -p "$pkgdir/usr/bin" "$pkgdir/bin"
  printf '#!/bin/sh\necho hello\n' > "$pkgdir/usr/bin/hello"
  chmod 0755 "$pkgdir/usr/bin/hello"
  ln -s ../usr/bin/hello "$pkgdir/bin/hello"
}
EOF
```

### 3. Run the pipeline

```sh
CFG=/tmp/pi-e2e-test/pi.ini
bin/an  --config $CFG bootstrap
bin/pi  --config $CFG build hello
bin/pi-index --packages /tmp/pi-e2e-test/pkg --index /tmp/pi-e2e-test/pkg/index.json --prune
bin/pi  --config $CFG install hello
bin/pi  --config $CFG verify
bin/pi  --config $CFG install hello     # skipped: already installed
```

The last command should print the smart-skip message. Look at `rootfs/var/lib/pi/packages/hello.footprint` to see the recorded modes and the relative symlink.

### 4. Clean up

```sh
rm -rf /tmp/pi-e2e-test
```
