# Media tools

Media tools move images, files, and generated audio between the workspace and the chat.

| Tool | What it does | Switch (default) |
|------|--------------|------------------|
| `load_image` | Load a local image into the model context (vision) | `tools.load_image.enabled` (`true`) |
| `send_file` | Send a local file to the current chat | `tools.send_file.enabled` (`true`) |
| `send_tts` | Synthesize speech and send it as an audio file | `tools.send_tts.enabled` (`false`) |
| `image_generate` | Generate an image and send it to the chat | `tools.image_generate.enabled` (`false`) |

`load_image`, `send_file`, and `message` media share the same read policy: protected credential files are refused, and every file is limited to `agents.defaults.max_media_size` (default 20 MB, `PICOCLAW_AGENTS_DEFAULTS_MAX_MEDIA_SIZE`).

## `load_image`

Adds a local image to the conversation so vision-capable models can see it. Supported formats: JPEG, PNG, GIF, WebP, and BMP; the agent is expected to analyze the image in its next response.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to the local image file. Relative paths resolve from the workspace |

Oversized images are downscaled before they reach the model: `agents.defaults.vision_max_image_dimension` (default `1568` px longest edge) and `agents.defaults.vision_jpeg_quality` (default `85`) control the resize.

## `send_file`

Sends a local file (image, document, archive, …) to the user on the current chat channel.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to the local file. Relative paths resolve from the workspace |
| `filename` | string | no | Optional display filename. Defaults to the basename of `path` |

The file is copied into the shared media store and delivered by the channel. If the channel is not running (no gateway), the call fails. Files larger than `agents.defaults.max_media_size` are refused.

## `send_tts`

Synthesizes speech from text and sends it as an audio file to the user.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `text` | string | yes | Text to synthesize. Write it in a concise, spoken style: no markdown, emojis, or code |
| `filename` | string | no | Optional filename for the audio file (for example `response.ogg`) |

Configuration:

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `tools.send_tts.enabled` | bool | `false` | Register the tool |

Even when enabled, the tool is only registered when PicoClaw detects a TTS provider: set `voice.tts_model_name` to a TTS-capable entry in `model_list` (see [Configuration](../guides/configuration.md) and [Providers](../guides/providers.md)). If the tool is enabled but no provider is found, PicoClaw logs a warning at startup and the tool stays unavailable.

## `image_generate`

Generates an image from a prompt with an image-capable OpenAI, Gemini, or OpenRouter model and sends it to the current chat. Generation downloads are limited by `agents.defaults.max_media_size`.

Configuration summary: `tools.image_generate.enabled` (default `false`) and `tools.image_generate.model_name` (alias of the `model_list` entry to use; `PICOCLAW_TOOLS_IMAGE_GENERATE_MODEL_NAME`). Full setup and supported arguments are documented in the [image generation guide](image-generation.md).

## Media cleanup

Inbound and outbound media files are stored in a shared media directory (`picoclaw_media` under the system temporary directory). A background cleaner deletes stale files so long-running gateways do not accumulate data.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `tools.media_cleanup.enabled` | bool | `true` | Enable the media cleaner |
| `tools.media_cleanup.max_age_minutes` | int | `30` | Delete media files older than this |
| `tools.media_cleanup.interval_minutes` | int | `5` | How often the cleaner runs |

Environment variables use the `PICOCLAW_MEDIA_CLEANUP_` prefix: `PICOCLAW_MEDIA_CLEANUP_ENABLED`, `PICOCLAW_MEDIA_CLEANUP_MAX_AGE`, `PICOCLAW_MEDIA_CLEANUP_INTERVAL`.

The cleaner runs inside the gateway (and is rebuilt on hot reload). The agent's read allowlist automatically includes the media directory so tools and models can resolve `media://` references; other tools cannot read outside their sandbox.
