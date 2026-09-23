import json, os, sys
sid, lo, hi = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
width = int(sys.argv[4]) if len(sys.argv) > 4 else 260
evs = [json.loads(l) for l in open(os.path.expanduser(f'~/.nabu/sessions/{sid}.jsonl'), encoding='utf-8') if l.strip()]
for i in range(lo, min(hi, len(evs))):
    e = evs[i]; d = e['data']; t = e['type']
    ts = e['timestamp'][11:19]
    if t == 'thinking':
        print(f"{i} {ts} THINK  {d.get('content','')[:width]!r}")
    elif t == 'message':
        u = d.get('usage') or {}
        print(f"{i} {ts} {d['role'].upper():9} in={u.get('input_tokens','')} {d.get('content','')[:width]!r}")
    elif t == 'tool_call':
        print(f"{i} {ts} CALL   {d['tool']} {json.dumps(d.get('arguments'))[:width]}")
    elif t == 'tool_result':
        print(f"{i} {ts} RESULT {d.get('status')} {d.get('content','')[:width]!r}")
    elif t in ('stop_veto', 'notice', 'compaction'):
        print(f"{i} {ts} {t.upper()} {str(d)[:width]}")
    elif t == 'context':
        print(f"{i} {ts} CTX {d.get('source')} {d.get('slot')} {d.get('content','')[:120]!r}")
    else:
        print(f"{i} {ts} {t} {str(d)[:120]}")
