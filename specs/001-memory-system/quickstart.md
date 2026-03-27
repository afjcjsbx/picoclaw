# Quickstart: Memory System

**Branch**: `001-memory-system` | **Date**: 2026-03-27

## Prerequisites

- Go 1.25+ (as per `go.mod`)
- PicoClaw built and running
- For hybrid search: an API key for at least one embedding provider (OpenAI, Gemini, Voyage, or Mistral)

## 1. Basic Setup (FTS-only, no configuration needed)

Place markdown files in your workspace:

```
workspace/
├── MEMORY.md              # Long-term curated memory (evergreen)
└── memory/
    ├── project-notes.md   # Thematic file (evergreen)
    ├── 2026-03-27.md      # Daily log (subject to temporal decay)
    └── api-reference.md   # Reference file (evergreen)
```

The memory system discovers and indexes these files automatically on first search or explicit sync.

## 2. Enable Hybrid Search (with embeddings)

Add memory configuration to your PicoClaw config file:

```yaml
memory:
  provider:
    provider: openai
    model: text-embedding-3-small
    remote:
      apiKey: "sk-your-key-here"
```

Other provider examples:

```yaml
# Gemini
memory:
  provider:
    provider: gemini
    model: gemini-embedding-001
    remote:
      apiKey: "your-gemini-key"

# Auto-select (tries local, then OpenAI, Gemini, Voyage, Mistral)
memory:
  provider:
    provider: auto
    remote:
      apiKey: "your-key"
```

## 3. Search Memory

From the agent's tool interface or programmatically:

```
Search query: "how does the routing system work"
→ Returns up to 6 results ranked by hybrid score (vector 0.7 + BM25 0.3)
```

Without an embedding provider configured, search uses FTS-only mode (BM25 keyword matching).

## 4. Verify the Index

Check the system status to confirm indexing:

```
Status:
  Backend: builtin
  Provider: openai
  Model: text-embedding-3-small
  Files indexed: 4
  Chunks indexed: 12
  FTS available: true
  Vector available: true
```

## 5. Optional Features

### Enable Session Memory (experimental)

```yaml
memory:
  sources:
    - memory
    - sessions
```

### Enable Temporal Decay

```yaml
memory:
  query:
    hybrid:
      temporalDecay:
        enabled: true
        halfLifeDays: 30
```

### Enable MMR Diversification

```yaml
memory:
  query:
    hybrid:
      mmr:
        enabled: true
        lambda: 0.7
```

### Add Extra Paths

```yaml
memory:
  extraPaths:
    - "/path/to/shared/docs"
    - "./relative/to/workspace"
```

## Validation Checklist

- [ ] `MEMORY.md` or `memory/` directory exists in workspace
- [ ] Running a search returns results from indexed files
- [ ] Modified files are re-indexed on next sync
- [ ] (If embeddings configured) Status shows provider and model active
- [ ] (If embeddings configured) Semantic queries return relevant results
- [ ] System works in FTS-only mode when no provider is configured
- [ ] Deleted memory files are removed from index on next sync
