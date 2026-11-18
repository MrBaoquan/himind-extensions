// broker.go —— 通过 Agent 注入的能力代理调用内核媒体能力。
//
// 媒体创作工具自己不保管任何 Provider 密钥，也不直接连模型服务：生图、改图
// 统一走 `media.image.generate` / `media.image.edit`，由 Agent 按用户配置的
// 服务、适配器与路由执行。插件只在 manifest 的 capability_dependencies 里声明
// 它需要哪些能力，代理会按这份声明校验每一次调用。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	brokerURLEnv   = "HIMIND_CAPABILITY_BROKER_URL"
	brokerTokenEnv = "HIMIND_CAPABILITY_BROKER_TOKEN"

	capImageGenerate   = "media.image.generate"
	capImageEdit       = "media.image.edit"
	capProviderCatalog = "media.provider.catalog"

	maxBrokerResponseBytes = 32 << 20
)

// CapabilityInvoker 抽象「调用一个内核能力」。生产环境是 broker，
// 单测里是内存替身，两者共用同一套编排逻辑。
type CapabilityInvoker interface {
	Invoke(capability string, input map[string]any) (map[string]any, error)
}

type broker struct {
	url    string
	token  string
	client *http.Client
}

// brokerFromEnv 读取 Agent 为本进程注入的代理地址与一次性 token。
func brokerFromEnv() (*broker, error) {
	url := strings.TrimSpace(os.Getenv(brokerURLEnv))
	token := strings.TrimSpace(os.Getenv(brokerTokenEnv))
	if url == "" || token == "" {
		return nil, fmt.Errorf("当前会话没有能力代理：媒体创作工具需要由 HiMind Agent 启动才能调用本机生图与改图能力")
	}
	return &broker{url: url, token: token, client: &http.Client{Timeout: 20 * time.Minute}}, nil
}

func (b *broker) Invoke(capability string, input map[string]any) (map[string]any, error) {
	if input == nil {
		input = map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{"capability": capability, "input": input})
	if err != nil {
		return nil, fmt.Errorf("组装能力请求失败: %w", err)
	}
	request, err := http.NewRequest(http.MethodPost, b.url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建能力请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+b.token)
	response, err := b.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("调用能力 %s 失败: %w", capability, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBrokerResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("读取能力响应失败: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &failure) == nil {
			message := strings.TrimSpace(failure.Message)
			if message == "" {
				message = failure.Error
			}
			if message != "" {
				return nil, fmt.Errorf("%s", message)
			}
		}
		return nil, fmt.Errorf("能力 %s 返回 HTTP %d", capability, response.StatusCode)
	}
	var envelope struct {
		Capability string         `json:"capability"`
		Result     map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解析能力响应失败: %w", err)
	}
	return envelope.Result, nil
}

// generatedFile 是内核生图/改图返回的单个产物。
type generatedFile struct {
	MediaID   string `json:"media_id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	MimeType  string `json:"mime_type"`
	SHA256    string `json:"sha256"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SizeBytes int    `json:"size_bytes"`
}

// filesFromResult 从内核返回体里取产物列表。
//
// 内核在 inline=false 时把 structured 直接作为结果返回（files 在顶层）；
// 若调用方误传 inline=true，产物会包在 structuredContent 里。两种都要能读，
// 否则一次参数写错就变成“生成成功但插件说没图”。
func filesFromResult(result map[string]any) []generatedFile {
	candidates := []map[string]any{result}
	for _, key := range []string{"structuredContent", "structured_content", "structured"} {
		if nested, ok := result[key].(map[string]any); ok {
			candidates = append(candidates, nested)
		}
	}
	for _, candidate := range candidates {
		raw, ok := candidate["files"]
		if !ok {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			continue
		}
		files := make([]generatedFile, 0, len(list))
		for _, item := range list {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			file := generatedFile{
				MediaID:   stringValue(entry["media_id"]),
				Path:      stringValue(entry["path"]),
				Name:      stringValue(entry["name"]),
				MimeType:  stringValue(entry["mime_type"]),
				SHA256:    stringValue(entry["sha256"]),
				Width:     intValue(entry["width"]),
				Height:    intValue(entry["height"]),
				SizeBytes: intValue(entry["size_bytes"]),
			}
			if file.Path == "" {
				continue
			}
			files = append(files, file)
		}
		if len(files) > 0 {
			return files
		}
	}
	return nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return 0
}
