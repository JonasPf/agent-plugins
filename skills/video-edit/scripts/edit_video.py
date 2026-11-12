#!/usr/bin/env python3
"""Cut parts out of videos and join them, as edit.json says.

Usage: edit_video.py --check [edit.json]    show the plan, change nothing
       edit_video.py [edit.json]            make edited.mp4
       options: --out NAME.mp4, --long (the operator agreed to a very long job)

edit.json lists the clips in the order they should play. A clip gives what to
remove with "cut", or what to keep with "keep", or neither, to use all of it:

  {"clips": [
    {"file": "first.mov", "cut": [["1:20", "2:05"], ["9:30", "end"]]},
    {"file": "second.mov", "keep": [["0:10", "3:00"]]}
  ]}

Times are seconds, m:ss, or h:mm:ss, with decimals if needed, and "end".

Cuts are exact, so they re-encode, at a steady 30 frames a second. Without
re-encoding a cut can only land on a keyframe, and in a screen recording those can
be 18 seconds apart. A screen recording also stores a frame only when the screen
changes, which lets sound and picture drift apart wherever two parts meet; a
steady rate makes each part exactly as long as it says. Joining whole clips that
match, frame rate included, is copied, losslessly and in moments. Sources are never
changed, and a result whose sound and picture come out apart is removed.

The work is done in pieces of under two minutes, kept in edit-parts/ until the
end. A command is stopped after 15 minutes, so a long job stops by itself first
and exits 3; running the same command again carries on where it stopped.
Requires ffmpeg and ffprobe on PATH (packages tool, ffmpeg_8-headless).
"""
import argparse, hashlib, json, os, re, shutil, subprocess, sys, time

PARTS = "edit-parts"
PIECE_SECONDS = 110
BUDGET_SECONDS = 780
MORE_TO_DO = 3
# Measured on the server: 1832x1522 at veryfast crf 20 ran at 2.7 times real time,
# with a peak of 370 MB. Other sizes are scaled from that, and capped.
SPEED_AT = (2.7, 1832 * 1522)
SPEED_CAP = 6.0
MAX_MINUTES = 120
WARN_ENCODE_MINUTES = 13
SPARE_DISK = 2 << 30
TOLERANCE = 0.5
FRAMES_PER_SECOND = 30
TIME = re.compile(r"^(?:(?:(\d+):)?(\d{1,2}):)?(\d+(?:\.\d+)?)$")

def fail(message):
    sys.exit(message)

def tool(name):
    exe = shutil.which(name)
    if not exe:
        fail(f"{name} not found on PATH: install ffmpeg_8-headless with the packages tool")
    return exe

def seconds(value, length):
    """A time as written in edit.json, in seconds."""
    if isinstance(value, (int, float)):
        return float(value)
    if str(value).strip().lower() == "end":
        return length
    m = TIME.match(str(value).strip())
    if not m:
        fail(f'"{value}" is not a time: write seconds, m:ss, h:mm:ss, or end')
    return int(m.group(1) or 0) * 3600 + int(m.group(2) or 0) * 60 + float(m.group(3))

def clock(t):
    m, s = divmod(t, 60)
    h, m = divmod(int(m), 60)
    return f"{h}:{m:02d}:{s:05.2f}" if h else f"{m}:{s:05.2f}"

def probe(path):
    if not os.path.isfile(path):
        fail(f"{path} is not a file in the working directory")
    r = subprocess.run([tool("ffprobe"), "-v", "error", "-show_streams", "-show_format", "-of", "json", path],
                       capture_output=True, text=True)
    try:
        info = json.loads(r.stdout)
        video = next(s for s in info["streams"] if s["codec_type"] == "video")
        length = float(info["format"]["duration"])
    except (ValueError, KeyError, StopIteration):
        fail(f"{path} has no video that can be read")
    audio = next((s for s in info["streams"] if s["codec_type"] == "audio"), None)
    width, height = int(video["width"]), int(video["height"])
    for side in video.get("side_data_list", []):
        if abs(int(side.get("rotation", 0))) in (90, 270):
            width, height = height, width
    return dict(length=length, width=width, height=height, audio=audio,
                # Copied, a clip after one at another frame rate loses two of its frames.
                match=(video["codec_name"], width, height, video.get("pix_fmt"), video.get("time_base"),
                       video.get("r_frame_rate"), audio and audio["codec_name"], audio and audio.get("sample_rate"), audio and audio.get("channels")))

