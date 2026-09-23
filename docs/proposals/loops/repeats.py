"""For one session: every stretch of identical tool calls with identical
results (no new information), with where it started and what preceded it."""
import json, os, sys
sid = sys.argv[1]
evs = [json.loads(l) for l in open(os.path.expanduser(f'~/.nabu/sessions/{sid}.jsonl'), encoding='utf-8') if l.strip()]
calls = {}  # call_id -> (index, key)
seq = []    # (index, key, result)
for i, e in enumerate(evs):
    if e['type'] == 'tool_call':
        d = e['data']
        calls[d['call_id']] = (i, d['tool'] + ' ' + json.dumps(d.get('arguments'), sort_keys=True))
    elif e['type'] == 'tool_result':
        d = e['data']
        if d['call_id'] in calls:
            i0, key = calls[d['call_id']]
            seq.append((i0, key, d.get('status', '') + ':' + d.get('content', '')[:2000]))
    elif e['type'] == 'message' and e['data'].get('role') == 'user':
        seq.append((i, 'USER', e['data']['content'][:200]))

# stretches: the same (key,result) seen again within the last 6 calls, counted per stretch
from collections import defaultdict
stretches = []
cur = None
recent = []
for i, key, res in seq:
    if key == 'USER':
        print(f"  [{i}] USER: {res!r}")
        recent = []
        continue
    pair = (key, res)
    if pair in recent:
        if cur and cur['pair'] == pair:
            cur['n'] += 1; cur['end'] = i
        else:
            cur = {'pair': pair, 'n': 2, 'start': i, 'end': i}
            stretches.append(cur)
    recent = (recent + [pair])[-6:]
for s in stretches:
    if s['n'] >= 3:
        print(f"stretch x{s['n']} events {s['start']}..{s['end']}: {s['pair'][0][:120]}  -> {s['pair'][1][:80]!r}")
