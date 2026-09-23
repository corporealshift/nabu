"""Thoughts that say "the user is right", tallied by what came just before them.

Usage: python answering.py   (reads ~/.nabu/sessions)
"""
import collections
import glob
import json
import os
import re

pat = re.compile(r"(you're right|user is right|kyle is right|the user is pointing|user called me)", re.I)
c = collections.Counter()
for f in glob.glob(os.path.expanduser('~/.nabu/sessions/*.jsonl')):
    evs = [json.loads(l) for l in open(f, encoding='utf-8') if l.strip()]
    last = None
    for e in evs:
        t, d = e['type'], e['data']
        if t == 'message' and d.get('role') == 'user':
            last = 'user message'
        elif t == 'stop_veto':
            last = 'stop veto'
        elif t == 'context' and d.get('slot') == 'suffix':
            last = 'suffix context (' + d.get('source', '') + ')'
        elif t == 'tool_result':
            last = 'tool result'
        elif t == 'thinking' and pat.search(d.get('content', '')):
            c[last] += 1
print(sum(c.values()), 'thoughts saying the user is right, by what came just before:')
for k, v in c.most_common():
    print(f'  {v:4}  after {k}')
