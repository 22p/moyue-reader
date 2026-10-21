package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"moyue/internal/epub"
	"moyue/internal/store"
	"moyue/internal/tts"
)

const ttsSettingKey = "tts"

const (
	ttsMaxRunes     = 1000
	ttsTestSentence = "你好，这是墨阅的语音朗读测试。海风吹过书页，故事从这里开始。"
	ttsRateLimit    = 20
	ttsRateWindow   = time.Minute
)

// ttsSettings 是保存在数据库里的语音朗读配置。API Key 只存在服务端，
// 任何面向读者的接口都不会返回它。
type ttsSettings struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	Voice    string `json:"voice"`
}

func defaultTTSSettings() ttsSettings {
	cfg := tts.Config{}.Normalize()
	return ttsSettings{
		Enabled:  false,
		Provider: cfg.Provider,
		BaseURL:  cfg.BaseURL,
		Model:    cfg.Model,
		Voice:    cfg.Voice,
	}
}

// loadTTSSettings 读取配置；旧版本或首次运行时返回默认值。
func (s *Server) loadTTSSettings() ttsSettings {
	cfg := defaultTTSSettings()
	raw, err := s.store.GetSetting(ttsSettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Warn("语音朗读配置损坏，使用默认值", "err", err)
		return defaultTTSSettings()
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfg.Provider != tts.ProviderOpenAI {
		cfg.Provider = tts.ProviderMiMo
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = tts.Config{Provider: cfg.Provider}.Normalize().BaseURL
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = tts.DefaultModel
	}
	if strings.TrimSpace(cfg.Voice) == "" {
		cfg.Voice = tts.DefaultVoice
	}
	return cfg
}

func (s *Server) saveTTSSettings(cfg ttsSettings) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ttsSettingKey, string(raw))
}

func (cfg ttsSettings) config() tts.Config {
	return tts.Config{
		Provider: cfg.Provider,
		BaseURL:  cfg.BaseURL,
		Model:    cfg.Model,
		APIKey:   cfg.APIKey,
		Voice:    cfg.Voice,
	}.Normalize()
}

type ttsAdminView struct {
	Enabled  bool        `json:"enabled"`
	Provider string      `json:"provider"`
	BaseURL  string      `json:"baseUrl"`
	Model    string      `json:"model"`
	Voice    string      `json:"voice"`
	HasKey   bool        `json:"hasKey"`
	KeyHint  string      `json:"keyHint"`
	Voices   []tts.Voice `json:"voices"`
}

func ttsView(cfg ttsSettings) ttsAdminView {
	view := ttsAdminView{
		Enabled:  cfg.Enabled,
		Provider: cfg.Provider,
		BaseURL:  cfg.BaseURL,
		Model:    cfg.Model,
		Voice:    cfg.Voice,
		HasKey:   cfg.APIKey != "",
		Voices:   tts.Voices(cfg.Provider),
	}
	if n := len(cfg.APIKey); n > 4 {
		view.KeyHint = "…" + cfg.APIKey[n-4:]
	} else if n > 0 {
		view.KeyHint = "已配置"
	}
	return view
}

// adminGetTTS 返回语音朗读配置（隐藏密钥原文）。
func (s *Server) adminGetTTS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tts": ttsView(s.loadTTSSettings())})
}

type ttsSettingsRequest struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	ClearKey bool   `json:"clearKey"`
	Voice    string `json:"voice"`
}

