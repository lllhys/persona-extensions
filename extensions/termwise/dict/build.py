#!/usr/bin/env python3
"""Rebuild dict/lexicon.json.gz from an ECDICT CSV (stdlib only).

Usage: ECDICT_CSV=/path/to/ecdict.csv python3 dict/build.py

The CSV is the upstream file from https://github.com/skywind3000/ECDICT
(MIT, Copyright (c) 2025 Linwei). This script does not download it.
Inflection handling (0: and p/d/i/3/s/r/t) follows https://github.com/s546126/termwise.
"""

import csv
import gzip
import io
import json
import os
import re
import sys

WORD = re.compile(r"^[A-Za-z][A-Za-z'-]{1,32}$")
BASIC = 5000
HERE = os.path.dirname(os.path.abspath(__file__))


def first(value, limit):
    text = (value or "").replace("\\n", "\n").replace("\\r", "\n").strip()
    if not text:
        return ""
    text = re.sub(r"\s+", " ", text.splitlines()[0].strip())
    if len(text) > limit:
        text = text[: limit - 1].rstrip() + "…"
    return text


def number(row, key):
    try:
        return int(row.get(key) or 0)
    except ValueError:
        return 0


def lemma_of(exchange, word):
    for part in (exchange or "").split("/"):
        if part.startswith("0:"):
            lemma = part[2:].strip().lower()
            if lemma and lemma != word:
                return lemma
    return ""


def keep(entry):
    rank = entry["frq"]
    if rank and rank <= BASIC:
        return False
    if BASIC < rank <= 20000:
        return True
    if entry["collins"] >= 1 and (rank == 0 or rank > BASIC):
        return True
    if entry["oxford"] == 1 and (rank == 0 or rank > BASIC):
        return True
    return False


def main():
    path = os.environ.get("ECDICT_CSV")
    if not path:
        sys.exit("set ECDICT_CSV to ecdict.csv")
    csv.field_size_limit(10**7)
    rows = {}
    with open(path, newline="", encoding="utf-8") as handle:
        for row in csv.DictReader(handle):
            word = (row.get("word") or "").strip().lower()
            gloss = first(row.get("translation") or "", 48)
            if not WORD.match(word) or not gloss:
                continue
            rows[word] = {
                "frq": number(row, "frq"),
                "collins": number(row, "collins"),
                "oxford": number(row, "oxford"),
                "tr": gloss,
                "en": first(row.get("definition") or "", 72),
                "ex": row.get("exchange") or "",
            }
    lemmas = {w: e for w, e in rows.items() if not lemma_of(e["ex"], w) and keep(e)}
    forms = {}
    for word, entry in rows.items():
        lemma = lemma_of(entry["ex"], word)
        if lemma in lemmas and not (entry["frq"] and entry["frq"] <= BASIC):
            forms[word] = lemma
    for word, entry in lemmas.items():
        for part in entry["ex"].split("/"):
            if ":" not in part:
                continue
            kind, form = part.split(":", 1)
            form = form.strip().lower()
            if kind not in {"p", "d", "i", "3", "r", "t", "s"} or not WORD.match(form):
                continue
            if form in lemmas or form in forms:
                continue
            surface = rows.get(form)
            if surface and surface["frq"] and surface["frq"] <= BASIC:
                continue
            forms[form] = word
    forms = {k: v for k, v in forms.items() if v in lemmas and k not in lemmas}
    payload = {
        "name": "ecdict-subset",
        "source": "https://github.com/skywind3000/ECDICT",
        "license": "MIT",
        "copyright": "Copyright (c) 2025 Linwei",
        "basic_frq_max": BASIC,
        "lemmas": {w: {"z": e["tr"], "e": e["en"]} for w, e in sorted(lemmas.items())},
        "forms": dict(sorted(forms.items())),
    }
    raw = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode()
    buf = io.BytesIO()
    with gzip.GzipFile(fileobj=buf, mode="wb", mtime=0) as gz:
        gz.write(raw)
    out = os.path.join(HERE, "lexicon.json.gz")
    with open(out, "wb") as handle:
        handle.write(buf.getvalue())
    print(f"lemmas {len(lemmas)} forms {len(forms)} gzip {buf.tell()} -> {out}")


if __name__ == "__main__":
    main()
