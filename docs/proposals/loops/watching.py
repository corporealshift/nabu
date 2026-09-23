"""Repeated identical calls: which were watching something that then changed,
and which never saw anything new.

A "run" is one call (tool + arguments) made 3+ times with no more than 6 other
calls between repeats and no user message inside. It is "watching" if its
result changed at some point during the run, "stuck" if every result was the
same.

Usage: python watching.py   (reads ~/.nabu/sessions)
"""
import collections
import glob
import json
import os

MUTATING = {"write", "edit"}


def runs_in(evs):
    calls = {}
    seq = []  # (event index, key, tool, result, timestamp)
    for i, e in enumerate(evs):
        d = e['data']
        if e['type'] == 'tool_call':
            calls[d['call_id']] = (i, d['tool'], d['tool'] + ' ' + json.dumps(d.get('arguments'), sort_keys=True), e['timestamp'])
        elif e['type'] == 'tool_result' and d['call_id'] in calls:
            i0, tool, key, ts = calls[d['call_id']]
            seq.append((i0, key, tool, d.get('status', '') + ':' + d.get('content', '')[:4000], ts))
        elif e['type'] == 'message' and d.get('role') == 'user':
            seq.append((i, None, None, None, e['timestamp']))
    open_runs = {}
    done = []
    for pos, (i, key, tool, res, ts) in enumerate(seq):
        if key is None:
            done.extend(open_runs.values())
            open_runs = {}
            continue
        for k in list(open_runs):
            if pos - open_runs[k]['last_pos'] > 7:
                done.append(open_runs.pop(k))
        r = open_runs.get(key)
        if r is None:
            open_runs[key] = {'key': key, 'tool': tool, 'results': [res], 'start': i, 'last_pos': pos, 't0': ts, 't1': ts}
        else:
            r['results'].append(res)
            r['last_pos'] = pos
            r['t1'] = ts
    done.extend(open_runs.values())
    return [r for r in done if len(r['results']) >= 3]


tally = collections.Counter()
examples = collections.defaultdict(list)
for f in glob.glob(os.path.expanduser('~/.nabu/sessions/*.jsonl')):
    evs = [json.loads(l) for l in open(f, encoding='utf-8') if l.strip()]
    sid = os.path.basename(f)[:10]
    for r in runs_in(evs):
        changed = len(set(r['results'])) > 1
        kind = ('mutating' if r['tool'] in MUTATING else 'observing') + (' / watching' if changed else ' / stuck')
        tally[kind] += 1
        examples[kind].append(f"{sid} ev{r['start']} x{len(r['results'])} {r['t0'][11:19]}-{r['t1'][11:19]} {r['key'][:110]}")

for k in sorted(tally):
    print(f"{tally[k]:4}  {k}")
for k in sorted(examples):
    print(f"\n== {k}")
    for x in examples[k][:12]:
        print("  ", x)
