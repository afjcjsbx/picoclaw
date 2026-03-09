# Adaptive Model Routing

Adaptive Model Routing is an opt-in feature that runs a cheap or local model first,
validates whether the result actually completed the job, and automatically escalates
to a cloud model on failure — all in one turn.

## How it differs from Failover

PicoClaw already has a **Failover** mechanism that handles provider-level failures
(rate limits, auth errors, timeouts). Adaptive Model Routing is a separate layer
that handles _outcome quality_: the LLM call may succeed at the provider level but
still produce an inadequate response (empty output, truncation, tool errors).

|                 | Failover                    | Adaptive Model Routing                          |
|-----------------|-----------------------------|-------------------------------------------------|
| Trigger         | Provider error / rate limit | Outcome validation failure                      |
| Default         | On                          | Off                                             |
| Max retries     | Configurable fallback chain | 1 escalation (v1)                               |
| Session history | Same session, next model    | Rerun from scratch (local attempt not appended) |

## How it works

1. The user message arrives and enters the agent loop as normal.
2. Instead of calling the primary model directly, the adaptive runner
   first executes the full LLM iteration (including tool calls) using the
   **local model** (`local_first_model`).
3. The outcome is validated — by heuristic (default) or by an optional LLM validator.
4. If validation **passes**: the local result is returned to the user. No cloud
   call is made, saving cost and latency.
5. If validation **fails**: the local run is discarded (session history is rolled
   back) and the full LLM iteration is re-attempted with the **cloud model**
   (`cloud_escalation_model`).
6. If the cloud run encounters a provider error, the normal failover chain takes
   over (fallback candidates, cooldown, retry).

```
User message
    |
    v
+-- Adaptive Runner --------------------------------+
|                                                    |
|  1. Run with local model                           |
|       |                                            |
|       v                                            |
|  2. Validate outcome                               |
|       |           |                                |
|    PASS         FAIL                               |
|       |           |                                |
|  Return local   3. Rollback session                |
|  result            |                               |
|                 4. Run with cloud model             |
|                    |                               |
|                 5. Return cloud result              |
|                    (failover chain available)       |
+----------------------------------------------------+
```

## Configuration

Add `adaptive_routing` inside `agents.defaults` in your `config.json`:

```json
{
  "agents": {
    "defaults": {
      "model_name": "openai/gpt-4.1-mini",
      "model_fallbacks": [
        "openai/gpt-4.1-nano"
      ],
      "adaptive_routing": {
        "enabled": true,
        "local_first_model": "ollama/qwen2.5-coder",
        "cloud_escalation_model": "openai/gpt-4.1-mini",
        "max_escalations": 1,
        "bypass_on_explicit_override": true,
        "validation": {
          "mode": "heuristic",
          "min_score": 0.75
        }
      }
    }
  }
}
```

### Configuration reference

| Field                         | Type   | Default       | Description                                                                 |
|-------------------------------|--------|---------------|-----------------------------------------------------------------------------|
| `enabled`                     | bool   | `false`       | Enable adaptive model routing                                               |
| `local_first_model`           | string | —             | Model name (from `model_list`) to try first. Typically a local/cheap model  |
| `cloud_escalation_model`      | string | —             | Model name to escalate to when local validation fails                       |
| `max_escalations`             | int    | `1`           | Maximum number of escalations per turn (v1 hard cap: 1)                     |
| `bypass_on_explicit_override` | bool   | `false`       | Skip adaptive routing when the user explicitly switches model via `/switch` |
| `validation.mode`             | string | `"heuristic"` | Validation strategy: `"heuristic"` or `"llm"`                               |
| `validation.min_score`        | float  | `0.75`        | Score threshold for passing validation (0.0–1.0)                            |

Both `local_first_model` and `cloud_escalation_model` must reference a valid
`model_name` in your `model_list`. If either model cannot be resolved, adaptive
routing is disabled for that agent with a warning log.

### LLM validation mode

When `mode` is set to `"llm"`, the adaptive runner sends the user's original
message and the assistant's response to a small validator model. The validator
returns a JSON verdict with a score, pass/fail flag, and a reason. If the
validator model is unreachable or returns invalid JSON, the response is treated
as a validation failure (triggering escalation).

The validator model receives a structured prompt containing:
- The user's original message
- The assistant's response (truncated to `max_assistant_chars`)
- Tool call information if present (truncated to `max_tool_output_chars`)
- Metadata (finish reason, token usage)

If `validator_model` is empty or the provider is nil, the system falls back to
heuristic validation with a warning log.

#### LLM validation configuration

| Field                              | Type   | Default | Description                                      |
|------------------------------------|--------|---------|--------------------------------------------------|
| `validation.validator_model`       | string | —       | Model to use as the validator (required for LLM mode) |
| `validation.max_tool_output_chars` | int    | `2000`  | Truncate tool output before sending to validator |
| `validation.max_assistant_chars`   | int    | `4000`  | Truncate assistant content before sending        |
| `validation.redact_secrets`        | bool   | `false` | Redact secrets (API keys, tokens) before sending to validator |