// adminPutTTS 保存语音朗读配置。apiKey 留空表示保持原密钥不变。
func (s *Server) adminPutTTS(w http.ResponseWriter, r *http.Request) {
	var req ttsSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	cur := s.loadTTSSettings()
	cfg := cur

	cfg.Enabled = req.Enabled
	cfg.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	if cfg.Provider != tts.ProviderOpenAI {
		cfg.Provider = tts.ProviderMiMo
	}
	cfg.BaseURL = strings.TrimSpace(req.BaseURL)
	if cfg.BaseURL == "" {
		cfg.BaseURL = tts.Config{Provider: cfg.Provider}.Normalize().BaseURL
	}
	if err := tts.ValidateURL(cfg.BaseURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.Model = strings.TrimSpace(req.Model)
	if cfg.Model == "" {
		cfg.Model = tts.DefaultModel
	}
	if utf8.RuneCountInString(cfg.Model) > 120 {
		writeError(w, http.StatusBadRequest, "模型名称过长")
		return
	}
	cfg.Voice = strings.TrimSpace(req.Voice)
	if cfg.Voice == "" {
		cfg.Voice = tts.DefaultVoice
	}
	if !tts.ValidVoice(cfg.Provider, cfg.Voice) {
		writeError(w, http.StatusBadRequest, "音色不受支持")
		return
	}
	if req.ClearKey {
		cfg.APIKey = ""
	} else if key := strings.TrimSpace(req.APIKey); key != "" {
		if len(key) > 512 {
			writeError(w, http.StatusBadRequest, "API Key 过长")
			return
		}
		cfg.APIKey = key
	}
	if cfg.Enabled && cfg.APIKey == "" {
		writeError(w, http.StatusBadRequest, "启用语音朗读前请填写 API Key")
		return
	}
	if err := s.saveTTSSettings(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "保存设置失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tts": ttsView(cfg)})
}

