---
lang: zh
title: "疑难解答"
description: "PicoClaw 常见故障排查：model_list 缺失、OpenRouter 免费层路由、通道连接失败、日志定位与调试步骤。"
---
# 🐛 疑难解答

> 返回 [README](../project/README.zh.md)

## "model ... not found in model_list" 或 OpenRouter "free is not a valid model ID"

**症状：** 你看到以下任一错误：

- `Error creating provider: model "openrouter/free" not found in model_list`
- OpenRouter 返回 400：`"free is not a valid model ID"`

**原因：** PicoClaw 现在按两步解析 provider 和 model：

- 如果设置了 `provider`，则会把 `model` 原样发送给该 provider。
- 如果未设置 `provider`，则会把 `model` 第一个 `/` 之前的字段当作 provider，并把第一个 `/` 之后的全部内容当作最终发送的模型 ID。

对于 OpenRouter 免费层路由，推荐显式设置 `provider`。

- **错误：** `"model": "free"` → 不会选中 OpenRouter，`free` 也不是可直接路由的 OpenRouter 模型配置。
- **正确：** `"provider": "openrouter", "model": "free"` → OpenRouter 收到 `free`。
- **也兼容：** `"model": "openrouter/free"` → provider 解析为 `openrouter`，最终模型 ID 解析为 `free`。

**修复方法：** 在 `~/.picoclaw/config.json`（或你的配置路径）中：

1. **agents.defaults.model_name** 必须匹配 `model_list` 中的某个 `model_name`（例如 `"openrouter-free"`）。
2. 该条目推荐显式设置 **provider** 为 `openrouter`，并在 **model** 中填写有效的 OpenRouter 模型 ID，例如：
   - `"free"` – 自动免费层
   - `"google/gemini-2.0-flash-exp:free"`
   - `"meta-llama/llama-3.1-8b-instruct:free"`

示例片段：

```json
{
  "agents": {
    "defaults": {
      "model_name": "openrouter-free"
    }
  },
  "model_list": [
    {
      "model_name": "openrouter-free",
      "provider": "openrouter",
      "model": "free",
      "api_keys": ["sk-or-v1-YOUR_OPENROUTER_KEY"],
      "api_base": "https://openrouter.ai/api/v1"
    }
  ]
}
```

在 [OpenRouter Keys](https://openrouter.ai/keys) 获取你的密钥。

## OpenCode Go 返回 400 "Model is unavailable"

**症状：** 模型条目显式设置了 `provider`，但 `model` 中又重复了 provider 名称：

```json
{"provider": "opencode-go", "model": "opencode-go/glm-5.3-flash"}
```

OpenCode Go 返回 `400 Upstream request failed: Model is unavailable`。

**原因：** OpenCode Go 的模型 ID 是扁平命名。重复 provider 名称会同时破坏
PicoClaw 的端点路由（厂商前缀用于选择协议）和上游的模型查找。

**修复方法：** 使用裸模型 ID：

```json
{"provider": "opencode-go", "model": "glm-5.3-flash"}
```

当前版本会在路由前去掉多余的 `opencode-go/` 前缀，因此带前缀的写法也能工作。
如果模型 ID 确实不存在，仍会看到这条上游错误。
