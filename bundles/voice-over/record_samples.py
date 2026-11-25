#!/usr/bin/env python3
"""Records what the review page plays when one of this machine's voices is picked.

Usage: python3 bundles/voice-over/record_samples.py

Each of Kokoro's voices in local.go says "Hello World 1 2 3" in its own
language, spoken the way local.go's Speak speaks a line and levelled the way
render.go's level levels one, so voices compare at one loudness. The MP3s go into samples.json, under
hexgrad/kokoro-82m, beside the other backends' samples, which are kept as they
are. Run it when kokoroVoices changes.

It runs the programs voice-over downloads into the user's cache, so render a
voice-over on this machine once first (VOICEOVER_BACKEND=local). It needs an
ffmpeg with libmp3lame on PATH. About a minute; it buys nothing.
"""
import base64
import glob
import json
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
SAMPLES = os.path.join(HERE, "samples.json")
MODEL = "hexgrad/kokoro-82m"
# By a voice name's first letter, as kokoroLanguages in local.go: the line it
# says, and the language espeak-ng reads it in (none for English, which is read
# from the lexicon of its accent).
LINES = {
    "a": ("Hello World 1 2 3", None),
    "b": ("Hello World 1 2 3", None),
    "e": ("Hola mundo 1 2 3", "es"),
    "f": ("Bonjour le monde 1 2 3", "fr"),
    "i": ("Ciao mondo 1 2 3", "it"),
    "p": ("Olá mundo 1 2 3", "pt-br"),
}
RATE = 24000
TARGET_DB = -20.0
TRIM = ("silenceremove=start_periods=1:start_threshold=-45dB,areverse,"
        "silenceremove=start_periods=1:start_threshold=-45dB,areverse")


def cache():
    if sys.platform == "darwin":
        return os.path.expanduser("~/Library/Caches/voice-over")
    return os.path.join(os.environ.get("XDG_CACHE_HOME") or os.path.expanduser("~/.cache"), "voice-over")


def voices():
    """kokoroVoices from local.go: each voice's name and speaker id."""
    src = open(os.path.join(HERE, "local.go")).read()
    block = re.search(r"var kokoroVoices = \[\]kokoroVoice\{\n(.*?)\n\}", src, re.S)
    if not block:
        sys.exit("local.go has no kokoroVoices")
    return [(name, int(sid)) for name, sid in re.findall(r'\{"(\w+)", (\d+)\}', block.group(1))]


def ffmpeg(*args, **kw):
    return subprocess.run(["ffmpeg", "-y", "-hide_banner", "-loglevel", "info", *args],
                          capture_output=True, check=True, **kw)


def main():
    root = cache()
    tts = glob.glob(os.path.join(root, "sherpa-onnx-v1.13.8-*", "bin", "sherpa-onnx-offline-tts"))
    kokoro = os.path.join(root, "kokoro-int8-multi-lang-v1_0")
    if not tts or not os.path.isdir(kokoro):
        sys.exit(f"no sherpa-onnx or Kokoro in {root}: render a voice-over with VOICEOVER_BACKEND=local once first")
    samples = json.load(open(SAMPLES))
    samples[MODEL] = {}
    with tempfile.TemporaryDirectory() as tmp:
        raw, trimmed = os.path.join(tmp, "raw.wav"), os.path.join(tmp, "trimmed.wav")
        for voice, sid in voices():
            line, lang = LINES[voice[0]]
            lexicon = "lexicon-gb-en.txt" if voice.startswith("b") else "lexicon-us-en.txt"
            subprocess.run([tts[0], f"--kokoro-model={kokoro}/model.int8.onnx",
                            f"--kokoro-voices={kokoro}/voices.bin", f"--kokoro-tokens={kokoro}/tokens.txt",
                            f"--kokoro-lexicon={kokoro}/{lexicon}", f"--kokoro-data-dir={kokoro}/espeak-ng-data",
                            f"--num-threads={min(os.cpu_count() or 1, 8)}", f"--sid={sid}",
                            *([f"--kokoro-lang={lang}"] if lang else []),
                            f"--output-filename={raw}", line], capture_output=True, check=True)
            ffmpeg("-i", raw, "-ac", "1", "-ar", str(RATE), "-af", TRIM, trimmed)
            report = ffmpeg("-i", trimmed, "-af", "volumedetect", "-f", "null", "-", text=True).stderr
            mean = re.search(r"mean_volume: (-?[\d.]+) dB", report)
            gain = TARGET_DB - float(mean.group(1)) if mean else 0.0
            mp3 = ffmpeg("-i", trimmed, "-af", f"volume={gain:.2f}dB,alimiter=limit=0.84:level=0",
                         "-ac", "1", "-ar", str(RATE), "-c:a", "libmp3lame", "-b:a", "48k",
                         "-id3v2_version", "0", "-f", "mp3", "-").stdout
            samples[MODEL][voice] = base64.b64encode(mp3).decode()
            print(f"{sid:2} {voice:12} {len(mp3) / 1024:5.1f} KB", flush=True)
    with open(SAMPLES, "w", encoding="utf-8") as f:
        json.dump(samples, f, indent=0, sort_keys=True)
    print(f"{SAMPLES}: {os.path.getsize(SAMPLES) / 1e3:.0f} KB")


if __name__ == "__main__":
    main()
