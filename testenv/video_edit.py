"""Drive edit_video.py on videos whose every second can be checked: colour says which frame this is."""
import json, os, re, shutil, subprocess, sys, tempfile, time

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
SK = ROOT + "/skills/video-edit/scripts/edit_video.py"
W = tempfile.mkdtemp(prefix="video-edit-")
os.chdir(W)
bad = []


def sh(*a):
    return subprocess.run(a, capture_output=True, text=True)


def ff(*a):
    r = sh("ffmpeg", "-y", "-hide_banner", "-loglevel", "error", *a)
    if r.returncode:
        sys.exit("setup failed: " + r.stderr[-300:])


def report(label, good, detail=""):
    print(("ok  " if good else "BAD ") + label + ("" if good else "  " + str(detail)[:200]))
    if not good:
        bad.append(label)


def bands(name, parts, size="640x360", audio=True, extra=()):
    """A video of solid colours: [(colour, seconds)], at 30 fps, with a tone if asked."""
    args = []
    for colour, d in parts:
        args += ["-f", "lavfi", "-i", f"color=c={colour}:s={size}:r=30:d={d}"]
    total = sum(d for _, d in parts)
    if audio:
        args += ["-f", "lavfi", "-i", f"sine=f=440:d={total}"]
    chain = "".join(f"[{i}:v]" for i in range(len(parts))) + f"concat=n={len(parts)}:v=1:a=0[v]"
    args += ["-filter_complex", chain, "-map", "[v]"]
    if audio:
        args += ["-map", f"{len(parts)}:a", "-c:a", "aac", "-ar", "48000", "-ac", "2"]
    args += ["-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "300", "-preset", "ultrafast", *extra, name]
    ff(*args)


def colour_at(path, t):
    r = subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-ss", str(t), "-i", path, "-frames:v", "1",
                        "-vf", "scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-"], capture_output=True)
    return tuple(r.stdout[:3])


def name_of(rgb):
    if not rgb:
        return None
    r, g, b = rgb
    if r > 150 and g < 90 and b < 90:
        return "red"
    if g > 90 and r < 90 and b < 90:
        return "green"
    if b > 150 and r < 90 and g < 90:
        return "blue"
    if r > 150 and g > 150 and b < 90:
        return "yellow"
    if r < 40 and g < 40 and b < 40:
        return "black"
    return str(rgb)


def info(path):
    d = json.loads(sh("ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", path).stdout)
    v = next(s for s in d["streams"] if s["codec_type"] == "video")
    a = next((s for s in d["streams"] if s["codec_type"] == "audio"), None)
    return dict(length=float(d["format"]["duration"]), w=v["width"], h=v["height"], audio=a,
                alen=float(a["duration"]) if a and "duration" in a else None, vlen=float(v.get("duration", 0)), codec=v["codec_name"], pix=v.get("pix_fmt"))


def run(plan, *flags, out="edited.mp4"):
    json.dump(plan, open("edit.json", "w"))
    return sh("python3", SK, "--out", out, *flags)


def refuses(label, plan, want, *flags):
    r = run(plan, *flags)
    report("refuses " + label, r.returncode != 0 and want in (r.stdout + r.stderr), (r.returncode, r.stdout[-120:], r.stderr[-200:]))


def expect(label, out, length, at):
    i = info(out)
    got = {t: name_of(colour_at(out, t)) for t in at}
    good = abs(i["length"] - length) < 0.25 and got == at
    if i["alen"]:
        good = good and abs(i["alen"] - i["vlen"]) < 0.2
    report(label, good, (i["length"], got, i["alen"], i["vlen"]))


bands("a.mov", [("red", 5), ("green", 5), ("blue", 10)])
bands("c.mov", [("yellow", 6)])
bands("b.mov", [("yellow", 10)], size="1280x720", audio=False)
bands("it's a test.mov", [("blue", 4)])

# exact cuts: the colour must change on the very boundary, though the source has one keyframe in ten seconds
r = run({"clips": [{"file": "a.mov", "cut": [["0:05", "0:10"]]}]})
report("a cut runs", r.returncode == 0, r.stderr)
expect("cut out the middle, to the frame", "edited.mp4", 15, {4.8: "red", 5.2: "blue", 14.5: "blue"})
report("source unchanged", abs(info("a.mov")["length"] - 20) < 0.1)
report("parts cleaned up", not os.path.exists("edit-parts"))

r = run({"clips": [{"file": "a.mov", "keep": [[1, 2], ["12", "14"]]}]})
expect("keep two ranges", "edited.mp4", 3, {0.5: "red", 1.5: "blue", 2.8: "blue"})

r = run({"clips": [{"file": "a.mov", "cut": [["0:00", "0:05"], ["0:10", "end"]]}]})
expect("cut both ends", "edited.mp4", 5, {0.3: "green", 4.5: "green"})