// adminTestTTS 用当前表单（或已保存）的配置合成一句示例，直接返回音频。
func (s *Server) adminTestTTS(w http.ResponseWriter, r *http.Request) {
	var req ttsSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	cfg := s.loadTTSSettings()
	cfg.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	if cfg.Provider != tts.ProviderOpenAI {
		cfg.Provider = tts.ProviderMiMo
	}
	if v := strings.TrimSpace(req.BaseURL); v != "" {
		cfg.BaseURL = v
	}
	if v := strings.TrimSpace(req.Model); v != "" {
		cfg.Model = v
	}
	if v := strings.TrimSpace(req.Voice); v != "" {
		cfg.Voice = v
	}
	if key := strings.TrimSpace(req.APIKey); key != "" {
		cfg.APIKey = key
	}
	if req.ClearKey {
		cfg.APIKey = ""
	}
	if cfg.APIKey == "" {
		writeError(w, http.StatusBadRequest, "请先填写 API Key")
		return
	}
	if err := tts.ValidateURL(cfg.BaseURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !tts.ValidVoice(cfg.Provider, cfg.Voice) {
		writeError(w, http.StatusBadRequest, "音色不受支持")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	data, mime, _, err := s.ttsClient.Synthesize(ctx, cfg.config(), ttsTestSentence, cfg.Voice)
	if err != nil {
		slog.Warn("语音朗读测试失败", "err", err)
		writeError(w, http.StatusBadGateway, "语音合成失败："+err.Error())
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// ttsStatus 面向所有登录用户，只暴露是否启用与可选音色。
func (s *Server) ttsStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadTTSSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":      cfg.Enabled && cfg.APIKey != "",
		"provider":     cfg.Provider,
		"model":        cfg.Model,
		"defaultVoice": cfg.Voice,
		"voices":       tts.Voices(cfg.Provider),
	})
}

func ttsSpeakable(bookType string) bool {
	switch bookType {
	case "txt", "epub", "fb2", "mobi", "azw", "azw3", "prc":
		return true
	default:
		return false
	}
}

// ttsChapterText 提取当前章节的纯文本，用于限制读者只能朗读书籍正文。
func (s *Server) ttsChapterText(b *store.Book, chapter int) (string, error) {
	if chapter < 0 {
		return "", fmt.Errorf("章节不存在")
	}
	switch b.Type {
	case "txt":
		tb, err := s.parseTXT(b)
		if err != nil || chapter >= len(tb.Chapters) {
			return "", fmt.Errorf("章节不存在")
		}
		ch := tb.Chapters[chapter]
		if ch.Start < 0 || ch.End > len(tb.Text) || ch.Start >= ch.End {
			return "", fmt.Errorf("章节不存在")
		}
		return ch.Title + "\n" + tb.Text[ch.Start:ch.End], nil
	case "fb2":
		fb, err := s.parseFB2(b)
		if err != nil || chapter >= len(fb.Chapters) {
			return "", fmt.Errorf("章节不存在")
		}
		return htmlToText(fb.Chapters[chapter].HTML), nil
	case "mobi", "azw", "azw3", "prc":
		_, chapters, err := s.parseKindle(b)
		if err != nil || chapter >= len(chapters) {
			return "", fmt.Errorf("章节不存在")
		}
		return htmlToText(chapters[chapter].HTML), nil
	case "epub":
		eb, err := s.parseEPUB(b)
		if err != nil || chapter >= len(eb.Chapters) {
			return "", fmt.Errorf("章节不存在")
		}
		ar, err := epub.OpenArchive(s.bookPath(b))
		if err != nil {
			return "", err
		}
		defer ar.Close()
		raw, err := ar.Read(eb.Chapters[chapter].Href)
		if err != nil {
			return "", err
		}
		return htmlToText(string(raw)), nil
	default:
		return "", fmt.Errorf("不支持的格式")
	}
}

func normalizeTTS(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) || r == '\u200b' || r == '\ufeff' {
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// allowTTS 按用户做简单滑动窗口限流，避免把服务端当成通用语音接口。
func (s *Server) allowTTS(userID string) bool {
	s.ttsMu.Lock()
	defer s.ttsMu.Unlock()
	now := time.Now()
	bucket, ok := s.ttsUses[userID]
	if !ok || now.Sub(bucket.first) > ttsRateWindow {
		s.ttsUses[userID] = &loginBucket{count: 1, first: now}
		return true
	}
	if bucket.count >= ttsRateLimit {
		return false
	}
	bucket.count++
	return true
}

// ttsSpeak 为当前章节的一句话合成语音，结果来自服务端代理与本地缓存。
func (s *Server) ttsSpeak(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if !ttsSpeakable(b.Type) {
		writeError(w, http.StatusBadRequest, "当前格式暂不支持语音朗读")
		return
	}
	var req struct {
		Chapter int    `json:"chapter"`
		Text    string `json:"text"`
		Voice   string `json:"voice"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "朗读文本为空")
		return
	}
	if utf8.RuneCountInString(text) > ttsMaxRunes {
		text = string([]rune(text)[:ttsMaxRunes])
	}
	// 限流放在解析章节之前，避免无效文本反复触发 EPUB 解包等重操作。
	if !s.allowTTS(u.ID) {
		writeError(w, http.StatusTooManyRequests, "朗读请求过于频繁，请稍后再试")
		return
	}
	chapterText, err := s.ttsChapterText(b, req.Chapter)
	if err != nil {
		writeError(w, http.StatusBadRequest, "章节不存在")
		return
	}
	if !strings.Contains(normalizeTTS(chapterText), normalizeTTS(text)) {
		writeError(w, http.StatusBadRequest, "朗读文本不属于当前章节")
		return
	}
	cfg := s.loadTTSSettings()
	if !cfg.Enabled || cfg.APIKey == "" {
		writeError(w, http.StatusServiceUnavailable, "管理员尚未配置语音朗读")
		return
	}
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		voice = cfg.Voice
	}
	if !tts.ValidVoice(cfg.Provider, voice) {
		voice = cfg.Voice
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	data, mime, cached, err := s.ttsClient.Synthesize(ctx, cfg.config(), text, voice)
	if err != nil {
		slog.Warn("语音合成失败", "book", b.ID, "err", err)
		writeError(w, http.StatusBadGateway, "语音合成失败，请稍后重试；管理员可在管理台测试语音服务")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if cached {
		w.Header().Set("X-Moyue-TTS-Cache", "hit")
	} else {
		w.Header().Set("X-Moyue-TTS-Cache", "miss")
	}
	_, _ = w.Write(data)
}
