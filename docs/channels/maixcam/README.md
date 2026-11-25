---
description: "MaixCam is a dedicated channel for connecting to Sipeed MaixCAM and MaixCAM2 AI camera devices."
---
> Back to [README](https://github.com/afjcjsbx/picoclaw/blob/main/README.md)

# MaixCam

MaixCam is a dedicated channel for connecting to Sipeed MaixCAM and MaixCAM2 AI camera devices. It uses TCP sockets for bidirectional communication and supports edge AI deployment scenarios.

## Configuration

```json
{
  "channel_list": {
    "maixcam": {
      "enabled": true,
      "type": "maixcam",
      "host": "0.0.0.0",
      "port": 18790,
      "allow_from": [],
      "placeholder": { "enabled": true, "text": ["Processing..."] },
      "group_trigger": { "mention_only": false },
      "reasoning_channel_id": ""
    }
  }
}
```

| Field      | Type   | Required | Description                                                      |
| ---------- | ------ | -------- | ---------------------------------------------------------------- |
| enabled    | bool   | Yes      | Whether to enable the MaixCam channel                            |
| host       | string | Yes      | TCP server listening address                                     |
| port       | int    | Yes      | TCP server listening port                                        |
| allow_from | array  | No       | Allowlist of device IDs; empty means all devices are allowed     |
| placeholder | object | No      | Placeholder message shown while the agent is working             |
| group_trigger | object | No    | Group trigger settings (e.g. `{ "mention_only": true }`)         |
| reasoning_channel_id | string | No | Optional device ID to route reasoning/thinking output to a separate channel |

## Use Cases

The MaixCam channel enables PicoClaw to act as an AI backend for edge devices:

- **Smart Surveillance**: MaixCAM sends image frames; PicoClaw analyzes them using vision models
- **IoT Control**: Devices send sensor data; PicoClaw coordinates responses
- **Offline AI**: Deploy PicoClaw on a local network for low-latency inference
