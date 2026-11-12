#!/bin/sh
# Installs the ffmpeg a conversation gets from the packages tool, from the release
# ../Dockerfile pins, where the tests run: testenv/Dockerfile and CI both call this,
# so the scripts shipped skills carry are tested on what they will run on. Debian's
# is three major versions behind. Needs nix, from install-nix.sh.
set -eu
NIXPKGS=08d3a97586f6b6343b3b5e799980773629432610
export NIX_CONFIG='experimental-features = nix-command flakes
sandbox = false
build-users-group ='
out="$(nix build --no-link --print-out-paths "github:NixOS/nixpkgs/$NIXPKGS#ffmpeg_8-headless^bin")"
# A root: the tests collect the store, and this is not theirs to collect.
ln -sfn "$out" /nix/var/nix/gcroots/ffmpeg
ln -sf "$out/bin/ffmpeg" "$out/bin/ffprobe" /usr/local/bin/
ffmpeg -version | head -1
