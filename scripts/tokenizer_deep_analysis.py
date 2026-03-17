#!/usr/bin/env python3
"""
Deep analysis of token savings opportunities beyond identifier renaming.
"""

import sys
import os
import re
import tiktoken
from collections import Counter, defaultdict

enc = tiktoken.get_encoding("cl100k_base")

def token_count(text: str) -> int:
    return len(enc.encode(text))

def find_source_files(root: str) -> list[str]:
    skip = {"node_modules", "dist", "cdk.out", ".venv", ".git"}
    files = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in skip]
        for f in filenames:
            if f.endswith((".ts", ".tsx")) and not f.endswith(".d.ts"):
                files.append(os.path.join(dirpath, f))
    return sorted(files)

def main():
    root = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/repos/qsearch")
    files = find_source_files(root)

    all_source = ""
    for f in files:
        with open(f) as fh:
            all_source += fh.read() + "\n"

    original_tokens = token_count(all_source)

    # === 1. String literals (especially repeated ones) ===
    print("=" * 70)
    print("1. REPEATED STRING LITERALS")
    print("=" * 70)
    string_re = re.compile(r"""(?:"([^"\\]*(?:\\.[^"\\]*)*)"|'([^'\\]*(?:\\.[^'\\]*)*)')""")
    string_counts = Counter()
    for m in string_re.finditer(all_source):
        s = m.group(1) if m.group(1) is not None else m.group(2)
        if s is None or len(s) < 3:
            continue
        string_counts[s] += 1

    print(f"{'String':<50} {'Count':>6} {'Tok':>4} {'TotalTok':>9} {'Saveable':>9}")
    print("-" * 85)
    string_savings = []
    for s, count in string_counts.most_common():
        if count < 2:
            break
        tok = token_count(f'"{s}"')
        total = tok * count
        # If extracted to a 1-token var, cost = 1*count + tok (definition)
        saveable = total - (count + tok) if total > count + tok else 0
        if saveable > 0:
            string_savings.append((s, count, tok, total, saveable))

    string_savings.sort(key=lambda x: x[4], reverse=True)
    total_string_saveable = sum(x[4] for x in string_savings)
    for s, count, tok, total, saveable in string_savings[:20]:
        display = s[:47] + "..." if len(s) > 50 else s
        print(f"{display:<50} {count:>6} {tok:>4} {total:>9} {saveable:>9}")
    print(f"\nTotal saveable from string dedup: {total_string_saveable:,} tokens")

    # === 2. Import statements ===
    print(f"\n{'=' * 70}")
    print("2. IMPORT STATEMENTS")
    print("=" * 70)
    import_lines = [line for line in all_source.split("\n") if line.strip().startswith("import ")]
    import_tokens = sum(token_count(line) for line in import_lines)
    print(f"Import lines: {len(import_lines)}")
    print(f"Import tokens: {import_tokens:,} ({import_tokens/original_tokens*100:.1f}% of total)")

    # === 3. Type annotations ===
    print(f"\n{'=' * 70}")
    print("3. TYPE ANNOTATIONS")
    print("=" * 70)
    # Rough heuristic: lines with type-heavy patterns
    type_patterns = [
        (r':\s*[A-Z]\w+(?:<[^>]+>)?', "type annotations (: Type)"),
        (r'<[A-Z]\w+(?:,\s*[A-Z]\w+)*>', "generic params (<Type>)"),
        (r'\bas\s+\w+', "type assertions (as Type)"),
        (r'interface\s+\w+', "interface declarations"),
        (r'type\s+\w+\s*=', "type aliases"),
    ]
    for pattern, desc in type_patterns:
        matches = re.findall(pattern, all_source)
        tok_sum = sum(token_count(m) for m in matches)
        print(f"  {desc:<40} {len(matches):>5} occurrences, ~{tok_sum:>6,} tokens")

    # === 4. Repeated patterns / boilerplate ===
    print(f"\n{'=' * 70}")
    print("4. REPEATED MULTI-TOKEN PATTERNS (non-identifier)")
    print("=" * 70)
    # Find repeated 3-5 word sequences
    words = all_source.split()
    ngram_counts = Counter()
    for n in [3, 4, 5]:
        for i in range(len(words) - n):
            gram = " ".join(words[i:i+n])
            if len(gram) > 10:
                ngram_counts[gram] += 1

    pattern_savings = []
    for gram, count in ngram_counts.most_common():
        if count < 5:
            break
        tok = token_count(gram)
        saveable = (tok - 1) * count  # replace with 1-token alias
        if saveable > 10:
            pattern_savings.append((gram, count, tok, saveable))

    pattern_savings.sort(key=lambda x: x[3], reverse=True)
    print(f"{'Pattern':<55} {'Count':>6} {'Tok':>4} {'Saveable':>8}")
    print("-" * 78)
    seen_substrs = set()
    shown = 0
    for gram, count, tok, saveable in pattern_savings:
        # Skip if substring of already-shown pattern
        if any(gram in s for s in seen_substrs):
            continue
        display = gram[:52] + "..." if len(gram) > 55 else gram
        print(f"{display:<55} {count:>6} {tok:>4} {saveable:>8}")
        seen_substrs.add(gram)
        shown += 1
        if shown >= 25:
            break

    # === 5. Whitespace / indentation tokens ===
    print(f"\n{'=' * 70}")
    print("5. WHITESPACE & INDENTATION")
    print("=" * 70)
    indent_tokens = 0
    for line in all_source.split("\n"):
        leading = len(line) - len(line.lstrip())
        if leading > 0:
            indent_tokens += token_count(line[:leading])
    print(f"Indentation tokens: {indent_tokens:,} ({indent_tokens/original_tokens*100:.1f}% of total)")

    # What if we reduced indent from 2-space to 1-space?
    reduced_indent = re.sub(r'^( {2})+', lambda m: ' ' * (len(m.group()) // 2), all_source, flags=re.MULTILINE)
    reduced_tokens = token_count(reduced_indent)
    print(f"If indent halved (2sp→1sp): saves {original_tokens - reduced_tokens:,} tokens ({(original_tokens - reduced_tokens)/original_tokens*100:.1f}%)")

    # What if we removed all indentation?
    no_indent = re.sub(r'^[ \t]+', '', all_source, flags=re.MULTILINE)
    no_indent_tokens = token_count(no_indent)
    print(f"If all indent removed:       saves {original_tokens - no_indent_tokens:,} tokens ({(original_tokens - no_indent_tokens)/original_tokens*100:.1f}%)")

    # === 6. Comments ===
    print(f"\n{'=' * 70}")
    print("6. COMMENTS")
    print("=" * 70)
    comment_lines = []
    for line in all_source.split("\n"):
        stripped = line.strip()
        if stripped.startswith("//") or stripped.startswith("/*") or stripped.startswith("*"):
            comment_lines.append(line)
    comment_text = "\n".join(comment_lines)
    comment_tokens = token_count(comment_text) if comment_lines else 0
    print(f"Comment lines: {len(comment_lines)}")
    print(f"Comment tokens: {comment_tokens:,} ({comment_tokens/original_tokens*100:.1f}% of total)")

    # === 7. Property access chains ===
    print(f"\n{'=' * 70}")
    print("7. REPEATED PROPERTY ACCESS CHAINS")
    print("=" * 70)
    chain_re = re.compile(r'\b\w+(?:\.\w+){2,}')
    chain_counts = Counter()
    for m in chain_re.finditer(all_source):
        chain_counts[m.group()] += 1

    print(f"{'Chain':<55} {'Count':>6} {'Tok':>4} {'Saveable':>8}")
    print("-" * 78)
    chain_savings = []
    for chain, count in chain_counts.most_common():
        if count < 3:
            break
        tok = token_count(chain)
        saveable = (tok - 1) * count
        if saveable > 5:
            chain_savings.append((chain, count, tok, saveable))
    chain_savings.sort(key=lambda x: x[3], reverse=True)
    total_chain = sum(x[3] for x in chain_savings)
    for chain, count, tok, saveable in chain_savings[:15]:
        print(f"{chain:<55} {count:>6} {tok:>4} {saveable:>8}")
    print(f"\nTotal saveable from chain aliasing: {total_chain:,} tokens")

    # === SUMMARY ===
    print(f"\n{'=' * 70}")
    print("SUMMARY OF TOKEN REDUCTION OPPORTUNITIES")
    print(f"{'=' * 70}")
    print(f"Original total: {original_tokens:,} tokens\n")
    print(f"{'Strategy':<45} {'Tokens':>8} {'%':>6}")
    print("-" * 62)
    print(f"{'Identifier renaming (prev experiment)':<45} {'22,196':>8} {'12.1%':>6}")
    print(f"{'String literal dedup':<45} {total_string_saveable:>8,} {total_string_saveable/original_tokens*100:>5.1f}%")
    print(f"{'Remove all indentation':<45} {original_tokens - no_indent_tokens:>8,} {(original_tokens-no_indent_tokens)/original_tokens*100:>5.1f}%")
    print(f"{'Remove comments':<45} {comment_tokens:>8,} {comment_tokens/original_tokens*100:>5.1f}%")
    print(f"{'Import statement removal':<45} {import_tokens:>8,} {import_tokens/original_tokens*100:>5.1f}%")
    print(f"{'Property chain aliasing':<45} {total_chain:>8,} {total_chain/original_tokens*100:>5.1f}%")
    combined = 22196 + total_string_saveable + (original_tokens - no_indent_tokens) + comment_tokens + import_tokens + total_chain
    print(f"{'─'*45} {'─'*8} {'─'*6}")
    print(f"{'Theoretical max (with overlap)':<45} {combined:>8,} {combined/original_tokens*100:>5.1f}%")


if __name__ == "__main__":
    main()
