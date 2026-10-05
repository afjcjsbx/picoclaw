package integrationtools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/common"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type ImageGenerateTool struct {
	config      *config.Config
	modelName   string
	maxFileSize int
	mediaStore  media.MediaStore
}

func NewImageGenerateTool(cfg *config.Config, maxFileSize int, store media.MediaStore) *ImageGenerateTool {
	if maxFileSize <= 0 {
		maxFileSize = config.DefaultMaxMediaSize
	}
	modelName := ""
	if cfg != nil {
		modelName = strings.TrimSpace(cfg.Tools.ImageGenerate.ModelName)
	}
	return &ImageGenerateTool{
		config:      cfg,
		modelName:   modelName,
		maxFileSize: maxFileSize,
		mediaStore:  store,
	}
}

func (t *ImageGenerateTool) Name() string { return "image_generate" }

func (t *ImageGenerateTool) Description() string { return "Generate an image from a text prompt" }

func (t *ImageGenerateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "A detailed description of the image to generate.",
			},
			"aspect_ratio": map[string]any{
				"type":        "string",
				"enum":        []string{"square", "landscape", "portrait"},
				"description": "Image shape for OpenAI and Gemini; defaults to square. OpenRouter uses the model default.",
			},
		},
		"required": []string{"prompt"},
	}
}

func (t *ImageGenerateTool) SetMediaStore(store media.MediaStore) { t.mediaStore = store }

func (t *ImageGenerateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	prompt, _ := args["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return tools.ErrorResult("prompt is required")
	}
	if t.config == nil || t.modelName == "" {
		return tools.ErrorResult(
			"configure tools.image_generate.model_name with an image generation model from model_list",
		)
	}
	if t.mediaStore == nil {
		return tools.ErrorResult("media store not configured")
	}
	channel, chatID := tools.ToolChannel(ctx), tools.ToolChatID(ctx)
	if channel == "" || chatID == "" {
		return tools.ErrorResult("no target channel/chat available")
	}
	modelCfg, err := t.config.GetModelConfig(t.modelName)
	if err != nil {
		return tools.ErrorResult(err.Error())
	}
	if modelCfg.APIKey() == "" {
		return tools.ErrorResult(fmt.Sprintf("model %q has no API key configured", t.modelName))
	}
	protocol, modelID := providers.ExtractProtocol(modelCfg)
	if strings.HasPrefix(strings.ToLower(modelID), protocol+"/") {
		modelID = modelID[len(protocol)+1:]
	}
	apiBase := providers.ResolveAPIBase(modelCfg)
	client := common.NewHTTPClient(modelCfg.Proxy)
	client.Timeout = 3 * time.Minute
	if client.Transport != nil {
		defer client.CloseIdleConnections()
	}

	aspect := strings.ToLower(strings.TrimSpace(stringArg(args, "aspect_ratio")))
	if aspect == "" {
		aspect = "square"
	}
	if aspect != "square" && aspect != "landscape" && aspect != "portrait" {
		return tools.ErrorResult("aspect_ratio must be square, landscape, or portrait")
	}

	var imageBytes []byte
	switch protocol {
	case "openai":
		imageBytes, _, err = t.generateOpenAIImage(ctx, client, apiBase, modelCfg.APIKey(), modelID, prompt, aspect)
	case "gemini":
		imageBytes, _, err = t.generateGeminiImage(ctx, client, apiBase, modelCfg.APIKey(), modelID, prompt, aspect)
	case "openrouter":
		imageBytes, err = t.generateOpenRouterImage(ctx, client, apiBase, modelCfg.APIKey(), modelID, prompt)
	default:
		return tools.ErrorResult(fmt.Sprintf(
			"image generation is not implemented for provider %q; select an OpenAI, Gemini, or OpenRouter image model",
			protocol,
		))
	}
	if err != nil {
		return tools.ErrorResult(err.Error()).WithError(err)
	}
	contentType, ext, err := validateGeneratedImage(imageBytes, t.maxFileSize)
	if err != nil {
		return tools.ErrorResult(err.Error())
	}

	file, err := os.CreateTemp("", "picoclaw-generated-*"+ext)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("failed to create generated image file: %v", err))
	}
	path := file.Name()
	if _, writeErr := file.Write(imageBytes); writeErr != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return tools.ErrorResult(fmt.Sprintf("failed to save generated image: %v", writeErr))
	}
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(path)
		return tools.ErrorResult(fmt.Sprintf("failed to close generated image: %v", closeErr))
	}

	scope := fmt.Sprintf("tool:image_generate:%s:%s", channel, chatID)
	ref, err := t.mediaStore.Store(path, media.MediaMeta{
		Filename:      "generated-image" + ext,
		ContentType:   contentType,
		Source:        "tool:image_generate",
		CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
	}, scope)
	if err != nil {
		_ = os.Remove(path)
		return tools.ErrorResult(fmt.Sprintf("failed to register generated image: %v", err))
	}
	return (&tools.ToolResult{
		ForLLM: "Generated and sent one image.",
		Media:  []string{ref},
	}).WithResponseHandled()
}