def ranges(clip, length):
    """What stays of a clip: a sorted list of (start, end)."""
    name = clip["file"]
    if "cut" in clip and "keep" in clip:
        fail(f"{name} has both cut and keep: use one")
    def read(pairs):
        out = []
        for pair in pairs:
            if not isinstance(pair, list) or len(pair) != 2:
                fail(f"{name}: each range is two times, from and to, not {pair}")
            a, b = seconds(pair[0], length), seconds(pair[1], length)
            if a >= b:
                fail(f"{name}: a range must end after it starts: {pair[0]} to {pair[1]}")
            if b > length + TOLERANCE:
                fail(f"{name}: {pair[1]} is past its end, {clock(length)}")
            out.append((a, min(b, length)))
        return sorted(out)
    def merged(pairs):
        out = []
        for a, b in pairs:
            if out and a <= out[-1][1]:
                out[-1] = (out[-1][0], max(out[-1][1], b))
            else:
                out.append((a, b))
        return out
    if "keep" in clip:
        return merged(read(clip["keep"]))
    kept, at = [], 0.0
    for a, b in merged(read(clip.get("cut", []))):
        if a > at:
            kept.append((at, a))
        at = b
    if at < length:
        kept.append((at, length))
    return kept

def load(path):
    if not os.path.isfile(path):
        fail(f"{path} not found: write the plan there first")
    try:
        plan = json.load(open(path, encoding="utf-8"))
    except ValueError as e:
        fail(f"{path} is not valid JSON: {e}")
    clips = plan.get("clips") if isinstance(plan, dict) else None
    if not clips:
        fail(f'{path} needs "clips", the videos in the order they should play')
    out = []
    for c in clips:
        if "file" not in c:
            fail(f"a clip has no file: {c}")
        info = probe(c["file"])
        spans = ranges(c, info["length"])
        if not spans:
            fail(f"{c['file']}: nothing of it is left")
        out.append(dict(file=c["file"], info=info, spans=spans,
                        edited="cut" in c or "keep" in c))
    if len(out) == 1 and not out[0]["edited"]:
        fail("one clip with nothing cut: there is nothing to do")
    return out

def can_copy(clips):
    """Whole clips that match can be joined as they are."""
    return not any(c["edited"] and c["spans"] != [(0.0, c["info"]["length"])] for c in clips) \
        and len({c["info"]["match"] for c in clips}) == 1

def pieces(clips):
    out = []
    for c in clips:
        for a, b in c["spans"]:
            while a < b - 0.001:
                d = min(PIECE_SECONDS, b - a)
                out.append(dict(file=c["file"], start=a, length=d, audio=bool(c["info"]["audio"])))
                a += d
    return out

def estimate(clips, kept):
    pixels = clips[0]["info"]["width"] * clips[0]["info"]["height"]
    speed = min(SPEED_CAP, SPEED_AT[0] * SPEED_AT[1] / pixels)
    return kept / speed / 60

def show(clips, copy, kept, out):
    for c in clips:
        print(f"{c['file']}  ({clock(c['info']['length'])}, {c['info']['width']}x{c['info']['height']}"
              f"{'' if c['info']['audio'] else ', no audio'})")
        for a, b in c["spans"]:
            print(f"  keep {clock(a)} to {clock(b)}  ({clock(b - a)})")
    print(f"{out} will run {clock(kept)}")
    if copy:
        print("Joined as they are: no re-encoding, no loss of quality, done in moments.")
    else:
        print(f"Re-encoded at {FRAMES_PER_SECOND} frames a second, about {max(estimate(clips, kept), 0.1):.1f} minutes of work.")
    sizes = {(c["info"]["width"], c["info"]["height"]) for c in clips}
    if len(sizes) > 1:
        print(f"Sizes differ: every clip is fitted inside {clips[0]['info']['width']}x{clips[0]['info']['height']}, "
              "the first clip's, with black bars where needed.")
    if any(c["info"]["audio"] for c in clips) and not all(c["info"]["audio"] for c in clips):
        print("A clip has no audio: it plays silently.")

def ffmpeg(*args):
    r = subprocess.run([tool("ffmpeg"), "-y", "-hide_banner", "-loglevel", "error", *args],
                       capture_output=True, text=True)
    if r.returncode != 0:
        fail(f"ffmpeg failed: {r.stderr.strip().splitlines()[-1] if r.stderr.strip() else 'unknown'}")

def encode(p, target, sound, path):
    """One piece at the shared size, with raw audio, so the joins make no gaps."""
    w, h = target[0] // 2 * 2, target[1] // 2 * 2
    # fps fills from the first frame if the source shows nothing new at the start.
    fit = (f"fps={FRAMES_PER_SECOND}:start_time=0,scale={w}:{h}:force_original_aspect_ratio=decrease,"
           f"pad={w}:{h}:(ow-iw)/2:(oh-ih)/2,setsar=1,format=yuv420p")
    args = ["-ss", f"{p['start']:.3f}", "-t", f"{p['length']:.3f}", "-i", p["file"]]
    if sound and not p["audio"]:
        args += ["-f", "lavfi", "-t", f"{p['length']:.3f}", "-i", "anullsrc=r=48000:cl=stereo"]
    args += ["-map", "0:v:0"]
    if sound:
        args += ["-map", "0:a:0" if p["audio"] else "1:a:0", "-af", "apad", "-c:a", "pcm_s16le", "-ar", "48000", "-ac", "2"]
    args += ["-vf", fit, "-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
             "-threads", "2", "-t", f"{p['length']:.3f}", path]
    ffmpeg(*args)

