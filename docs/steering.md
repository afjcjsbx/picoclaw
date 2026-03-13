# Steering

Steering allows injecting messages into an already-running agent loop, interrupting it between tool calls without waiting for the entire cycle to complete.

## How it works

When the agent is executing a sequence of tool calls (e.g. the model requested 3 tools in a single turn), steering checks the queue **after each tool** completes. If it finds queued messages:

1. The remaining tools are **skipped** and receive `"Skipped due to queued user message."` as their result
2. The steering messages are **injected into the conversation context**
3. The model is called again with the updated context, including the user's steering message

```
User ──► Steer("change approach")
                │
Agent Loop      ▼
  ├─ tool[0] ✔  (executed)
  ├─ [polling] → steering found!
  ├─ tool[1] ✘  (skipped)
  ├─ tool[2] ✘  (skipped)
  └─ new LLM turn with steering message
```

## Configuration

In `config.json`, under `agents.defaults`:

```json
{
  "agents": {
    "defaults": {
      "steering_mode": "one-at-a-time"
    }
  }
}
```

### Modes

| Value | Behavior |
|-------|----------|
| `"one-at-a-time"` | **(default)** Dequeues only one message per polling cycle. If there are 3 messages in the queue, they are processed one at a time across 3 successive iterations. |
| `"all"` | Drains the entire queue in a single poll. All pending messages are injected into the context together. |

The environment variable `PICOCLAW_AGENTS_DEFAULTS_STEERING_MODE` can be used as an alternative.

## Go API

### Steer — Send a steering message

```go
agentLoop.Steer(providers.Message{
    Role:    "user",
    Content: "change direction, focus on X instead",
})
```

The message is enqueued in a thread-safe manner. It will be picked up at the next polling point (after the current tool finishes).

### SteeringMode / SetSteeringMode

```go
// Read the current mode
mode := agentLoop.SteeringMode() // SteeringOneAtATime | SteeringAll

// Change it at runtime
agentLoop.SetSteeringMode(agent.SteeringAll)
```

### Continue — Resume an idle agent

When the agent is idle (it has finished processing and its last message was from the assistant), `Continue` checks if there are steering messages in the queue and uses them to start a new cycle:

```go
response, err := agentLoop.Continue(ctx, sessionKey, channel, chatID)
if response == "" {
    // No steering messages in queue, the agent stays idle
}
```

`Continue` internally uses `SkipInitialSteeringPoll: true` to avoid double-dequeuing the same messages (since it already extracted them and passes them directly as input).

## Polling points in the loop

Steering is checked at **three points** in the agent cycle:

1. **At loop start** — before the first LLM call, to catch messages enqueued during setup
2. **After each tool** — between tool calls within the same batch
3. **After the last tool** — to catch messages that arrived while the last tool was executing

## Skipped tool behavior

When steering interrupts a batch of tool calls, the tools that were not yet executed receive a `tool` result with:

```
Content: "Skipped due to queued user message."
```

This is saved to the session and sent to the model, so it is aware that some requested actions were not performed.

## Full flow example

```
1. User: "search for info on X, write a file, and send me a message"

2. LLM responds with 3 tool calls: [web_search, write_file, message]

3. web_search is executed → result saved

4. [polling] → User called Steer("no, search for Y instead")

5. write_file is skipped → "Skipped due to queued user message."
   message is skipped    → "Skipped due to queued user message."

6. Message "search for Y instead" injected into context

7. LLM receives the full updated context and responds accordingly
```

## Notes

- Steering **does not interrupt** a tool that is currently executing. It waits for the current tool to finish, then checks the queue.
- With `one-at-a-time` mode, if multiple messages are enqueued rapidly, they will be processed one per iteration. This gives the model the opportunity to react to each message individually.
- With `all` mode, all pending messages are combined into a single injection. Useful when you want the agent to receive all the context at once.
