# Ask User tool

`ask_user` lets the agent pause a task and ask the user one or more questions. It
is enabled by default. Disable it with `"tools": {"ask_user": {"enabled": false}}`
or `PICOCLAW_TOOLS_ASK_USER_ENABLED=false`. Agents with an explicit `AGENT.md`
tool allowlist must include `ask_user` to use it.

The agent decides when a clarification is necessary; the tool does not infer it.
It supports free-text questions, single-choice questions, multi-choice
questions, and batches of one to five independent questions.

## Input

```json
{
  "questions": [
    {
      "question": "Which format do you prefer?",
      "choices": ["JSON", "YAML", "XML"]
    },
    {
      "question": "Which features do you need?",
      "choices": ["Authentication", "Logging", "Caching"],
      "multi_select": true
    },
    {
      "question": "Any other requirements?"
    }
  ]
}
```

Rules:

- 1–5 questions per call.
- `question` must be non-empty after trimming and at most 1,000 characters.
- `choices` is optional. Omit it, use `null`, or use `[]` for a free-text
  question. A non-empty list made only of blank strings is a validation error.
- At most four valid choices are kept; extra choices are truncated in order.
- Each choice may contain at most 8,000 characters after trimming.
- The rendered prompt of a single question (question, numbered options and
  instructions) must fit the `tools.ask_user.max_prompt_chars` budget (default
  `1900`, enough for Telegram and Discord). An oversized prompt is rejected
  before any question is delivered, so a long batch never starts halfway.
- User answers are capped at 2,000 characters. Longer answers are truncated and
  the result carries a `notice`.
- `multi_select` defaults to `false` and is ignored on questions without
  choices.
- When a question has at least two choices, the first choice is displayed with
  a `(Recommended)` marker. The marker is presentation-only and never appears in
  the result returned to the model.
- Questions in one batch must be independent. If a later question depends on an
  earlier answer, the agent must make a separate `ask_user` call in a later
  model response. Two `ask_user` calls emitted in the same response are not
  executed: the second is rejected with an explicit error, because only one
  interaction can wait for a reply at a time.

## Chat interaction

PicoClaw currently ships a text/chat adapter. It presents one question at a
time in the active conversation:

```
Which format do you prefer?

1. JSON (Recommended)
2. YAML
3. XML

Reply with a number, or type your own answer (Other).
Type `/skip` to skip this question or `/cancel` to cancel.
```

- Single choice: reply with the option number, the option text, or custom text
  for the implicit **Other** option. An exact option text wins over a number:
  with options `2` and `1`, a reply of `1` selects the option labeled `1`.
- Multi-select: reply with one or more option numbers separated by commas (for
  example `1,3`), or custom text. A custom answer containing commas is treated
  as one entry when no option number or exact option label is present.
- Free text: reply with any non-empty text. An empty or media-only message is
  ignored and the question keeps waiting.
- `/skip` (case-insensitive) marks the question as explicitly skipped.
- `/cancel` (case-insensitive) ends the batch with outcome `cancelled`. Answers
  already collected are preserved.
- On questions with choices, the bare words `skip` and `cancel` are accepted as
  shortcuts too, unless an option uses that exact word as its label. On open
  questions they are ordinary answers, so use the slash forms to skip or
  cancel.

The interaction is bound to the conversation **and to the sender who started
the turn**. A reply from another participant of a group chat, from another
agent, or from another session is not consumed by `ask_user`: it follows the
normal routing rules and the pending question keeps waiting (until it times
out). This prevents a different user or agent from answering on the requester's
behalf.

The adapter keeps the answers already confirmed and never sends later questions
after a timeout. The timeout is configured with
`tools.ask_user.timeout_seconds` (default `300`) and starts when the first
question is delivered. A timeout produces a complete result with the answers
collected so far.

Prompt size is checked for every question before the first message is sent. A
question whose rendered prompt exceeds `tools.ask_user.max_prompt_chars`
(default `1900`) fails with a validation error and nothing is delivered.

The text adapter requires an interactive chat turn driven by the agent loop.
Invocations that cannot receive an inbound reply, such as direct, cron or
heartbeat runs, return outcome `undelivered` with an explanatory notice instead
of blocking until the timeout.

## Output

```json
{
  "responses": [
    {
      "question": "Which format do you prefer?",
      "choices_offered": ["JSON", "YAML", "XML"],
      "status": "answered",
      "user_response": "JSON"
    },
    {
      "question": "Which features do you need?",
      "choices_offered": ["Authentication", "Logging", "Caching"],
      "status": "answered",
      "user_response": ["Authentication", "Logging"]
    },
    {
      "question": "Any other requirements?",
      "choices_offered": null,
      "status": "skipped",
      "user_response": null
    }
  ],
  "outcome": "submitted"
}
```

- `responses` has exactly one entry per normalized question, in input order.
- `choices_offered` contains the normalized options without the recommended
  marker, or `null` for open questions.
- `status` is `answered`, `skipped`, or `unanswered`.
  - `skipped`: the key was present in the adapter result but the value was
    empty or `null`.
  - `unanswered`: the key was not present at all.
- `user_response` is `null` unless the status is `answered`. Multi-select answers
  are always arrays, even with a single selected option.
- `outcome` is one of `submitted`, `cancelled`, `timed_out`, or `undelivered`.
- `notice` is included only when the adapter reports a timeout or delivery
  problem, for example `[user did not respond within 5m]`, or when an answer was
  truncated to the 2,000-character cap.

Validation errors and adapter failures are returned through the tool error
channel as `{"error": "..."}`. They are never returned as an apparently
successful result. Validation runs in the core before any message is delivered.

## Configuration

| Config key | Type | Default | Description |
|------------|------|---------|-------------|
| `tools.ask_user.enabled` | bool | `true` | Registers the `ask_user` tool |
| `tools.ask_user.timeout_seconds` | int | `300` | Seconds to wait for the user, measured from the first delivered question |
| `tools.ask_user.max_prompt_chars` | int | `1900` | Maximum characters in one rendered question prompt; larger prompts are rejected before delivery |
