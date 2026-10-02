# Changelog

## v0.1.0 (2026-09-01)

Initial release — extracted from Argus v3.0.5 code review platform.

### Packages

- **llm**: LLM client with retry (exponential backoff + jitter), rate limiting (token bucket), budget tracking (TokenAccountant interface), fitInput context window guard, GenerateJSON with truncation retry
- **breaker**: Circuit breaker (closed → open → half-open → closed), per-key Breakers registry
- **worker**: DB-as-queue worker pool with TaskQueue interface, heartbeat-based crash recovery, two-phase graceful shutdown
- **knowledge/rag**: Local RAG (markdown chunking + ASCII/CJK tokenization + TF-IDF scoring), eino tool adapter
- **progress**: Generic event bus `Bus[T]` with multi-subscriber broadcast, 64-buffered channels, lossy drop
- **skill**: File-based content/methodology resolver with path traversal protection, LRU cache, checksum
- **severity**: Severity normalization, SHA256 fingerprinting, glob matching (`**`, `*`, `?`)
- **safejson**: Markdown/HTML anti-injection (headings, fences, quotes, lists, HTML comments)
- **audit**: Structured audit logging
- **textutil**: Rune-safe text truncation
- **workcopy**: Git working copy sandbox with WorktreeKey, singleflight dedup, TTL sweep, orphan cleanup

### Dependencies

- ekit v0.20.0
- eino v0.9.18
- golang.org/x/sync v0.22.0
- golang.org/x/time v0.15.0