func (t *ImageGenerateTool) generateOpenAIImage(
	ctx context.Context,
	client *http.Client,
	apiBase, apiKey, model, prompt, aspect string,
) ([]byte, string, error) {
	size := "1024x1024"
	switch aspect {
	case "landscape":
		size = "1536x1024"
	case "portrait":
		size = "1024x1536"
	}
	body, err := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
		"size":   size,
		"n":      1,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode image request: %w", err)
	}
	respBody, err := t.postJSON(ctx, client, strings.TrimRight(apiBase, "/")+"/images/generations", apiKey, "", body)
	if err != nil {
		return nil, "", err
	}
	var response struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil || len(response.Data) == 0 {
		return nil, "", fmt.Errorf("image model returned no image data")
	}
	if encoded := response.Data[0].B64JSON; encoded != "" {
		data, err := base64.StdEncoding.DecodeString(encoded)
		return data, "", err
	}
	if response.Data[0].URL == "" {
		return nil, "", fmt.Errorf("image model returned neither image data nor a URL")
	}
	return t.downloadImage(ctx, response.Data[0].URL)
}

func (t *ImageGenerateTool) generateOpenRouterImage(
	ctx context.Context,
	client *http.Client,
	apiBase, apiKey, model, prompt string,
) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode image request: %w", err)
	}
	respBody, err := t.postJSON(ctx, client, strings.TrimRight(apiBase, "/")+"/images", apiKey, "", body)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err = json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to decode OpenRouter image response: %w", err)
	}
	if len(response.Data) == 0 || response.Data[0].B64JSON == "" {
		return nil, fmt.Errorf("OpenRouter returned no image data")
	}
	imageBytes, err := base64.StdEncoding.DecodeString(response.Data[0].B64JSON)
	if err != nil {
		return nil, fmt.Errorf("failed to decode OpenRouter image data: %w", err)
	}
	return imageBytes, nil
}

func (t *ImageGenerateTool) generateGeminiImage(
	ctx context.Context,
	client *http.Client,
	apiBase, apiKey, model, prompt, aspect string,
) ([]byte, string, error) {
	body, err := json.Marshal(map[string]any{
		"contents": []any{map[string]any{
			"parts": []any{map[string]string{"text": prompt}},
		}},
		"generationConfig": map[string]any{
			"responseModalities": []string{"IMAGE"},
			"imageConfig": map[string]string{
				"aspectRatio": map[string]string{
					"square":    "1:1",
					"landscape": "4:3",
					"portrait":  "3:4",
				}[aspect],
			},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode image request: %w", err)
	}
	endpoint := strings.TrimRight(apiBase, "/") + "/models/" + url.PathEscape(model) + ":generateContent"
	respBody, err := t.postJSON(ctx, client, endpoint, "", apiKey, body)
	if err != nil {
		return nil, "", err
	}
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					InlineData struct {
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, "", fmt.Errorf("failed to decode image model response: %w", err)
	}
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData.Data == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			return data, part.InlineData.MIMEType, err
		}
	}
	return nil, "", fmt.Errorf("image model returned no image data; verify the selected model supports image output")
}

func (t *ImageGenerateTool) postJSON(
	ctx context.Context,
	client *http.Client,
	endpoint, bearerKey, googleAPIKey string,
	body []byte,
) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create image request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearerKey != "" {
		req.Header.Set("Authorization", "Bearer "+bearerKey)
	}
	if googleAPIKey != "" {
		req.Header.Set("X-Goog-Api-Key", googleAPIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image generation request failed: %w", err)
	}
	defer resp.Body.Close()
	responseLimit := int64(32 << 20)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		responseLimit = max(responseLimit, int64(base64.StdEncoding.EncodedLen(t.maxFileSize))+(1<<20))
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read image model response: %w", err)
	}
	if int64(len(responseBody)) > responseLimit {
		return nil, fmt.Errorf("image model response exceeds the configured media size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"image model returned HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(responseBody)),
		)
	}
	return responseBody, nil
}

func (t *ImageGenerateTool) downloadImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || !isPublicImageHost(parsed.Hostname()) {
		return nil, "", fmt.Errorf("image model returned an invalid image URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create image download request: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialPublicImageHost
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Timeout:   60 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 || req.URL.Scheme != "https" || !isPublicImageHost(req.URL.Hostname()) {
				return fmt.Errorf("image download redirected to an invalid host")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("image download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("image download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(t.maxFileSize)+1))
	if err != nil {
		return nil, "", fmt.Errorf("failed to read generated image: %w", err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func validateGeneratedImage(data []byte, maxSize int) (string, string, error) {
	if len(data) == 0 || len(data) > maxSize {
		return "", "", fmt.Errorf("generated image is empty or exceeds the configured media size limit")
	}
	prefixLen := len(data)
	if prefixLen > 512 {
		prefixLen = 512
	}
	contentType := http.DetectContentType(data[:prefixLen])
	switch contentType {
	case "image/png":
		return contentType, ".png", nil
	case "image/jpeg":
		return contentType, ".jpg", nil
	case "image/webp":
		return contentType, ".webp", nil
	case "image/gif":
		return contentType, ".gif", nil
	default:
		return "", "", fmt.Errorf(
			"image model returned an unsupported image format (%s)",
			strings.TrimSpace(contentType),
		)
	}
}

func isPublicImageHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPublicImageIP(ip)
	}
	return true
}

func isPublicImageIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate()
}

func dialPublicImageHost(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || !isPublicImageHost(host) {
		return nil, fmt.Errorf("image download target is not public")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image download host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("image download host has no IP address")
	}
	for _, address := range addresses {
		if !isPublicImageIP(address.IP) {
			return nil, fmt.Errorf("image download target is not public")
		}
	}
	dialer := &net.Dialer{}
	var dialErr error
	for _, address := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, dialErr
}

func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}
