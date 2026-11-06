#!/usr/bin/env python3
"""Fail if any relative link or #anchor in the Markdown docs is broken. Used by `make docs-check` and CI."""
import glob
import os
import re
import sys


def strip_code(text):
    return re.sub(r"```.*?```", "", text, flags=re.S)


def anchors(path):
    """GitHub-style heading anchors for a Markdown file."""
    out = set()
    for heading in re.findall(r"^#{1,6} (.+)$", strip_code(open(path, encoding="utf-8").read()), re.M):
        out.add(re.sub(r"[^\w\- ]", "", heading.strip().lower()).replace(" ", "-"))
    return out


def main():
    files = sorted(set(glob.glob("*.md") + glob.glob("docs/**/*.md", recursive=True)))
    broken = checked = 0
    for f in files:
        for link in re.findall(r"\]\(([^)\s]+)\)", strip_code(open(f, encoding="utf-8").read())):
            if link.startswith(("http://", "https://", "mailto:")):
                continue
            checked += 1
            path, _, frag = link.partition("#")
            target = os.path.normpath(os.path.join(os.path.dirname(f), path)) if path else f
            if not os.path.exists(target):
                print(f"{f}: missing target {link}")
                broken += 1
            elif frag and target.endswith(".md") and frag not in anchors(target):
                print(f"{f}: no heading for #{frag} in {target}")
                broken += 1
    print(f"{checked} relative links checked in {len(files)} files, {broken} broken")
    return 1 if broken else 0


if __name__ == "__main__":
    sys.exit(main())
