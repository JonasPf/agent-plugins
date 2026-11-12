#!/bin/sh
# Installs Nix where the tests run: testenv/Dockerfile and CI both call this, so
# the two cannot drift. It is Nix's own release, unpacked into /nix/store and
# registered the way its installer does, with nix on PATH for whoever runs the
# tests. The runtime image carries a static build instead (see ../Dockerfile),
# because there /nix is a volume and a nix inside it would outlive the image
# that brought it. The version is the one the agent's release ships as nixStatic.
set -eu
VERSION=2.34.9
case "$(uname -m)" in
  x86_64) arch=x86_64 sha=e8912e087582b3799e0e5a6b06f9d63386c54821f77ad4de3b9b748736ad364e ;;
  aarch64 | arm64) arch=aarch64 sha=f0bb2518db6c9db3f925be3edb76f6e0da17cb72fba329493f510ca08e5111a7 ;;
  *) echo "no Nix release for $(uname -m)" >&2; exit 1 ;;
esac
name="nix-$VERSION-$arch-linux"
tmp="$(mktemp -d)"
curl -fsSL -o "$tmp/$name.tar.xz" "https://releases.nixos.org/nix/nix-$VERSION/$name.tar.xz"
echo "$sha  $tmp/$name.tar.xz" | sha256sum -c -
tar -xJf "$tmp/$name.tar.xz" -C "$tmp"
mkdir -p /nix/store /nix/var/nix
cp -RP "$tmp/$name/store/"* /nix/store/
nix="$(ls -d /nix/store/*-nix-"$VERSION")"
"$nix/bin/nix-store" --load-db < "$tmp/$name/.reginfo"
# A root, as the installer's profile would be: the tests collect the store, and
# an unrooted Nix would be the first thing collected.
mkdir -p /nix/var/nix/gcroots
ln -sfn "$nix" /nix/var/nix/gcroots/nix
ln -sf "$nix/bin/nix" /usr/local/bin/nix
rm -rf "$tmp"
nix --version
