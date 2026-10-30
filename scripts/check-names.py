"""Fails if names that must not appear in this repository appear in any tracked text file.

The names are stored only as SHA-256 hashes, so this file does not contain them either.
Each word (letters and digits) is split at camelCase and letter/digit boundaries. Its parts,
runs of adjacent parts, and pairs of neighbouring words are lower-cased and hashed, so
"FooBar", "foo-bar", "foo bar" and "foo_bar" all count as "foobar".
"word" entries match only a whole word (short names that also occur inside hex strings).

    python3 scripts/check-names.py            # check tracked files
    python3 scripts/check-names.py --hash X   # print the hash line for a new name
"""

import hashlib
import re
import subprocess
import sys

FORBIDDEN = [
    ("part", "99e829f578dd2b61e7c85c62b973ecfb87704888ce4c769eb60f1ae1083dca15"),
    ("part", "2b11985dc820cd172f8de46869f601a49ecf49d18b6d7113537b66dabb4ac57d"),
    ("word", "601c218b06bd8acf858fae088f77b00c4bd04c67966215229cf838a692889f5c"),
    ("part", "0dbe5eed7bf15a420a2a6a7356654aea49eb6c506d544ae1460ff41dc840a100"),
    ("part", "42acb9b16f5ede7a31bd611579b16a53943e6e11305372951b24633e6453d926"),
    ("part", "daf637ed8a929ffdf9e54a2749b858761d3e4b89c73bb4a3129b1c4cb08e83b3"),
    ("part", "2ab871c9fd15d13fa3f4552baec2c2228ce79e120eb0c530eea4f0b2141787ee"),
    ("part", "4e4e96713df8b898ed7cfd1ac00f57e923b8a7a789b09702aac835cf25bfc8ef"),
    ("part", "487537935c922e003caf22a9e8b0108e8b13ff4b350a2a2e83e290c48314cd3c"),
]
PART = {h for m, h in FORBIDDEN if m == "part"}
WORD = {h for m, h in FORBIDDEN if m == "word"}

WORD_RE = re.compile(r"[A-Za-z0-9]+")
PART_RE = re.compile(r"[A-Z]+(?![a-z])|[A-Z]?[a-z]+|[0-9]+")
HEXLIKE = re.compile(r"[0-9a-fA-F]{12,}")


def h(s):
    return hashlib.sha256(s.lower().encode()).hexdigest()


def hits(line):
    words = WORD_RE.findall(line)
    for i, w in enumerate(words):
        if len(w) > 40 or HEXLIKE.fullmatch(w):
            continue
        if h(w) in WORD or h(w) in PART:
            return True
        parts = PART_RE.findall(w)
        for a in range(len(parts)):
            for b in range(a + 1, min(len(parts), a + 4) + 1):
                if h("".join(parts[a:b])) in PART:
                    return True
        if i + 1 < len(words) and h(w + words[i + 1]) in PART:
            return True
    return False


def main():
    if len(sys.argv) == 3 and sys.argv[1] == "--hash":
        print(f'    ("part", "{h(re.sub(r"[^A-Za-z0-9]", "", sys.argv[2]))}"),')
        return 0
    files = subprocess.run(["git", "ls-files", "-z"], capture_output=True, check=True).stdout.decode().split("\0")
    bad = 0
    for f in filter(None, files):
        try:
            raw = open(f, "rb").read()
        except OSError:
            continue
        if b"\0" in raw[:8192]:  # binary
            continue
        for n, line in enumerate(raw.decode("utf-8", "replace").splitlines(), 1):
            if hits(line):
                print(f"{f}:{n}: forbidden name", file=sys.stderr)
                bad += 1
    if bad:
        print(f"check-names: {bad} line(s) with forbidden names", file=sys.stderr)
        return 1
    print("check-names: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
