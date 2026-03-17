#!/usr/bin/env python3
"""
Experiment: Replace identifiers in source code with short-token names.
Measures token savings using tiktoken (cl100k_base, used by GPT-4 / close to Claude's tokenizer).

Usage:
    python3 scripts/tokenizer_experiment.py ~/repos/qsearch
"""

import sys
import os
import re
import tiktoken
from collections import Counter
from pathlib import Path

# Use cl100k_base (GPT-4 / Claude-approximate)
enc = tiktoken.get_encoding("cl100k_base")


def token_count(text: str) -> int:
    return len(enc.encode(text))


def find_source_files(root: str) -> list[str]:
    """Find .ts/.tsx files, excluding node_modules/dist/etc."""
    skip = {"node_modules", "dist", "cdk.out", ".venv", ".git"}
    files = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in skip]
        for f in filenames:
            if f.endswith((".ts", ".tsx")) and not f.endswith(".d.ts"):
                files.append(os.path.join(dirpath, f))
    return sorted(files)


# Regex to extract identifiers (camelCase, PascalCase, snake_case, UPPER_CASE)
# Matches word-like tokens that look like variable/function/type names
IDENT_RE = re.compile(r'\b([a-zA-Z_$][a-zA-Z0-9_$]{2,})\b')

# TypeScript keywords to skip
TS_KEYWORDS = {
    "abstract", "any", "as", "async", "await", "bigint", "boolean", "break",
    "case", "catch", "class", "const", "constructor", "continue", "debugger",
    "declare", "default", "delete", "do", "else", "enum", "export", "extends",
    "false", "finally", "for", "from", "function", "get", "if", "implements",
    "import", "in", "infer", "instanceof", "interface", "is", "keyof", "let",
    "module", "namespace", "never", "new", "null", "number", "object", "of",
    "package", "private", "protected", "public", "readonly", "require",
    "return", "set", "static", "string", "super", "switch", "symbol", "this",
    "throw", "true", "try", "type", "typeof", "undefined", "unique", "unknown",
    "var", "void", "while", "with", "yield",
    # Common globals / built-ins we shouldn't rename
    "console", "document", "window", "process", "Promise", "Array", "Object",
    "String", "Number", "Boolean", "Error", "Map", "Set", "JSON", "Math",
    "Date", "RegExp", "Buffer", "setTimeout", "setInterval", "clearTimeout",
    "clearInterval", "fetch", "Response", "Request", "Headers", "URL",
    "URLSearchParams", "FormData", "Blob", "File", "FileReader",
    "AbortController", "AbortSignal", "Event", "EventTarget",
    "HTMLElement", "Element", "Node", "NodeList",
}


def generate_short_names():
    """Generate 1-token names: a-z, then aa, ab, ..., then aaa, etc."""
    import string
    # Single letters (all 1 token)
    for c in string.ascii_lowercase:
        yield c
    # Two-letter combos
    for c1 in string.ascii_lowercase:
        for c2 in string.ascii_lowercase:
            name = c1 + c2
            # Verify it's actually 1 token
            if token_count(name) == 1:
                yield name
    # Three-letter combos (many are 1 token)
    for c1 in string.ascii_lowercase:
        for c2 in string.ascii_lowercase:
            for c3 in string.ascii_lowercase:
                name = c1 + c2 + c3
                if token_count(name) == 1:
                    yield name
    # Four-letter combos (still many are 1 token)
    for c1 in string.ascii_lowercase:
        for c2 in string.ascii_lowercase:
            for c3 in string.ascii_lowercase:
                for c4 in string.ascii_lowercase:
                    name = c1 + c2 + c3 + c4
                    if token_count(name) == 1:
                        yield name
    # Fallback: numbered names
    i = 0
    while True:
        yield f"v{i}"
        i += 1