def listing(paths):
    with open(os.path.join(PARTS, "list.txt"), "w", encoding="utf-8") as f:
        for p in paths:
            quoted = os.path.abspath(p).replace("'", "'\\''")
            f.write(f"file '{quoted}'\n")
    return os.path.join(PARTS, "list.txt")

def prepare(work):
    """Start clean unless these are the pieces of the same plan."""
    stamp = hashlib.sha256(json.dumps(work, sort_keys=True).encode()).hexdigest()
    mark = os.path.join(PARTS, "plan")
    if not (os.path.isfile(mark) and open(mark).read() == stamp):
        shutil.rmtree(PARTS, ignore_errors=True)
        os.makedirs(PARTS)
        with open(mark, "w") as f:
            f.write(stamp)

def verify(out, kept, sound):
    def broken(message):
        os.remove(out)
        fail(f"{message}. Removed it: do not hand anything over")
    r = subprocess.run([tool("ffprobe"), "-v", "error", "-show_streams", "-show_format", "-of", "json", out],
                       capture_output=True, text=True)
    info = json.loads(r.stdout)
    got = float(info["format"]["duration"])
    if abs(got - kept) > TOLERANCE + 0.1:
        broken(f"{out} runs {clock(got)}, not the {clock(kept)} planned")
    lengths = {s["codec_type"]: float(s["duration"]) for s in info["streams"] if "duration" in s}
    if sound and "audio" not in lengths:
        broken(f"{out} has lost its audio")
    if "audio" in lengths and "video" in lengths and abs(lengths["audio"] - lengths["video"]) > TOLERANCE:
        broken(f"{out}: audio runs {lengths['audio']:.2f} s and video {lengths['video']:.2f} s")
    return got

def main():
    ap = argparse.ArgumentParser(usage=__doc__)
    ap.add_argument("plan", nargs="?", default="edit.json")
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--out", default="edited.mp4")
    ap.add_argument("--long", action="store_true")
    ap.add_argument("--budget", type=float, default=BUDGET_SECONDS, help=argparse.SUPPRESS)
    a = ap.parse_args()

    clips = load(a.plan)
    if a.out in {c["file"] for c in clips}:
        fail(f"{a.out} is one of the sources: write to another name, sources are never changed")
    kept = sum(b - x for c in clips for x, b in c["spans"])
    copy = can_copy(clips)
    show(clips, copy, kept, a.out)

    minutes = kept / 60
    if minutes > MAX_MINUTES and not a.long:
        fail(f"too long to start without asking: {minutes:.0f} minutes of video. Tell the operator; "
             "if they agree, run this again with --long.")
    if not copy and estimate(clips, kept) > WARN_ENCODE_MINUTES:
        print("Long: this takes more than one run. When it stops with exit code 3, run the same command again.")
    need = sum(os.path.getsize(c["file"]) for c in clips) * 2
    free = shutil.disk_usage(".").free
    if free < need + SPARE_DISK:
        fail(f"not enough disk: this needs about {need >> 20} MB and {free >> 20} MB is free, keeping 2 GB spare")
    if a.check:
        return

    sound = any(c["info"]["audio"] for c in clips)
    if copy:
        os.makedirs(PARTS, exist_ok=True)
        ffmpeg("-f", "concat", "-safe", "0", "-i", listing([c["file"] for c in clips]),
               "-map", "0", "-c", "copy", "-movflags", "+faststart", a.out)
    else:
        work = pieces(clips)
        target = (clips[0]["info"]["width"], clips[0]["info"]["height"])
        prepare(dict(work=work, target=target, sound=sound))
        done, began, encoded = [], time.time(), 0
        for i, p in enumerate(work):
            path = os.path.join(PARTS, f"{i:04d}.mkv")
            done.append(path)
            if os.path.exists(path):
                continue
            # Only after a piece, so that every run gets somewhere.
            if encoded and time.time() - began > a.budget:
                print(f"Stopped at piece {i + 1} of {len(work)} to stay inside the time limit. Run the same command again.")
                sys.exit(MORE_TO_DO)
            encode(p, target, sound, path + ".tmp.mkv")
            os.rename(path + ".tmp.mkv", path)
            encoded += 1
        args = ["-f", "concat", "-safe", "0", "-i", listing(done), "-map", "0", "-c:v", "copy"]
        if sound:
            args += ["-c:a", "aac", "-b:a", "192k"]
        ffmpeg(*args, "-movflags", "+faststart", a.out)
    got = verify(a.out, kept, sound)
    shutil.rmtree(PARTS, ignore_errors=True)
    print(f"wrote {a.out}: {clock(got)}, {os.path.getsize(a.out) / (1 << 20):.1f} MB")

if __name__ == "__main__":
    main()
