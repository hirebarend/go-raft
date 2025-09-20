# Prompt to Refine a Single Raft Method (Go, Full Code Pasted)

You are given my full Raft implementation in Go. I will paste the entire code.

Your task is to **produce the best possible implementation of exactly one target method** while keeping changes **as small as possible** and **fully aligned with the Raft paper (Ongaro & Ousterhout, 2014)**.

## Scope
- **Target method:** `<METHOD_NAME>`.
- Only modify this method. If an absolutely necessary, tightly-scoped helper change is required for correctness, you may do it, but prefer not to. Do not touch unrelated code.

## Hard Requirements
1. **Accuracy vs. Raft Paper**
   - Conform precisely to the Raft paper: §5 (basics), §5.2 (Leader election), §5.3 (Log replication), §5.4 (Safety), §7 (Snapshots/compaction) if relevant.
   - If my current behavior conflicts with the paper, fix the method to match the paper.
2. **Minimal, Local Changes**
   - Make **surgical edits** only. Keep the existing control flow and structures when possible.
   - Do **not** rename identifiers/types unless required for correctness.
3. **Style Preservation**
   - Keep my coding style and naming conventions. Preserve the existing method **signature and receiver**. Code must pass `gofmt`.
4. **No Comments in Code**
   - Do **not** add comments or docstrings anywhere in the code you output.
5. **Deterministic, Safe, Idempotent**
   - Correct under retries, stale/duplicate RPCs, concurrent goroutine scheduling, and partial failures consistent with my design.
6. **No New Dependencies**
   - Use only the standard library already in use in my codebase.

## Considerations (apply as relevant to the target method)
- Term handling: monotonic `currentTerm`, step-down on higher term, persisted updates if my code persists state.
- Indices: `commitIndex`, `lastApplied`, `lastIncludedIndex/Term` boundaries with log compaction.
- Log matching: `prevLogIndex/Term` checks, conflict handling; fast backtracking only if already present.
- Safety invariants: election safety, leader completeness, log matching.
- Timing: maintain my timeouts/backoff behavior.
- Concurrency: keep my existing locking; avoid widening critical sections unless fixing a race.
- Idempotence: handle duplicate `AppendEntries` / `InstallSnapshot` / `RequestVote` appropriately.

## Output Format (strict)
- Output **only the improved method’s complete code**, including its unchanged signature and receiver.
- **No extra text** before or after. **No diffs, no rationale, no tests, no explanations.**
- The code must contain **no comments**.

Begin after I paste the full code and specify `<METHOD_NAME>`.
