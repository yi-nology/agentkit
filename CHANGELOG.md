# Changelog

## v0.7.0 (2026-09-06)

Full-library audit fixes: 3 Critical + ~20 Important across 16 packages, each with regression tests.

### Security

- **mcp**: stdio subprocess env is now a real whitelist (PATH/HOME/TMPDIR base set + `Env` entries) instead of inheriting the full parent environment; `Env` entries are variable names looked up from the current process (or literal `KEY=VALUE`). Previously all parent secrets leaked to external MCP servers, contradicting the documented security model
- **acpx**: prompt is guarded before being passed as positional/flag argument — prompts starting with `-` get a newline prefix so CLI flag parsers cannot consume them as flags (prompt injection could bypass sandbox flags when the LLM fills the prompt)
- **workcopy**: `prepare` error path scrubbed the wrong argument — git failure details were dropped and token redaction never applied; error now keeps git output with token redacted

### Reliability

- **acpx**: single-line stdout flood no longer bypasses the 8MB cap (line buffer capped at 1MB, oversized lines dropped and resynced at next newline); timeout/cancel now uses `cmd.Cancel` (TERM group) + `WaitDelay` (SIGKILL) so the documented grace period actually works, and caller cancellation is no longer misreported as timeout (`ErrTimeout`/`ErrCanceled` sentinels, errors wrapped with `%w`); partial trailing line is flushed on failure paths too
- **llm**: `finish_reason=length` no longer panics when `Usage` is missing; deterministic 4xx (400/402/404/405/413/422 + auth/invalid-model markers) classified non-retryable via structured status code first, then digit-boundary text matching ("429" no longer matches "1429ms"); `AttemptTimeout` now bounds each attempt instead of the whole retry cycle
- **llm/breaker**: half-open probe can no longer wedge a model out of the fallback chain — breaker adds a probe deadline (`DefaultProbeTimeout`) and ignores failures recorded for rejected requests during open (cooldown no longer extended indefinitely); resilient pairs `Failure` on ctx-cancel and nil-model paths after `Allow`
- **breaker**: probe-stale recovery + no cooldown extension, regression-tested
- **worker**: `Stop` before `Start` no longer burns the shutdown path (mutex-based lifecycle, re-`Start` guarded); task panic only loses that task (worker survives, stale-reset requeues it); heartbeat joins the stop WaitGroup with per-call timeouts
- **progress**: manual `cancel()` now wakes the ctx listener goroutine (Background-ctx subscribers no longer leak)
- **workcopy**: `Sweep` no longer deletes in-use worktrees (rc>0 kept regardless of TTL — configure TTL above the longest task; crash leaks still handled by orphan sweep); singleflight race between shared result and `Release` fixed (registration moved inside the flight, bounded retry)
- **knowledge/rag**: `MilvusStore.Index` upserts by deterministic primary key — repeated restarts no longer accumulate duplicate rows; existing collections are validated (dim/metric) and loaded
- **skill**: progressive disclosure closes the frontmatter-name gap — `Meta.Name` is the canonical ref name (use_skill/allowed basis), frontmatter name becomes display alias `Meta.Title`; `AsSkillTool` normalizes alias inputs via the new `AliasResolver` capability

### API

- **toolprior**: `WithCallLimit` over-limit now returns a model-visible `LIMIT_REACHED: ...` text (nil error) instead of a Go error — eino ToolsNode propagates tool errors as whole-run aborts, discarding all partial progress; `Ordered` sorts Info-failed entries last as documented; call counter widened to int64; `Table.Add` panics on nil Tool (fail fast at registration)
- **agentrun**: `Config.ToolsFactory` builds a fresh tool table per attempt — use with `RunWithRetry` so `WithCallLimit` budgets reset instead of carrying over; new `RunWithEventsAndRetry` makes retries observable; `tool_result` events added; empty final reply is distinguished from "no final reply"
- **acpx/mcp/skill/rag**: eino tool wrappers pass through the framework context instead of `context.Background()` (cancellation/timeout now reaches subprocesses and Milvus calls)

### Polish (minor)