r = run({"clips": [{"file": "a.mov", "cut": [[2, 6], [4, 8]]}]})
expect("overlapping cuts merge", "edited.mp4", 14, {1.5: "red", 2.5: "green", 4.5: "blue"})

# a join of mismatched clips: fits inside the first, silent where there is no sound
r = run({"clips": [{"file": "a.mov"}, {"file": "b.mov"}]})
report("mismatched join says so", "Sizes differ" in r.stdout and "plays silently" in r.stdout, r.stdout)
i = info("edited.mp4")
report("output is the first clip's size, with audio", (i["w"], i["h"]) == (640, 360) and i["audio"], i)
expect("mismatched join", "edited.mp4", 30, {4.5: "red", 19.5: "blue", 20.5: "yellow", 29.5: "yellow"})

r = run({"clips": [{"file": "b.mov"}, {"file": "a.mov"}]})
i = info("edited.mp4")
report("joins at the first clip's size", (i["w"], i["h"]) == (1280, 720), i)
expect("a small clip goes second", "edited.mp4", 30, {9.5: "yellow", 10.5: "red"})

bands("tall.mov", [("green", 4)], size="360x640")
r = run({"clips": [{"file": "a.mov", "keep": [[0, 2]]}, {"file": "tall.mov"}]})
report("tall clip joins", r.returncode == 0, r.stderr)
mid = subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-ss", "3", "-i", "edited.mp4", "-frames:v", "1", "-vf", "crop=20:20:310:170,scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-"], capture_output=True).stdout
report("a tall clip sits in the middle", name_of(tuple(mid[:3])) == "green" and info("edited.mp4")["w"] == 640, tuple(mid[:3]))
edge = subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-ss", "3", "-i", "edited.mp4", "-frames:v", "1", "-vf", "crop=20:20:10:170,scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-"], capture_output=True).stdout
report("the bars are black", name_of(tuple(edge[:3])) == "black", tuple(edge[:3]))

# a join of matching clips is lossless and immediate
t0 = time.time()
r = run({"clips": [{"file": "a.mov"}, {"file": "c.mov"}]})
took = time.time() - t0
report("matching join says it copies", "no re-encoding" in r.stdout, r.stdout)
report("matching join is quick", took < 3, took)
expect("matching join", "edited.mp4", 26, {19.5: "blue", 20.5: "yellow"})
def frames(path):
    return sh("ffmpeg", "-v", "error", "-i", path, "-map", "0:v", "-frames:v", "600", "-f", "md5", "-").stdout.strip()
report("a copied join keeps every frame of the original", frames("a.mov") == frames("edited.mp4") != "", (frames("a.mov"), frames("edited.mp4")))

# two screen recordings alike in size, codec, and timescale but not in frame rate: copied, the second
# lost two of its frames, which at 2 a second left the picture a second short (1.45 s in a real run)
def rated(name, fps, d):
    ff("-f", "lavfi", "-i", f"testsrc2=s=320x240:r={fps}:d={d}", "-f", "lavfi", "-i", f"sine=f=440:d={d}",
       "-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ar", "48000",
       "-ac", "2", "-video_track_timescale", "600", name)


rated("fast.mov", 60, 20)
rated("slow.mov", 2, 30)
r = run({"clips": [{"file": "fast.mov"}, {"file": "slow.mov"}]})
report("clips at different frame rates re-encode", r.returncode == 0 and "Re-encoded" in r.stdout, (r.stdout[-200:], r.stderr[-200:]))
expect("sound and picture stay together across frame rates", "edited.mp4", 50, {})

# a result out of step is never left where it could be handed over
if os.path.exists("edited.mp4"):
    os.remove("edited.mp4")
r = sh("python3", "-B", "-c", f"import sys; sys.path.insert(0, {os.path.dirname(SK)!r}); sys.argv = ['edit_video.py']; "
       "import edit_video; edit_video.can_copy = lambda clips: True; edit_video.main()")
report("a join out of step is removed, not handed over", r.returncode != 0 and "audio runs" in r.stderr and not os.path.exists("edited.mp4"),
       (r.returncode, r.stderr[-200:], os.path.exists("edited.mp4")))

# a name with a space and an apostrophe, as a screen recording's name has
r = run({"clips": [{"file": "it's a test.mov", "keep": [[1, 3]]}, {"file": "a.mov", "keep": [[0, 1]]}]})
expect("awkward file names", "edited.mp4", 3, {1.5: "blue", 2.5: "red"})