def extract_identifiers(source: str) -> Counter:
    """Extract and count identifiers from source code."""
    counts = Counter()
    for match in IDENT_RE.finditer(source):
        ident = match.group(1)
        if ident not in TS_KEYWORDS:
            counts[ident] += 1
    return counts


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/repos/qsearch")
    files = find_source_files(root)
    print(f"Found {len(files)} source files\n")

    # Read all source
    all_source = {}
    total_source = ""
    for f in files:
        with open(f) as fh:
            content = fh.read()
            all_source[f] = content
            total_source += content + "\n"

    # Count original tokens
    original_tokens = token_count(total_source)
    original_bytes = len(total_source.encode("utf-8"))
    print(f"Original: {original_tokens:,} tokens, {original_bytes:,} bytes")

    # Extract identifiers
    ident_counts = extract_identifiers(total_source)
    print(f"Unique identifiers found: {len(ident_counts):,}")

    # Analyze token cost of each identifier
    ident_token_info = []
    for ident, count in ident_counts.items():
        tokens = token_count(ident)
        ident_token_info.append((ident, count, tokens))

    # Sort by total token cost (tokens * occurrences), descending
    ident_token_info.sort(key=lambda x: x[1] * x[2], reverse=True)

    # Show top identifiers by total token cost
    print(f"\n{'Identifier':<40} {'Occur':>6} {'Tok':>4} {'TotalTok':>9}")
    print("-" * 65)
    for ident, count, tokens in ident_token_info[:30]:
        print(f"{ident:<40} {count:>6} {tokens:>4} {count * tokens:>9}")

    # Build replacement map: only replace idents where we save tokens
    short_name_gen = generate_short_names()
    replacement_map = {}
    # Skip single-letter names and very short common words that are already 1 token
    for ident, count, tokens in ident_token_info:
        if tokens <= 1:
            continue  # already optimal
        short = next(short_name_gen)
        short_tokens = token_count(short)
        if short_tokens < tokens:
            replacement_map[ident] = (short, tokens, short_tokens)

    # Apply replacements
    replaced_source = total_source
    tokens_saved_detail = []
    for ident, (short, old_tok, new_tok) in sorted(
        replacement_map.items(), key=lambda x: len(x[0]), reverse=True  # longest first
    ):
        count_before = replaced_source.count(ident)
        # Use word boundary replacement to avoid partial matches
        replaced_source = re.sub(r'\b' + re.escape(ident) + r'\b', short, replaced_source)
        saved = (old_tok - new_tok) * count_before
        if saved > 0:
            tokens_saved_detail.append((ident, short, old_tok, new_tok, count_before, saved))

    tokens_saved_detail.sort(key=lambda x: x[5], reverse=True)

    new_tokens = token_count(replaced_source)
    new_bytes = len(replaced_source.encode("utf-8"))

    print(f"\n{'='*65}")
    print(f"RESULTS")
    print(f"{'='*65}")
    print(f"Original:  {original_tokens:>8,} tokens  {original_bytes:>10,} bytes")
    print(f"Replaced:  {new_tokens:>8,} tokens  {new_bytes:>10,} bytes")
    print(f"Saved:     {original_tokens - new_tokens:>8,} tokens ({(original_tokens - new_tokens) / original_tokens * 100:.1f}%)")
    print(f"           {original_bytes - new_bytes:>8,} bytes  ({(original_bytes - new_bytes) / original_bytes * 100:.1f}%)")

    print(f"\nTop replacements by tokens saved:")
    print(f"{'Original':<35} {'New':<6} {'OldTok':>7} {'NewTok':>7} {'Count':>6} {'Saved':>6}")
    print("-" * 75)
    for ident, short, old_tok, new_tok, count, saved in tokens_saved_detail[:30]:
        print(f"{ident:<35} {short:<6} {old_tok:>7} {new_tok:>7} {count:>6} {saved:>6}")

    # Show a sample of replaced code
    print(f"\n{'='*65}")
    print("SAMPLE: First replaced file (first 30 lines)")
    print(f"{'='*65}")
    first_file = files[0]
    original_content = all_source[first_file]
    replaced_content = original_content
    for ident, (short, _, _) in sorted(
        replacement_map.items(), key=lambda x: len(x[0]), reverse=True
    ):
        replaced_content = re.sub(r'\b' + re.escape(ident) + r'\b', short, replaced_content)

    print(f"--- {first_file} ---")
    for i, line in enumerate(replaced_content.split("\n")[:30], 1):
        print(f"  {i:3}: {line}")


if __name__ == "__main__":
    main()
