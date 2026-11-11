---
lang: zh
title: "异步任务与 Spawn"
description: "PicoClaw 通过 spawn 工具支持异步任务执行。主要由 Heartbeat（心跳） 系统使用，在不阻塞主 Agent 循环的情况下运行耗时任务。"
---
# 🔄 异步任务与 Spawn

> 返回 [README](../project/README.zh.md)

PicoClaw 通过 `spawn` 工具支持**异步任务执行**。主要由 **Heartbeat（心跳）** 系统使用，在不阻塞主 Agent 循环的情况下运行耗时任务。

## Heartbeat

心跳系统会定期检查 `workspace/HEARTBEAT.md` 中的计划任务。首次运行时会自动生成默认模板，你可以自定义它来定义快速任务（内联处理）和长任务（通过 `spawn` 委派）。

**`HEARTBEAT.md` 示例：**

```markdown
## Quick Tasks (respond directly)

- Report current time

## Long Tasks (use spawn for async)

- Search the web for AI news and summarize
- Check email and report important messages
```

**关键行为：**

| 特性             | 描述                                       |
| ---------------- | ------------------------------------------ |
| **spawn**        | 创建异步子 Agent，不阻塞主心跳进程         |
| **独立上下文**   | 子 Agent 拥有独立上下文，无会话历史        |
| **出站工具**     | 默认情况下子 Agent 可直接向用户发送消息    |
| **非阻塞**       | spawn 后，心跳继续处理下一个任务           |
| **结果回传**     | 子任务完成后结果始终回到父 Agent 的轮次    |

#### 子 Agent 通信原理

```
心跳触发 (Heartbeat triggers)
    ↓
Agent 读取 HEARTBEAT.md
    ↓
对于长任务: spawn 子 Agent
    ↓                           ↓
继续下一个任务               子 Agent 独立工作
    ↓                           ↓
所有任务完成                 结果回到父 Agent 的轮次
    ↓                           ↓
响应 HEARTBEAT_OK            父 Agent 转发，或子 Agent
                             直接发送（允许出站时）
```

默认情况下，子 Agent 继承父 Agent 的完整工具集，包括出站工具
（`message`、`send_file`、`send_tts`、`image_generate` 和 `reaction`），
因此无需经过主 Agent 即可直接联系用户。

有两个开关可以控制这一行为：

- `spawn` 调用上的 `allow_outbound` 只对当前任务生效。
- `agents.defaults.subturn.direct_message` 设置所有子任务的默认行为
  （除非显式配置，否则为 `true`）。

单次调用只能收紧全局设置：`allow_outbound: false` 始终为该任务禁用出站，而
`true`（或省略）会遵循配置的默认值，不能解除全局的 `direct_message: false`。

禁用出站后，子 Agent 会在没有整套出站工具的情况下运行；其余工具（文件、
Web、exec 等）仍然可用，最终结果会返回父 Agent 的轮次，由父 Agent 决定是否
转发。频道和聊天上下文依然保留，供只用于作用域限定的工具（如
`spawn_status`）使用。

```json
{
  "agent_id": "researcher",
  "task": "汇总最新报告",
  "allow_outbound": false
}
```

```json
{
  "agents": {
    "defaults": {
      "subturn": {
        "direct_message": false
      }
    }
  }
}
```

也可以通过环境变量 `PICOCLAW_AGENTS_DEFAULTS_SUBTURN_DIRECT_MESSAGE=false`
修改全局默认值。

**配置：**

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

| 选项       | 默认值 | 描述                         |
| ---------- | ------ | ---------------------------- |
| `enabled`  | `true` | 启用/禁用心跳                |
| `interval` | `30`   | 检查间隔，单位分钟 (最小: 5) |

**环境变量:**

- `PICOCLAW_HEARTBEAT_ENABLED=false` 禁用
- `PICOCLAW_HEARTBEAT_INTERVAL=60` 更改间隔
