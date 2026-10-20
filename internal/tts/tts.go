// Package tts 封装语音合成服务的调用与本地缓存。
//
// 默认接入小米 MiMo 的 OpenAI 兼容语音合成接口（/v1/chat/completions），
// 也支持标准的 OpenAI /v1/audio/speech 接口。合成结果按「服务商 + 接口 +
// 模型 + 音色 + 文本」的哈希缓存在本地，重复朗读同一句不会重复计费。
package tts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// ProviderMiMo 是小米 MiMo 的 chat/completions 语音合成。
	ProviderMiMo = "mimo"
	// ProviderOpenAI 是标准的 /v1/audio/speech 接口。
	ProviderOpenAI = "openai"

	// DefaultBaseURL 是小米 MiMo 的默认语音合成地址。
	DefaultBaseURL = "https://api.xiaomimimo.com/v1/chat/completions"
	// DefaultOpenAIURL 是 OpenAI 兼容语音接口的默认地址。
	DefaultOpenAIURL = "https://api.openai.com/v1/audio/speech"
	// DefaultModel 是小米 MiMo 的默认语音合成模型。
	DefaultModel = "mimo-v2.5-tts"
	// DefaultVoice 是默认音色。
	DefaultVoice = "mimo_default"

	maxAudioBytes = 24 << 20
	maxChunkRunes = 1200
)

// Voice 是一个可选音色。
type Voice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MiMoVoices 是小米 MiMo 官方预置音色。
var MiMoVoices = []Voice{
	{ID: "mimo_default", Name: "默认"},
	{ID: "冰糖", Name: "冰糖"},
	{ID: "茉莉", Name: "茉莉"},
	{ID: "苏打", Name: "苏打"},
	{ID: "白桦", Name: "白桦"},
	{ID: "Mia", Name: "Mia"},
	{ID: "Chloe", Name: "Chloe"},
	{ID: "Milo", Name: "Milo"},
	{ID: "Dean", Name: "Dean"},
}

// Voices 返回某个服务商可选的音色列表。
func Voices(provider string) []Voice {
	if provider == ProviderOpenAI {
		return []Voice{
			{ID: "alloy", Name: "alloy"},
			{ID: "echo", Name: "echo"},
			{ID: "fable", Name: "fable"},
			{ID: "onyx", Name: "onyx"},
			{ID: "nova", Name: "nova"},
			{ID: "shimmer", Name: "shimmer"},
		}
	}
	return MiMoVoices
}

// ValidVoice 校验音色：MiMo 只允许官方预置音色，其它服务商允许常见的
// 字母、数字、下划线、连字符与中文名。
func ValidVoice(provider, voice string) bool {
	voice = strings.TrimSpace(voice)
	if voice == "" || utf8.RuneCountInString(voice) > 64 {
		return false
	}
	if provider == ProviderOpenAI {
		for _, r := range voice {
			if r == '-' || r == '_' || r == '.' {
				continue
			}
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				continue
			}
			if r > 127 {
				continue
			}
			return false
		}
		return true
	}
	for _, v := range MiMoVoices {
		if v.ID == voice {
			return true
		}
	}
	return false
}

// Config 是一次合成所需的服务配置。
type Config struct {
	Provider string
	BaseURL  string
	Model    string
	APIKey   string
	Voice    string
}

// Normalize 补全空字段的默认值。
func (c Config) Normalize() Config {
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	if c.Provider != ProviderOpenAI {
		c.Provider = ProviderMiMo
	}
	c.BaseURL = strings.TrimSpace(c.BaseURL)
	if c.BaseURL == "" {
		if c.Provider == ProviderOpenAI {
			c.BaseURL = DefaultOpenAIURL
		} else {
			c.BaseURL = DefaultBaseURL
		}
	}
	c.Model = strings.TrimSpace(c.Model)
	if c.Model == "" {
		c.Model = DefaultModel
	}
	c.Voice = strings.TrimSpace(c.Voice)
	if c.Voice == "" {
		c.Voice = DefaultVoice
	}
	c.APIKey = strings.TrimSpace(c.APIKey)
	return c
}

// ValidateURL 检查管理员填写的接口地址。
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("接口地址不是有效的 URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("接口地址必须以 http:// 或 https:// 开头")
	}
	return nil
}

// Client 调用语音合成服务，并在本地缓存合成结果。
type Client struct {
	HTTP     *http.Client
	CacheDir string

	mu     sync.Mutex
	writes int
}

// NewClient 创建客户端。cacheDir 为空表示不缓存。
func NewClient(cacheDir string) *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 90 * time.Second},
		CacheDir: cacheDir,
	}
}

