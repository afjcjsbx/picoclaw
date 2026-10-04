# Image generation

PicoClaw can generate images through the `image_generate` tool using a model configured in `model_list`. The generated image is sent to the current chat.

Configure an image-generating OpenAI, Gemini, or OpenRouter model in `model_list`, keep its API key in `.security.yml`, then select its `model_name` alias and enable the tool:

```json
{
  "model_list": [
    {
      "model_name": "image-model",
      "model": "gemini/gemini-3.1-flash-image"
    }
  ],
  "tools": {
    "image_generate": {
      "enabled": true,
      "model_name": "image-model"
    }
  }
}
```

For OpenRouter, use a model from its image catalog that accepts text-only prompts and produces a raster image. For example, Meta Muse Image:

```json
{
  "model_list": [
    {
      "model_name": "muse-image",
      "provider": "openrouter",
      "model": "meta/muse-image",
      "api_base": "https://openrouter.ai/api/v1"
    }
  ],
  "tools": {
    "image_generate": {
      "enabled": true,
      "model_name": "muse-image"
    }
  }
}
```

Add the OpenRouter key to `~/.picoclaw/.security.yml`:

```yaml
model_list:
  muse-image:
    api_keys:
      - "YOUR_OPENROUTER_API_KEY"
```

Put the API key in `~/.picoclaw/.security.yml`, using the same `model_name` as the entry in `model_list`:

```yaml
model_list:
  image-model:
    api_keys:
      - "YOUR_GEMINI_API_KEY"
```

For OpenAI, use a model that supports the Images API. For Gemini, choose a model that supports image output. For OpenRouter, use an image generation model slug; PicoClaw calls OpenRouter's `POST /api/v1/images` endpoint. The API key is loaded from `.security.yml`; `api_base` is read from the selected `model_list` entry. `model_name` can also be set with `PICOCLAW_TOOLS_IMAGE_GENERATE_MODEL_NAME`; enable the tool with `PICOCLAW_TOOLS_IMAGE_GENERATE_ENABLED=true`.

The tool accepts a prompt and an optional `aspect_ratio` (`square`, `landscape`, or `portrait`) for OpenAI and Gemini. OpenRouter receives only the model and prompt, so it uses the model's default image shape. The tool generates one image per call. Downloads are limited by `agents.defaults.max_media_size` (20 MB by default).
