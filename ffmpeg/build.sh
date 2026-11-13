#!/bin/sh
# Builds the ffmpeg the voice-over bundle carries: one program, from ffmpeg's
# own source at a pinned version, with only what the bundle asks of it and no
# library from outside ffmpeg. Nothing GPL and nothing non-free is enabled, so
# the result is under ffmpeg's LGPL 2.1 or later alone (see NOTICE beside this).
#
# Usage: ffmpeg/build.sh OUT    writes OUT/ffmpeg, OUT/LICENSE.ffmpeg.txt and
#                               OUT/SOURCE.ffmpeg.txt, and keeps the source it
#                               was built from as OUT/src/ffmpeg-<version>.tar.xz
#                               (cmd/pack -bin carries OUT's files, not src/)
# On Linux it links statically, so the program runs on any Linux of that
# architecture; on macOS it uses only the system's own libraries.
set -eu

VERSION=8.1.3
# The sha256 of https://ffmpeg.org/releases/ffmpeg-8.1.3.tar.xz, as downloaded
# on 2026-11-13.
SHA256=7138d28c96d9d3e3af4ee3d8cad72741f8ffb40da90c1112235dea3ecd3178a3

out=$(mkdir -p "$1" && cd "$1" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

tarball="ffmpeg-$VERSION.tar.xz"
src="$out/src/$tarball"
mkdir -p "$out/src"
if [ ! -f "$src" ]; then
  curl -fsSL "https://ffmpeg.org/releases/$tarball" -o "$src"
fi
if command -v sha256sum >/dev/null; then got=$(sha256sum "$src" | cut -d' ' -f1)
else got=$(shasum -a 256 "$src" | cut -d' ' -f1); fi
if [ "$got" != "$SHA256" ]; then
  echo "$tarball has sha256 $got, want $SHA256" >&2
  exit 1
fi
tar -xJf "$src" -C "$work"

# Raw samples are the format s16le, but configure names its muxer and demuxer
# pcm_s16le.
# What the bundle reads: the containers and codecs of phone and screen
# recordings, the speech the providers send (MP3, WAV), and its own files.
demuxers=mov,matroska,avi,mpegts,flv,mp3,wav,aiff,caf,aac,ogg,flac,amr,pcm_s16le,image2,mpegps
decoders=h264,hevc,vp8,vp9,mpeg4,mpeg2video,mjpeg,prores,rawvideo,aac,aac_latm,mp3float,mp3,opus,vorbis,flac,alac,ac3,eac3,amrnb,amrwb,pcm_s16le,pcm_s16be,pcm_s24le,pcm_s24be,pcm_s32le,pcm_f32le,pcm_f32be,pcm_mulaw,pcm_alaw,adpcm_ima_qt
parsers=h264,hevc,vp8,vp9,mpeg4video,mpegvideo,mjpeg,aac,aac_latm,mpegaudio,opus,vorbis,flac,ac3
# What it writes: AAC for the sound it sends and the voice-over itself, PCM
# for the track, JPEG for frames, and the containers they go in. The picture
# is always copied, so no video encoder.
encoders=aac,pcm_s16le,mjpeg,wrapped_avframe
muxers=mp4,ipod,mov,wav,pcm_s16le,image2,null
# Rotation (a phone held upright) is applied with transpose and the flips.
filters=scale,format,aformat,aresample,anull,null,copy,transpose,hflip,vflip,silenceremove,areverse,volumedetect,volume,alimiter,apad
bsfs=aac_adtstoasc,h264_mp4toannexb,hevc_mp4toannexb,extract_extradata,vp9_superframe,null

set -- --prefix="$work/install" \
  --disable-everything --disable-autodetect --disable-network --disable-doc --disable-debug \
  --disable-ffplay --disable-ffprobe --disable-avdevice --disable-videotoolbox --disable-audiotoolbox --enable-small \
  --enable-protocol=file,pipe \
  --enable-demuxer="$demuxers" --enable-decoder="$decoders" --enable-parser="$parsers" \
  --enable-encoder="$encoders" --enable-muxer="$muxers" --enable-filter="$filters" --enable-bsf="$bsfs" \
  --enable-swscale --enable-swresample
if [ "$(uname -s)" = Linux ]; then
  set -- "$@" --extra-ldflags=-static --pkg-config-flags=--static
fi
configured="$*"

cd "$work/ffmpeg-$VERSION"
./configure "$@" >"$work/configure.log" 2>&1 || { tail -30 "$work/configure.log" >&2; exit 1; }
if command -v nproc >/dev/null; then jobs=$(nproc); else jobs=$(sysctl -n hw.ncpu); fi
make -j"$jobs" ffmpeg >"$work/make.log" 2>&1 || { tail -30 "$work/make.log" >&2; exit 1; }
strip ffmpeg
cp ffmpeg "$out/ffmpeg"
cp COPYING.LGPLv2.1 "$out/LICENSE.ffmpeg.txt"
chmod 755 "$out/ffmpeg"
chmod 644 "$out/LICENSE.ffmpeg.txt"
{
  echo "This ffmpeg is FFmpeg $VERSION, under the GNU Lesser General Public License,"
  echo "version 2.1 or later (LICENSE.ffmpeg.txt). It is built with no library from"
  echo "outside FFmpeg, and nothing under the GPL or non-free is enabled."
  echo
  echo "Its source is $tarball, published beside this bundle in the same release,"
  echo "and at https://ffmpeg.org/releases/$tarball (sha256 $SHA256)."
  echo "It was built by ffmpeg/build.sh in https://github.com/JonasPf/agent-plugins,"
  echo "unchanged, configured with:"
  echo
  echo "  ./configure $configured" | sed "s|$work/install|PREFIX|"
} >"$out/SOURCE.ffmpeg.txt"
chmod 644 "$out/SOURCE.ffmpeg.txt"
"$out/ffmpeg" -hide_banner -version | head -1