- **acpx**: Codex adopts the result-priority strategy on failure paths and accumulates per-turn usage (consistency with claude/mimo); test fixtures cleaned up (`t.Setenv`, malformed JSON fixture)
- **llm**: `Client.Generate/GenerateJSON` no longer mutate the caller's message slice; `Attempt.Duration` actually recorded; `OnFallback` reports the actual next model; all-breakers-open returns a distinct error instead of "全部 0 个模型失败"; backoff jitter guard for sub-nanosecond `BaseDelay`; dead truncation-retry branch removed from `GenerateJSON`; `OpenAIProviderConfig.MaxOutputTokens < 0` omits the `max_tokens` param (inference-model compat); `CostTracker` capped at 10k records; unused credential fields dropped from `OpenAIProvider`
- **knowledge/rag**: `sqrtF` uses `math.Sqrt` (hand-rolled Newton iterations drifted up to 60%); `Rescan` keeps the old index when the root dir is missing (no silent empty knowledge base); chunk hard cap (4000 runes) guards Milvus VarChar limit on pasted base64/logs/unclosed code fences; `NProbe <= 0` falls back to default; search-param errors no longer swallowed; filter keys whitelisted (file/heading) against expression injection; `Local.Retrieve` honors ctx; default-value logic extracted to a pure function shared with tests
- **workcopy**: `cloneURL` preserves the original scheme (no forced https upgrade for intranet http); `GIT_TERMINAL_PROMPT=0` set on all git calls
- **mcp**: empty Allow-hit results reported via `OnError` (typo'd tool names no longer silently invisible); `NewPool` ignores empty names and keeps the first duplicate
- **skill**: `..` rejected only as a path segment (names like `v1..2` allowed); frontmatter quotes stripped only when paired; symlink boundary and cache semantics documented; prompt-injection caveat documented for `ListPrompt`
- **obsx**: missing OnStart state no longer produces astronomic durations (1.7 万年) or spurious slow-call warnings; package doc example signature fixed
- **safejson**: horizontal rules (`***`/`___`), table rows, reference definitions and tab-ordered lists now neutralized; emphasis text (`***bold***`) not falsely flagged
- **severity**: trailing `/**` no longer matches the directory itself (.gitignore semantics); `?` matches one rune (multibyte filenames)
- **textutil**: negative length no longer panics; no full `[]rune` allocation when no truncation is needed
- **audit**: nil-logger/-receiver safe; test name now matches behavior
- **progress**: dropped-event counter (`Bus.Dropped()`)
- **README**: package table now covers all 16 packages; acpx protocol/sandbox support matrix corrected (9 adapters); Resilient / toolprior / mcp / agentrun / obsx quick-start sections added; glob example fixed

## v0.1.0 (2026-09-01)

Initial release — extracted from Argus v3.0.5 code review platform.

### Packages

- **llm**: LLM client with retry (exponential backoff + jitter), rate limiting (token bucket), budget tracking (TokenAccountant interface), fitInput context window guard, GenerateJSON with truncation retry
- **breaker**: Circuit breaker (closed → open → half-open → closed), per-key Breakers registry
- **worker**: DB-as-queue worker pool with TaskQueue interface, heartbeat-based crash recovery, two-phase graceful shutdown
- **knowledge/rag**: Local RAG (markdown chunking + ASCII/CJK tokenization + TF-IDF scoring), eino tool adapter
- **progress**: Generic event bus `Bus[T]` with multi-subscriber broadcast, 64-buffered channels, lossy drop
- **skill**: File-based content/methodology resolver with path traversal protection, in-process cache, checksum
- **severity**: Severity normalization, SHA256 fingerprinting, glob matching (`**`, `*`, `?`)
- **safejson**: Markdown/HTML anti-injection (headings, fences, quotes, lists, HTML comments)
- **audit**: Structured audit logging
- **textutil**: Rune-safe text truncation
- **workcopy**: Git working copy sandbox with WorktreeKey, singleflight dedup, TTL sweep, orphan cleanup

### Dependencies

- ekit v0.20.1
- eino v0.9.18
- golang.org/x/sync v0.22.0
- golang.org/x/time v0.15.0
