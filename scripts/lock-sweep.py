#!/usr/bin/env python3
"""Report engine calls that reach the HOST while a lock is held.

The rule this enforces is FIX-038 §1.1 / FIX-041 §1.1: what may run inside one
of the engine's locks is decided by whether the call can reach the host — a
broker, a processor, a reporter the embedding application supplied — and not by
whether it can re-enter the lock. A host call under a lock stalls everything
else behind a latency the engine does not control, and "usually fast" is a
property of the host's network rather than of this engine.

The shape has now appeared four times (FIX-036 §1.5, FIX-038 §1.1 twice,
FIX-041 §1.1). FIX-038 ran a sweep like this one and did not keep it; three
days later the same shape came back. So it lives here.

WHAT IT CANNOT DO, stated first because a scanner that reports clean is the
dangerous failure mode:

  * It is SYNTACTIC. It matches method names, so it sees a host call written
    directly inside a critical section and misses one reached through a helper.
    #320's own defect — AddEventProcessor learning to call the broker, two
    frames down from the lock — is exactly the case it would NOT have caught.
  * The pattern list below is the whole of its knowledge. A host-facing method
    missing from it is invisible; FIX-038 lost `Report` from its list and the
    sweep reported clean on a live defect.
  * It cannot tell a method that is host-facing today from the same name on an
    internal type.

So: a clean run is evidence, not proof, and every finding is read before it is
believed. Run it when touching locking code, and after adding a host-facing
method add its name to PATTERNS.

Usage: scripts/lock-sweep.py [path ...]      (default: cmd internal pkg)
Exit 0 when nothing is reported, 1 otherwise.
"""

import os
import re
import sys

# Methods a host can implement, or that reach one that can. Grouped by the port
# they belong to so an addition has an obvious home.
PATTERNS = {
    # messaging.MessageBroker / messaging.Subscription
    "Subscribe", "Publish", "Unsubscribe", "AddKey",
    # eventproc.EventProcessor and the optional key capability
    "ProcessEvent", "CorrelationKeys", "ApplyProcessorKeys",
    # waiter lifecycle — a message waiter's Service subscribes and its Stop
    # unsubscribes, both against the host's broker
    "Service",
    # observability: the reporter is host-supplied (ADR-022)
    "Report", "Reporter",
    # renv: reaching the runtime's variables runs host-registered accessors
    "RuntimeVar",
    # exec.CallableResolver: the host maps a callable reference onto a
    # registry key, and may call back into the engine to do it (ADR-023 v.5
    # §2.7 puts the call outside every lock for exactly that reason)
    "ResolveCallable",
}

LOCK_RE = re.compile(r"\b(?:(\w+(?:\.\w+)*))\.(?:R)?Lock\(\)")
UNLOCK_RE = re.compile(r"\b(?:(\w+(?:\.\w+)*))\.(?:R)?Unlock\(\)")
CALL_RE = re.compile(r"\.(\w+)\(")
FUNC_RE = re.compile(r"^func\b")
ALLOW = "locksweep:allow"


def strip_code(line):
    """Drop the trailing line comment and string literals, so a method name
    mentioned in prose or in a message never counts as a call."""
    out, in_str, quote, i = [], False, "", 0

    while i < len(line):
        c = line[i]

        if in_str:
            if c == "\\":
                i += 2
                continue
            if c == quote:
                in_str = False
            i += 1
            continue

        if c in "\"'`":
            in_str, quote = True, c
            i += 1
            continue

        if c == "/" and i + 1 < len(line) and line[i + 1] == "/":
            break

        out.append(c)
        i += 1

    return "".join(out)


def scan_file(path):
    """Yield (line_no, held_lock, call) for every host-facing call made while a
    lock is held."""
    findings = []

    with open(path, encoding="utf-8") as fh:
        lines = fh.readlines()

    depth = 0          # brace depth
    held = []          # [(name, depth_acquired)] — explicitly unlocked
    deferred = []      # [name] — released when the function body closes
    fn_depth = None    # brace depth of the enclosing function body

    for no, raw in enumerate(lines, start=1):
        code = strip_code(raw)

        if fn_depth is None and FUNC_RE.match(raw):
            fn_depth = depth

        opens = code.count("{")
        closes = code.count("}")

        is_defer = code.lstrip().startswith("defer ")

        # A lock taken on this line is not yet protecting this line's own call,
        # and a lock released on this line still was — so calls are judged
        # against the state BEFORE the line's lock/unlock is applied, except
        # that an unlock earlier in the same line does release it. Both are
        # rare enough that line granularity is honest here.
        active = [n for n, _ in held] + deferred

        if active:
            for call in CALL_RE.findall(code):
                if call in PATTERNS and ALLOW not in raw:
                    # a lock/unlock pair is not itself a host call
                    findings.append((no, active[-1], call, raw.strip()))

        for m in LOCK_RE.finditer(code):
            name = m.group(1)
            if is_defer:
                continue
            held.append((name, depth))

        for m in UNLOCK_RE.finditer(code):
            name = m.group(1)
            if is_defer:
                deferred.append(name)
                continue
            for i in range(len(held) - 1, -1, -1):
                if held[i][0] == name:
                    held.pop(i)
                    break

        depth += opens - closes

        # Leaving a block releases anything taken inside it, and leaving the
        # function body releases the deferred unlocks. Without this a single
        # unbalanced branch would report every later line in the file.
        held = [(n, d) for n, d in held if d < depth or d == depth]
        held = [(n, d) for n, d in held if d <= depth]

        if fn_depth is not None and depth <= fn_depth:
            fn_depth = None
            deferred = []
            held = []

    return findings


def go_files(roots):
    for root in roots:
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = [
                d for d in dirnames
                if d not in (".git", "generated", "testdata")
            ]
            for name in sorted(filenames):
                if name.endswith(".go") and not name.endswith("_test.go"):
                    yield os.path.join(dirpath, name)


def main():
    roots = sys.argv[1:] or ["cmd", "internal", "pkg"]
    roots = [r for r in roots if os.path.isdir(r)]

    total = 0

    for path in go_files(roots):
        for no, lock, call, text in scan_file(path):
            total += 1
            print(f"{path}:{no}: {call} reached while {lock} is held")
            print(f"    {text}")

    if total:
        print()
        print(f"{total} host call(s) under a lock. Each is either a defect "
              f"(move it out of the critical section) or a reviewed exception "
              f"(mark the line // {ALLOW}: <reason>).")

        return 1

    print("lock-sweep: no host call found inside a critical section.")
    print("This is evidence, not proof — the sweep is syntactic and knows only "
          "the names in PATTERNS. See the module docstring.")

    return 0


if __name__ == "__main__":
    sys.exit(main())
