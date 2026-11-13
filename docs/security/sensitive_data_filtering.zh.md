---
lang: zh
description: "PicoClaw 可以从工具调用结果中过滤敏感值（API 密钥、令牌、密码等），然后再发送给 LLM。这可以防止 LLM 看到自己的凭据，避免通过工具输出泄露或产生混淆行为。"
---
# 敏感数据过滤

PicoClaw 可以从工具调用结果中过滤敏感值（API 密钥、令牌、密码等），然后再发送给 LLM。这可以防止 LLM 看到自己的凭据，避免通过工具输出泄露或产生混淆行为。

---

## 概述

当 LLM 使用的工具返回其自身的凭据时（例如，一个回显正在使用的 API 密钥的工具），这些值会自动替换为 `[FILTERED]` 再发送给 LLM，同时也会在写入磁盘的大型工具结果产物中脱敏。

敏感值从 `.security.yml` 中收集 —— 这是所有敏感配置的集中存储，包括：

- 模型 API 密钥
- 频道令牌（Telegram、Discord、Slack、Matrix 等）
- Web 工具 API 密钥（Brave、Tavily、Perplexity 等）
- 技能令牌（GitHub、ClawHub）

---

## 配置

敏感数据过滤在 `config.json` 的 `tools` 部分配置：

| 配置 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `filter_sensitive_data` | bool | `true` | 启用/禁用过滤。为 `false` 时，不进行任何过滤。 |
| `filter_min_length` | int | `8` | 触发过滤的最小内容长度。短内容会被跳过以提高性能。 |

```json
{
  "tools": {
    "filter_sensitive_data": true,
    "filter_min_length": 8
  }
}
```

### 环境变量

| 变量 | 说明 |
|------|------|
| `PICOCLAW_TOOLS_FILTER_SENSITIVE_DATA` | 设置为 `true` 或 `false` 以覆盖配置值 |

---

## 工作原理

1. **启动时**：使用反射从 `.security.yml` 中收集所有敏感值，并编译成 `strings.Replacer`（O(n+m) 性能，仅计算一次）。

2. **每个工具结果**：在将任何工具结果发送给 LLM 之前：
   - 如果 `filter_sensitive_data` 为 `false`，内容原样传递
   - 如果内容长度 < `filter_min_length`，内容原样传递（快速路径）
   - 否则，所有敏感值都会被替换为 `[FILTERED]`

3. **替换**：使用 `strings.Replacer` 进行高效的 O(n+m) 字符串替换，其中 n = 内容长度，m = 敏感值总长度。

---

## 大型工具结果产物

超过 `tools.max_inline_result_chars`（默认 `16384`）的大型工具结果会保存到 agent 工作区的 `<workspace>/.artifacts/tool/` 下，同时在模型上下文中保留有界的头尾预览与指针。`web_fetch` 将 `maxChars` 默认设为同一阈值，且按调用只能调低；较小的预览**即使页面未超过该阈值**也会写入产物（上限为 `2,000,000` 个字符），因此完整页面仍可恢复。完整行为参见[大型工具结果](../reference/tools_configuration.zh.md#大型工具结果)。

产物在**写入过程中**完成脱敏。所有产物路径——通用工具结果转存、exec 的流式 `exec_*.log`，以及 MCP 的 `.artifacts/mcp` 文件——都会经过与日志和出站消息相同的精确值脱敏层。`.security.yml` 中配置的凭据在字节流写入文件之前就会被替换为 `[FILTERED]`。即使凭据被拆到多次写入中，或跨越头尾截断边界，也能被识别：流式写入器最多只保留“最长凭据长度减一”个字节，截断点也会移动，避免将机密拆成两段。

- **模型上下文**：仍然经过过滤——结果中内嵌的预览与其他工具内容一样会经过 `filter_sensitive_data`。
- **磁盘上的产物**：已配置的凭据已替换为 `[FILTERED]`，完整载荷产物与 exec 流均是如此。该脱敏独立于 `filter_sensitive_data`：关闭模型侧过滤不会让凭据以明文落盘。
- **内存**：不会为脱敏生成完整副本。脱敏通过一个很小的携带缓冲区流式完成（最多为最长已注册凭据长度减一，通常只有几十字节），因此峰值内存仍受 I/O 块与现有头尾窗口限制。
- **残余风险**：未在 `.security.yml` 中注册的值无法被精确值脱敏识别。当命令可能输出 PicoClaw 不知道的凭据时，仍应将 `<workspace>/.artifacts/` 视为敏感数据，并避免用 `send_file` 将产物转发到不可信目标。

产物目录以 `0700`、文件以 `0600` 创建；权限仍然是对内容脱敏的补充，而不是替代。

---

## 示例

给定以下 `.security.yml`：

```yaml
model_list:
  my-model:
    api_keys:
      - sk-secret-key-12345

channels:
  telegram:
    token: "123456:ABC-DEF"
```

以及包含以下内容的工具结果：

```
The model is using API key sk-secret-key-12345 and Telegram bot 123456:ABC-DEF
```

LLM 将收到：

```
The model is using API key [FILTERED] and Telegram bot [FILTERED]
```

---

## 性能

- **快速路径**：短于 `filter_min_length`（默认 8）的内容会直接返回，不进行任何字符串扫描
- **高效替换**：使用 `strings.Replacer`，复杂度为 O(n+m)，而非正则表达式
- **延迟初始化**：替换映射通过 `sync.Once` 在首次访问时构建一次
- **流式产物**：产物脱敏为 O(n)，仅保留不超过最长已注册凭据长度的携带缓冲区，因此保存数兆字节的转存不会在内存中产生第二份副本

---

## 安全注意事项

- **凭据泄露防护**：如果没有过滤，返回凭据的工具可能导致 LLM 看到自己的 API 密钥，可能导致日志中泄露凭据或产生混淆
- **纵深防御**：过滤是对凭据加密的补充（而非替代）—— 应同时使用这两个功能
- **无误报**：只有明确存储在 `.security.yml` 中的值才会被过滤；LLM 的通用知识不受影响
- **产物在磁盘上已脱敏**：大型工具结果产物与 exec 转存文件会经过相同的精确值脱敏，且独立于 `filter_sensitive_data`（见[大型工具结果产物](#大型工具结果产物)）。未出现在 `.security.yml` 中的凭据仍要求将 `<workspace>/.artifacts/` 视为敏感数据

---

## 相关文档

- [凭据加密](./credential_encryption.zh.md) — 配置中 API 密钥的加密
- [工具配置](../reference/tools_configuration.zh.md)