# variable frame rate, which every screen recording has: frames only when the screen changes
ff("-i", "a.mov", "-vf", "select='lt(n\\,20)+not(mod(n\\,7))'", "-fps_mode", "vfr", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-g", "300", "-preset", "ultrafast", "-c:a", "copy", "vfr.mov")
r = run({"clips": [{"file": "vfr.mov", "cut": [[5, 10]]}]})
report("variable frame rate runs", r.returncode == 0, r.stderr)
expect("variable frame rate keeps its time", "edited.mp4", 15, {4.5: "red", 5.5: "blue"})

# a clip rotated by metadata, as a phone's is
ff("-display_rotation", "90", "-i", "tall.mov", "-c", "copy", "rot.mp4")
r = run({"clips": [{"file": "rot.mp4", "keep": [[0, 2]]}]})
report("a rotated clip runs", r.returncode == 0, r.stderr)

# pieces: a job stops, keeps what it did, and carries on; nothing is done twice
bands("long.mov", [("red", 130), ("blue", 130)], size="160x90")
plan = {"clips": [{"file": "long.mov", "cut": [[125, 135]]}]}
r = run(plan, "--budget", "0")
report("a long job stops by itself", r.returncode == 3 and "Run the same command again" in r.stdout, (r.returncode, r.stdout[-160:], r.stderr[-100:]))
made = sorted(f for f in os.listdir("edit-parts") if f.endswith(".mkv"))
report("it keeps what it did", made == ["0000.mkv"], made)
stamp = os.path.getmtime("edit-parts/0000.mkv")
runs = 1
while r.returncode == 3 and runs < 12:
    r = run(plan, "--budget", "0")
    runs += 1
report("rerunning finishes it", r.returncode == 0 and runs > 2, (runs, r.returncode, r.stderr))
expect("a job in pieces", "edited.mp4", 250, {124.5: "red", 125.5: "blue", 249: "blue"})
gaps = sh("ffmpeg", "-hide_banner", "-i", "edited.mp4", "-af", "silencedetect=noise=-50dB:d=0.05", "-f", "null", "-").stderr
report("no silent gap in the sound where pieces meet", "silence_start" not in gaps, gaps[-200:])

r = run(plan, "--budget", "0")
os.remove("edited.mp4")
r = run({"clips": [{"file": "long.mov", "cut": [[0, 10]]}]}, "--budget", "0")
r = run({"clips": [{"file": "long.mov", "keep": [[0, 3]]}]})
report("a changed plan starts clean", r.returncode == 0 and abs(info("edited.mp4")["length"] - 3) < 0.2, r.stderr)

# what it refuses, and --check changes nothing
refuses("a time past the end", {"clips": [{"file": "a.mov", "cut": [["0:15", "0:25"]]}]}, "past its end")
refuses("a backwards range", {"clips": [{"file": "a.mov", "cut": [[5, 2]]}]}, "must end after")
refuses("a missing file", {"clips": [{"file": "gone.mov"}, {"file": "a.mov"}]}, "not a file")
refuses("cut and keep together", {"clips": [{"file": "a.mov", "cut": [[0, 1]], "keep": [[2, 3]]}]}, "use one")
refuses("nothing left", {"clips": [{"file": "a.mov", "cut": [["0:00", "end"]]}]}, "nothing of it is left")
refuses("one clip and no change", {"clips": [{"file": "a.mov"}]}, "nothing to do")
refuses("a bad time", {"clips": [{"file": "a.mov", "cut": [["soon", "later"]]}]}, "is not a time")
refuses("no clips", {"clips": []}, "needs \"clips\"")
r = run({"clips": [{"file": "a.mov", "cut": [[1, 2]]}]}, out="a.mov")
report("refuses to overwrite a source", r.returncode != 0 and "never changed" in r.stderr, r.stderr)
report("the source survived", abs(info("a.mov")["length"] - 20) < 0.1)

before = set(os.listdir("."))
r = run({"clips": [{"file": "a.mov", "cut": [[1, 2]]}]}, "--check", out="check.mp4")
report("--check shows the plan", r.returncode == 0 and "keep 0:00.00 to 0:01.00" in r.stdout and "will run" in r.stdout, r.stdout + r.stderr)
report("--check makes nothing", set(os.listdir(".")) - before <= {"edit.json"}, set(os.listdir(".")) - before)

ff("-f", "lavfi", "-i", "color=c=red:s=64x36:r=1:d=7500", "-c:v", "libx264", "-preset", "ultrafast", "huge.mp4")
refuses("over two hours without asking", {"clips": [{"file": "huge.mp4", "cut": [[0, 1]]}]}, "too long to start")
r = run({"clips": [{"file": "huge.mp4", "cut": [[0, 1]]}]}, "--check", "--long")
report("--long lets it through", r.returncode == 0, r.stderr)

print("\n%d problems" % len(bad), bad)
os.chdir(ROOT)
shutil.rmtree(W)
sys.exit(1 if bad else 0)
