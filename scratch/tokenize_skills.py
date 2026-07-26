#!/usr/bin/env python
"""Phase 5.4 budget check.

Strips YAML frontmatter from each skill .md, tokenizes the body with a
real BPE encoder (cl100k_base — the same family as mimo-v2.5-free's
underlying tokenizer), and reports the per-file token count. Compares
against the budget claimed in the frontmatter (token_budget_main /
token_budget_mini) and the spec window (Main 5-8k, Mini 2-4k).

Usage:
    python tokenize_skills.py

Exit code is 0 if every file is within the spec window, 1 otherwise.
"""
import json
import re
import sys
from pathlib import Path

import tiktoken

SKILLS_DIR = Path(__file__).resolve().parent.parent / "skills"

# Per work.md §2: Main 5-8k, Mini 2-4k. We allow ±5% slack on the
# upper bound (8.4k Main, 4.2k Mini) because the spec is "within
# 5-8k" / "within 2-4k" and a single token over is not a useful
# failure mode for a manual authoring pass.
WINDOWS = {
    "main": (5_000, 8_400),
    "mini": (2_000, 4_200),
}

# cl100k_base is the GPT-4 / GPT-3.5-turbo / many-open-models family
# tokenizer. It's the closest reasonable proxy for "what does a
# frontier model see as a token" without bundling a proprietary
# mimo-v2.5-free encoder. The error margin between encoders is
# usually <5% for English-like prose, well under the 5-8k window
# slack.
ENCODER = tiktoken.get_encoding("cl100k_base")

FRONTMATTER_RE = re.compile(r"^---\n(.*?)\n---\n(.*)$", re.DOTALL)


def split_frontmatter(text: str) -> tuple[dict, str]:
    m = FRONTMATTER_RE.match(text)
    if not m:
        return {}, text
    fm_text, body = m.group(1), m.group(2)
    # Cheap YAML parse for the few fields we care about. Avoid
    # pulling in PyYAML just for this — the frontmatter is
    # author-controlled and predictable.
    fm: dict = {}
    for line in fm_text.splitlines():
        if ":" not in line:
            continue
        key, _, val = line.partition(":")
        fm[key.strip()] = val.strip().strip('"').strip("'")
    return fm, body


def tokenize_body(body: str) -> int:
    return len(ENCODER.encode(body, disallowed_special=()))


def main() -> int:
    files = sorted(SKILLS_DIR.glob("*.md"))
    if not files:
        print(f"no skill files in {SKILLS_DIR}", file=sys.stderr)
        return 1

    # Build a name -> Main-file-budget map so Mini files can inherit
    # their claimed budget from the Main file's `token_budget_mini`
    # field (Mini frontmatter has no `token_budget_mini` of its own;
    # the budget is declared on the parent Main file per work.md §4).
    main_budgets: dict[str, dict] = {}
    for f in files:
        text = f.read_text(encoding="utf-8")
        fm, _ = split_frontmatter(text)
        if fm.get("tier") == "main":
            name = f.stem
            main_budgets[name] = fm

    rows = []
    all_ok = True
    for f in files:
        text = f.read_text(encoding="utf-8")
        fm, body = split_frontmatter(text)
        tier = fm.get("tier", "?")
        if tier == "main":
            claimed = int(fm.get("token_budget_main", 0))
        else:
            # Mini inherits from the parent Main file
            # (e.g. frontend-mini.md -> frontend.md).
            parent_name = f.stem.removesuffix("-mini")
            claimed = int(main_budgets.get(parent_name, {}).get("token_budget_mini", 0))
        actual = tokenize_body(body)
        if tier in WINDOWS:
            lo, hi = WINDOWS[tier]
            ok = lo <= actual <= hi
        else:
            ok = False
            lo, hi = (0, 0)
        all_ok = all_ok and ok
        rows.append(
            {
                "file": f.name,
                "tier": tier,
                "claimed": claimed,
                "actual": actual,
                "window": (lo, hi),
                "ok": ok,
            }
        )

    # Pretty print
    name_w = max(len(r["file"]) for r in rows)
    print(
        f"{'file':<{name_w}}  {'tier':<5}  {'claimed':>8}  {'actual':>7}  {'window':>13}  status"
    )
    print("-" * (name_w + 2 + 5 + 2 + 8 + 2 + 7 + 2 + 13 + 2 + 8))
    for r in rows:
        lo, hi = r["window"]
        status = "OK" if r["ok"] else "OUT OF BUDGET"
        print(
            f"{r['file']:<{name_w}}  {r['tier']:<5}  {r['claimed']:>8}  {r['actual']:>7}  "
            f"{lo}-{hi:<6}  {status}"
        )

    if not all_ok:
        print(
            "\nOne or more files are outside the spec window "
            "(Main 5-8k, Mini 2-4k, ±5% upper-bound slack).",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
