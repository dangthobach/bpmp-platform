#!/usr/bin/env python3
"""Generate a lexical control/event inventory; no dependency or JSX execution."""
import argparse
import csv
import hashlib
import json
import re
from pathlib import Path


def balanced(source, start):
    depth, quote, escaped = 0, None, False
    for i in range(start, len(source)):
        c = source[i]
        if quote:
            if escaped:
                escaped = False
            elif c == '\\':
                escaped = True
            elif c == quote:
                quote = None
            continue
        if c in "\"'`":
            quote = c
        elif c == '{':
            depth += 1
        elif c == '}':
            depth -= 1
            if depth == 0:
                return source[start:i + 1]
    return source[start:start + 300]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, default=Path('../bpmp-prototype/src'))
    parser.add_argument('--output', type=Path, default=Path('docs/refactor-prototype'))
    args = parser.parse_args()
    if not args.source.is_dir():
        parser.error('source directory does not exist')
    args.output.mkdir(parents=True, exist_ok=True)
    rows, files = [], []
    for path in sorted(args.source.rglob('*.tsx')):
        source = path.read_text()
        name = path.relative_to(args.source).as_posix()
        files.append({'file': name, 'sha256': hashlib.sha256(source.encode()).hexdigest()})
        # Lexical evidence only: custom component callback props are also included.
        for match in re.finditer(r'\b(on[A-Z]\w*)\s*=\s*\{', source):
            start = source.index('{', match.start())
            rows.append({'file': name, 'line': source.count('\n', 0, match.start()) + 1,
                         'kind': 'event-prop', 'name': match[1], 'expression': balanced(source, start)})
        for match in re.finditer(r'<(button|input|select|textarea|form|a)\b', source):
            i = match.end()
            while i < len(source):
                if source[i] == '{':
                    i += len(balanced(source, i))
                elif source[i] in "\"'":
                    quote = source[i]
                    i += 1
                    while i < len(source) and source[i] != quote:
                        i += 2 if source[i] == '\\' else 1
                    i += 1
                elif source[i] == '>':
                    i += 1
                    break
                else:
                    i += 1
            opening = source[match.start():i]
            # Keep the opening tag and following label context for manual review.
            context = source[i:i + 220].split('</' + match[1] + '>')[0]
            rows.append({'file': name, 'line': source.count('\n', 0, match.start()) + 1,
                         'kind': 'control', 'name': match[1], 'expression': opening + context})
    rows.sort(key=lambda r: (r['file'], r['line'], r['kind']))
    for number, row in enumerate(rows, 1):
        row['id'] = f'UI-{number:04d}'
    (args.output / 'interaction-inventory.json').write_text(json.dumps({'method': 'lexical-v1', 'files': files, 'entries': rows}, ensure_ascii=False, indent=2) + '\n')
    with (args.output / 'interaction-inventory.csv').open('w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=['id', 'file', 'line', 'kind', 'name', 'expression'])
        writer.writeheader()
        writer.writerows(rows)
    lines = ['# Prototype control/event inventory', '',
             'Generated lexical evidence. Includes native controls, non-button events, and custom callback props.',
             'IDs are snapshot-local; regeneration can renumber them. File hashes are in JSON.',
             'A control and its event are separate rows, not separate user journeys.',
             'Expressions are evidence, not proof that an action persists or succeeds.',
             'Conditional/dynamic labels, forwarded props, keyboard behavior and handler semantics require manual/browser review.',
             'No browser walkthrough was performed. This is not a certified exhaustive runtime interaction inventory.', '',
             '| File | Native controls | Event props |', '|---|---:|---:|']
    for file in files:
        entries = [r for r in rows if r['file'] == file['file']]
        lines.append(f"| {file['file']} | {sum(r['kind'] == 'control' for r in entries)} | {sum(r['kind'] == 'event-prop' for r in entries)} |")
    for file in files:
        lines += ['', '## ' + file['file'], '', '| ID | Line | Kind | Name | Source excerpt |', '|---|---:|---|---|---|']
        for row in rows:
            if row['file'] == file['file']:
                excerpt = re.sub(r'\s+', ' ', row['expression']).replace('|', '&#124;').replace('`', '&#96;')
                lines.append(f"| {row['id']} | {row['line']} | {row['kind']} | {row['name']} | {excerpt[:240]} |")
    (args.output / 'interaction-inventory.md').write_text('\n'.join(lines) + '\n')
    print(f"{len(files)} TSX files; {sum(r['kind'] == 'control' for r in rows)} controls; {sum(r['kind'] == 'event-prop' for r in rows)} event props")


if __name__ == '__main__':
    main()
