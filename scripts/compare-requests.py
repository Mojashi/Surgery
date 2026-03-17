#!/usr/bin/env python3
"""
Compare two intercepted API requests.

Usage:
  python3 scripts/compare-requests.py /tmp/intercept-18888-2.json /tmp/intercept-18889-2.json
  python3 scripts/compare-requests.py <label_a>:<file_a> <label_b>:<file_b>
"""
import json, sys
from collections import Counter

def analyze(msgs):
    blocks = Counter()
    sizes = Counter()
    for m in msgs:
        content = m.get("content", [])
        if isinstance(content, list):
            for b in content:
                if isinstance(b, dict):
                    t = b.get("type", "")
                    blocks[t] += 1
                    if t == "text":
                        sizes[t] += len(b.get("text", ""))
                    elif t == "tool_result":
                        c = b.get("content", "")
                        sizes[t] += len(c) if isinstance(c, str) else len(json.dumps(c))
                    elif t == "tool_use":
                        sizes[t] += len(json.dumps(b.get("input", {})))
                    elif t == "thinking":
                        sizes[t] += len(b.get("thinking", ""))
        elif isinstance(content, str):
            blocks["string"] += 1
            sizes["string"] += len(content)
    return blocks, sizes

def load(arg):
    if ":" in arg and not arg.startswith("/"):
        label, path = arg.split(":", 1)
    else:
        label = arg.split("/")[-1]
        path = arg
    data = json.load(open(path))
    msgs = data.get("messages", data) if isinstance(data, dict) else data
    return label, msgs

if len(sys.argv) < 3:
    print(__doc__)
    sys.exit(1)

label_a, msgs_a = load(sys.argv[1])
label_b, msgs_b = load(sys.argv[2])

size_a = len(json.dumps(msgs_a))
size_b = len(json.dumps(msgs_b))

print(f"{label_a}: {len(msgs_a)} messages, {size_a:,} bytes")
print(f"{label_b}: {len(msgs_b)} messages, {size_b:,} bytes")
diff = size_a - size_b
if size_a > 0:
    print(f"Difference: {diff:+,} bytes ({diff*100/size_a:+.1f}%)")
print()

ba, sa = analyze(msgs_a)
bb, sb = analyze(msgs_b)

all_types = sorted(set(list(ba.keys()) + list(bb.keys())))
print(f"{'Block type':20s}  {'Count':>12s}  {'Size':>16s}")
print("-" * 55)
for t in all_types:
    ca, cb = ba.get(t, 0), bb.get(t, 0)
    za, zb = sa.get(t, 0), sb.get(t, 0)
    cd = ca - cb
    zd = za - zb
    print(f"{t:20s}  {ca:4d} → {cb:4d} ({cd:+d})  {za:>7,} → {zb:>7,} ({zd:+,})")

print()
total_a = sum(sa.values())
total_b = sum(sb.values())
print(f"Total content: {total_a:,} → {total_b:,} ({total_a-total_b:+,})")

# Show first difference
if len(msgs_a) == len(msgs_b):
    for i in range(len(msgs_a)):
        if json.dumps(msgs_a[i], sort_keys=True) != json.dumps(msgs_b[i], sort_keys=True):
            print(f"\nFirst diff at message [{i}] ({msgs_a[i].get('role', '?')})")
            break
    else:
        print("\nAll messages identical.")
else:
    print(f"\nMessage count differs: {len(msgs_a)} vs {len(msgs_b)}")
