#!/usr/bin/env python3
"""Check repository Markdown links and heading anchors without network access.

  python3 -I scripts/check-docs.py
"""
import html
import pathlib
import re
import subprocess
import sys
import urllib.parse

ROOT = pathlib.Path(__file__).resolve().parent.parent
INLINE = re.compile(r'!?\[[^\]\n]*\]\(\s*(<[^>\n]*>|[^\s)]+)(?:\s+[^)]*)?\)')
DEFINITION = re.compile(r'^ {0,3}\[([^\]\n]+)\]:\s*(<[^>\n]*>|\S+)', re.MULTILINE)
REFERENCE = re.compile(r'!?\[([^\]\n]+)\]\[([^\]\n]*)\]')


def prose(text):
    """Exclude fenced code while keeping line numbers stable."""
    lines, fence = [], None
    for line in text.splitlines(keepends=True):
        match = re.match(r'^ {0,3}(`{3,}|~{3,})', line)
        if fence is None and match:
            fence = match[1]
        elif fence is not None:
            if re.match(r'^ {0,3}' + re.escape(fence[0]) + '{' + str(len(fence)) + r',}\s*$', line):
                fence = None
        else:
            lines.append(line)
            continue
        lines.append('\n' if line.endswith('\n') else '')
    return ''.join(lines)


def anchors(text):
    found, counts = set(), {}
    for title in re.findall(r'^ {0,3}#{1,6}\s+(.+?)\s*#*\s*$', prose(text), re.MULTILINE):
        title = html.unescape(re.sub(r'<[^>]+>', '', title)).lower()
        slug = re.sub(r'[^\w\- ]', '', title).replace(' ', '-')
        count = counts.get(slug, 0)
        counts[slug] = count + 1
        found.add(slug if count == 0 else '%s-%s' % (slug, count))
    found.update(re.findall(r'\b(?:id|name)=["\']([^"\']+)["\']', prose(text)))
    return found


def check(root, paths):
    root = root.resolve()
    errors, total = [], 0
    for source in paths:
        source = root / source
        text = re.sub(r'(`+)(.*?)\1', lambda m: re.sub(r'[^\n]', ' ', m[0]),
                      prose(source.read_text()), flags=re.DOTALL)
        definitions = {m[1].strip().lower(): m[2] for m in DEFINITION.finditer(text)}
        targets = [(m.start(), m[1]) for m in INLINE.finditer(text)]
        targets.extend((m.start(), m[2]) for m in DEFINITION.finditer(text))
        for match in REFERENCE.finditer(text):
            label = (match[2] or match[1]).strip().lower()
            if label not in definitions:
                errors.append('%s:%s: undefined reference [%s]' %
                              (source.relative_to(root), text.count('\n', 0, match.start()) + 1, label))
        for offset, target in targets:
            target = html.unescape(target.strip('<>'))
            url = urllib.parse.urlsplit(target)
            if url.scheme or url.netloc:
                continue
            total += 1
            location = '%s:%s' % (source.relative_to(root), text.count('\n', 0, offset) + 1)
            path = urllib.parse.unquote(url.path)
            dest = ((root / path.lstrip('/')) if path.startswith('/') else source.parent / path).resolve() if path else source
            if not dest.is_relative_to(root):
                errors.append('%s: link leaves repository: %s' % (location, target))
            elif not dest.exists():
                errors.append('%s: missing target: %s' % (location, target))
            elif url.fragment and dest.suffix.lower() == '.md' and urllib.parse.unquote(url.fragment) not in anchors(dest.read_text()):
                errors.append('%s: missing heading: %s' % (location, target))
    return errors, total


def main():
    files = subprocess.check_output(['git', '-C', str(ROOT), 'ls-files', '-z', '--cached', '--others', '--exclude-standard'])
    paths = sorted({path for path in files.decode().split('\0') if path.endswith('.md')})
    errors, total = check(ROOT, paths)
    for error in errors:
        print(error, file=sys.stderr)
    print('docs: %s (%s Markdown files, %s local links)' % ('failed' if errors else 'ok', len(paths), total))
    return bool(errors)


if __name__ == '__main__':
    sys.exit(main())
