"""Context fill when repetition loops start, against the fill of all turns."""
import glob, json, os, collections

def band(p):
    return f"{int(p // 10) * 10}-{int(p // 10) * 10 + 9}%"

all_turns = collections.Counter()
loop_turns = collections.Counter()
loop_events = 0
for f in glob.glob(os.path.expanduser('~/.nabu/sessions/*.jsonl')):
    evs = [json.loads(l) for l in open(f, encoding='utf-8') if l.strip()]
    window = next((e['data'].get('context_window') for e in evs if e['type'] == 'session'), 0) or 0
    if not window:
        continue
    fill = 0.0
    calls = {}
    recent = []
    for e in evs:
        d = e['data']
        if e['type'] == 'message' and d.get('role') == 'assistant' and d.get('usage'):
            fill = 100.0 * d['usage']['input_tokens'] / window
            all_turns[band(fill)] += 1
        elif e['type'] == 'message' and d.get('role') == 'user':
            recent = []
        elif e['type'] == 'tool_call':
            calls[d['call_id']] = d['tool'] + json.dumps(d.get('arguments'), sort_keys=True)
        elif e['type'] == 'tool_result' and d['call_id'] in calls:
            pair = (calls[d['call_id']], d.get('status'), d.get('content', '')[:2000])
            if pair in recent:
                # a call repeated with the same result: no new information
                loop_turns[band(fill)] += 1
                loop_events += 1
            recent = (recent + [pair])[-6:]

print(f"repeated call+result events: {loop_events}")
print(f"{'fill':>8} {'turns':>7} {'repeats':>8} {'repeats per 100 turns':>22}")
for b in sorted(all_turns, key=lambda s: int(s.split('-')[0])):
    n, r = all_turns[b], loop_turns[b]
    print(f"{b:>8} {n:>7} {r:>8} {100 * r / n:>22.1f}")
