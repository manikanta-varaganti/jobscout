#!/usr/bin/env python3
"""Print the CHANGELOG section for a version (e.g. v0.1.0) so it can be used as GitHub release notes."""
import re
import sys


def main():
    if len(sys.argv) != 2:
        print("usage: release_notes.py vX.Y.Z", file=sys.stderr)
        return 2
    version = sys.argv[1].lstrip("v")
    text = open("CHANGELOG.md", encoding="utf-8").read()
    m = re.search(rf"^## \[{re.escape(version)}\][^\n]*\n(.*?)(?=^## \[|^\[[^\]]+\]: )", text, re.S | re.M)
    if not m or not m.group(1).strip():
        print(f"CHANGELOG.md has no entry for {version}", file=sys.stderr)
        return 1
    # Relative links work in the repo but not on the release page.
    body = re.sub(r"\]\((?!https?://|#)([^)]+)\)", r"](https://github.com/manikanta-varaganti/jobscout/blob/main/\1)", m.group(1).strip())
    print(body)
    print("\n---\n**Download:** pick the archive for your computer below "
          "(`darwin_arm64` = Apple-silicon Mac, `darwin_amd64` = Intel Mac, `windows_amd64` = Windows), "
          "unzip it and double-click `jobscout`. See the "
          "[User Guide](https://github.com/manikanta-varaganti/jobscout/blob/main/docs/user-guide.md#installing).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