// Synthesize 合成一段文字，返回 MP3 字节、MIME 与是否命中缓存。
func (c *Client) Synthesize(ctx context.Context, cfg Config, text, voice string) ([]byte, string, bool, error) {
	cfg = cfg.Normalize()
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, "", false, errors.New("朗读文本为空")
	}
	if utf8.RuneCountInString(text) > maxChunkRunes {
		text = string([]rune(text)[:maxChunkRunes])
	}
	if cfg.APIKey == "" {
		return nil, "", false, errors.New("未配置 API Key")
	}
	voice = strings.TrimSpace(voice)
	if voice == "" {
		voice = cfg.Voice
	}

	key := cacheKey(cfg, text, voice)
	if data, ok := c.readCache(key); ok {
		return data, "audio/mpeg", true, nil
	}

	data, mime, err := c.fetch(ctx, cfg, text, voice)
	if err != nil {
		return nil, "", false, err
	}
	c.writeCache(key, data)
	return data, mime, false, nil
}

func cacheKey(cfg Config, text, voice string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		cfg.Provider, cfg.BaseURL, cfg.Model, voice, text,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (c *Client) cachePath(key string) string {
	if c.CacheDir == "" {
		return ""
	}
	return filepath.Join(c.CacheDir, key+".mp3")
}

func (c *Client) readCache(key string) ([]byte, bool) {
	p := c.cachePath(key)
	if p == "" {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	_ = os.Chtimes(p, time.Now(), time.Now())
	return data, true
}

func (c *Client) writeCache(key string, data []byte) {
	p := c.cachePath(key)
	if p == "" || len(data) == 0 {
		return
	}
	if err := os.MkdirAll(c.CacheDir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(c.CacheDir, ".tts-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return
	}
	if err := os.Rename(name, p); err != nil {
		os.Remove(name)
		return
	}

	c.mu.Lock()
	c.writes++
	prune := c.writes%24 == 0
	c.mu.Unlock()
	if prune {
		c.prune(600)
	}
}

// prune 在缓存文件过多时按最久未使用删除一部分，避免无限增长。
func (c *Client) prune(limit int) {
	entries, err := os.ReadDir(c.CacheDir)
	if err != nil || len(entries) <= limit {
		return
	}
	type fileInfo struct {
		name    string
		modTime time.Time
	}
	files := make([]fileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mp3") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{name: e.Name(), modTime: info.ModTime()})
	}
	if len(files) <= limit {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	remove := len(files) - limit + limit/4
	for i := 0; i < remove && i < len(files); i++ {
		_ = os.Remove(filepath.Join(c.CacheDir, files[i].name))
	}
}

func (c *Client) fetch(ctx context.Context, cfg Config, text, voice string) ([]byte, string, error) {
	var body map[string]any
	if cfg.Provider == ProviderOpenAI {
		body = map[string]any{
			"model":           cfg.Model,
			"input":           text,
			"voice":           voice,
			"response_format": "mp3",
			"speed":           1,
		}
	} else {
		body = map[string]any{
			"model": cfg.Model,
			"messages": []map[string]string{
				{"role": "assistant", "content": text},
			},
			"audio": map[string]any{
				"format": "mp3",
				"voice":  voice,
			},
			"stream": false,
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL, bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	if cfg.Provider == ProviderMiMo {
		req.Header.Set("api-key", cfg.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("无法连接语音服务：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := readAPIError(resp.Body)
		if msg == "" {
			msg = resp.Status
		}
		return nil, "", fmt.Errorf("语音服务返回 %d：%s", resp.StatusCode, msg)
	}

	contentType := resp.Header.Get("Content-Type")
	if cfg.Provider == ProviderOpenAI && strings.HasPrefix(contentType, "audio/") {
		data, err := readLimited(resp.Body)
		if err != nil {
			return nil, "", err
		}
		if len(data) == 0 {
			return nil, "", errors.New("语音服务返回了空音频")
		}
		return data, "audio/mpeg", nil
	}

	var out struct {
		Choices []struct {
			Message struct {
				Audio *struct {
					Data string `json:"data"`
				} `json:"audio"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Data string `json:"data"`
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxAudioBytes))
	if err := dec.Decode(&out); err != nil {
		return nil, "", fmt.Errorf("无法解析语音服务响应：%w", err)
	}

	encoded := out.Data
	if encoded == "" && len(out.Choices) > 0 && out.Choices[0].Message.Audio != nil {
		encoded = out.Choices[0].Message.Audio.Data
	}
	if encoded == "" {
		return nil, "", errors.New("语音服务没有返回音频数据")
	}
	data, err := decodeBase64(encoded)
	if err != nil {
		return nil, "", fmt.Errorf("音频数据解码失败：%w", err)
	}
	if len(data) == 0 {
		return nil, "", errors.New("语音服务返回了空音频")
	}
	return data, "audio/mpeg", nil
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ','); i >= 0 && strings.Contains(s[:i], "base64") {
		s = s[i+1:]
	}
	if data, err := base64.StdEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

func readLimited(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(r, maxAudioBytes)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readAPIError(r io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(r, 8192))
	if len(raw) == 0 {
		return ""
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &out); err == nil {
		if out.Error.Message != "" {
			return truncate(out.Error.Message, 240)
		}
		if out.Message != "" {
			return truncate(out.Message, 240)
		}
	}
	return truncate(strings.Join(strings.Fields(string(raw)), " "), 240)
}

func truncate(s string, limit int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}