#### Example LLM validation config

```json
"validation": {
  "mode": "llm",
  "min_score": 0.75,
  "validator_model": "openai/gpt-4.1-nano",
  "max_tool_output_chars": 2000,
  "max_assistant_chars": 4000,
  "redact_secrets": true
}
```

## Heuristic validation

The default heuristic validator starts from a score of **1.0** and applies the
following deductions:

| Condition                       | Score deduction | Notes                                           |
|---------------------------------|-----------------|-------------------------------------------------|
| Provider/runtime error          | Score = 0.0     | Immediate fail (early return)                   |
| Tool execution error            | -0.6            | `finish_reason` is `"error"` or `"tool_error"`  |
| Empty assistant output          | -0.4            | No content and no tool calls                    |
| Pending (unresolved) tool calls | -0.4            | Tool calls present but no content               |
| Timeout / truncation            | -0.3            | `finish_reason` is `"length"` or `"max_tokens"` |

A response **passes** when:

- The score is >= `validation.min_score` (default 0.75), **and**
- There are no failure conditions (no deductions were applied)

This means a truncated response (score 0.7) will always trigger escalation at the
default threshold, even though 0.7 is close to 0.75, because having _any_ failure
condition is treated as a signal that the local model didn't complete the job.

## Session handling

When the local model attempt fails validation:

1. The session history is rolled back to the state before the local attempt.
2. The cloud model receives the exact same input messages (from scratch).
3. Only the cloud model's tool calls and responses are persisted in the session.

This ensures that a failed local attempt never pollutes the conversation history
with partial or incorrect results.

## Interaction with other features

### Failover

Each adaptive attempt (local and cloud) has full access to the normal provider
fallback chain. Adaptive routing only escalates based on _outcome quality_, not on
provider errors. If the local model's provider is rate-limited, the failover chain
handles it before adaptive validation even runs.

### Model routing (light/heavy)

Adaptive routing and complexity-based model routing (`routing` config) are
independent features. When adaptive routing provides override candidates,
`selectCandidates` (the complexity router) is bypassed. You can use both
features simultaneously, but typically you would choose one or the other.

### `/switch model` command

When a user explicitly switches the model via `/switch model to <name>` and
`bypass_on_explicit_override` is `true`, adaptive routing is bypassed. The
switched model is used directly without any local-first / escalation logic.

## Example scenarios

### Local Ollama + cloud OpenAI

Run a local Ollama model for fast, free responses. Escalate to OpenAI only when
the local model fails to produce a good answer:

```json
{
  "model_list": [
    {
      "model_name": "ollama/qwen2.5-coder",
      "model": "ollama/qwen2.5-coder",
      "api_base": "http://localhost:11434/v1"
    },
    {
      "model_name": "openai/gpt-4.1-mini",
      "model": "openai/gpt-4.1-mini",
      "api_key": "sk-..."
    }
  ],
  "agents": {
    "defaults": {
      "model_name": "openai/gpt-4.1-mini",
      "adaptive_routing": {
        "enabled": true,
        "local_first_model": "ollama/qwen2.5-coder",
        "cloud_escalation_model": "openai/gpt-4.1-mini",
        "max_escalations": 1,
        "bypass_on_explicit_override": true,
        "validation": {
          "mode": "heuristic",
          "min_score": 0.75
        }
      }
    }
  }
}
```

### Strict validation threshold

If you want the local model to pass only when the response is perfect (no
truncation, no errors of any kind), set `min_score` to `1.0`:

```json
"validation": {
"mode": "heuristic",
"min_score": 1.0
}
```

### Relaxed threshold

If you're willing to accept slightly degraded local responses (e.g. truncated but
still useful), lower the threshold. Note that any failure condition still triggers
escalation regardless of score:

```json
"validation": {
"mode": "heuristic",
"min_score": 0.5
}
```

## Troubleshooting

### Adaptive routing is not activating

Check the logs for these messages:

- `adaptive: local_first_model "..." or cloud_escalation_model "..." not found in model_list`
  — One or both model names don't match any `model_name` in your `model_list`.
- `Adaptive routing bypassed: user explicitly switched model`
  — The user ran `/switch model to ...` and `bypass_on_explicit_override` is true.

### Local model always fails

If the local model consistently produces empty responses or errors, check:

1. That the local model server (e.g. Ollama) is running and accessible.
2. That the model name in `model_list` matches what the provider expects.
3. The provider logs for connection errors or auth issues.

### Performance considerations

Each adaptive escalation doubles the latency for that turn (local attempt + cloud
attempt). If the local model fails frequently, consider:

- Using a more capable local model.
- Lowering `min_score` if partial responses are acceptable.
- Disabling adaptive routing and using a cloud model directly.
