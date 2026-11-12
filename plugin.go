package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	plugin "github.com/gotify/plugin-api"
)

const version = "0.1.0"

// Config is edited in Gotify's plugin page. No cloud-mail changes are needed.
type Config struct {
	Secret       string `json:"secret" yaml:"secret"`
	Priority     int    `json:"priority" yaml:"priority"`
	MaxBodyChars int    `json:"max_body_chars" yaml:"max_body_chars"`
}

type Plugin struct {
	mu       sync.RWMutex
	config   Config
	enabled  bool
	basePath string
	handler  plugin.MessageHandler
	// Serializes delivery and duplicate detection, including concurrent retries.
	deliveryMu sync.Mutex
	seen       map[int64]time.Time
}

var (
	_ plugin.Plugin     = (*Plugin)(nil)
	_ plugin.Configurer = (*Plugin)(nil)
	_ plugin.Messenger  = (*Plugin)(nil)
	_ plugin.Webhooker  = (*Plugin)(nil)
	_ plugin.Displayer  = (*Plugin)(nil)
)

func GetGotifyPluginInfo() plugin.Info {
	return plugin.Info{
		ModulePath:  "gotify-cloud-mail-webhook-plugin",
		Version:     version,
		Name:        "Cloud Mail Webhook",
		Description: "接收 cloud-mail 邮件通知，显示发件人、收件人、验证码及正文，支持 HTML 转文本。",
	}
}

func NewGotifyPluginInstance(_ plugin.UserContext) plugin.Plugin {
	return &Plugin{seen: make(map[int64]time.Time)}
}

func (p *Plugin) DefaultConfig() interface{} {
	// Gotify persists this configuration. Each user gets a different random secret.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// Fail closed: an empty secret cannot enable the plugin.
		return &Config{Priority: 5, MaxBodyChars: 6000}
	}
	return &Config{Secret: hex.EncodeToString(secret), Priority: 5, MaxBodyChars: 6000}
}

func (p *Plugin) ValidateAndSetConfig(value interface{}) error {
	cfg, ok := value.(*Config)
	if !ok || cfg == nil {
		return errors.New("invalid configuration")
	}
	copy := *cfg
	copy.Secret = strings.TrimSpace(copy.Secret)
	if len(copy.Secret) < 16 || len(copy.Secret) > 256 || strings.ContainsAny(copy.Secret, "\r\n") {
		return errors.New("secret 必须为 16–256 字节，且不能包含换行")
	}
	for _, r := range copy.Secret {
		if r < 33 || r > 126 {
			return errors.New("secret 请使用不含空格的可见 ASCII 字符，例如随机十六进制字符串")
		}
	}
	if copy.Priority < 0 || copy.Priority > 10 {
		return errors.New("priority 必须为 0–10")
	}
	if copy.MaxBodyChars < 100 || copy.MaxBodyChars > 20000 {
		return errors.New("max_body_chars 必须为 100–20000")
	}
	p.mu.Lock()
	p.config = copy
	p.mu.Unlock()
	return nil
}

func (p *Plugin) SetMessageHandler(h plugin.MessageHandler) {
	p.mu.Lock()
	p.handler = h
	p.mu.Unlock()
}

func (p *Plugin) Enable() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.config.Secret == "" || p.handler == nil {
		return errors.New("请先配置密钥，并确认 Gotify 消息处理器已初始化")
	}
	p.enabled = true
	return nil
}

func (p *Plugin) Disable() error {
	p.mu.Lock()
	p.enabled = false
	p.mu.Unlock()
	return nil
}

func (p *Plugin) RegisterWebhook(basePath string, router *gin.RouterGroup) {
	p.mu.Lock()
	p.basePath = strings.TrimRight(basePath, "/")
	p.mu.Unlock()
	router.POST("/receive", p.receive)
}

func (p *Plugin) GetDisplay(location *url.URL) string {
	p.mu.RLock()
	path := p.basePath + "/receive"
	p.mu.RUnlock()
	endpoint := &url.URL{Path: path}
	if location != nil {
		endpoint.Scheme, endpoint.Host = location.Scheme, location.Host
	}
	return fmt.Sprintf("## cloud-mail 接入\n\n在 cloud-mail 系统设置中启用 Webhook：\n\n- 请求地址：`%s`\n- 密钥：填写本插件配置里的 `secret`，原样填写，无须 Bearer 前缀。\n- 重试次数：建议 2。\n\n保存配置并启用插件，然后发送一封测试邮件。\n\n优先显示纯文本正文；仅有 HTML 时转换为文本，不保留原始排版和图片。\n插件密钥不要放进 URL。若 Gotify 域名受 Cloudflare Access 邮箱验证码保护，该接口需要允许机器访问。", endpoint.String())
}

func (p *Plugin) receive(c *gin.Context) {
	p.mu.RLock()
	cfg, enabled, handler := p.config, p.enabled, p.handler
	p.mu.RUnlock()
	if !enabled || handler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin is disabled"})
		return
	}
	provided := c.GetHeader("Authorization")
	if cfg.Secret == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.Secret)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook secret"})
		return
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "expected application/json"})
		return
	}
	// Bound memory use before decoding untrusted HTML/JSON.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	decoder := json.NewDecoder(c.Request.Body)
	var mail Email
	if err := decoder.Decode(&mail); err != nil {
		p.decodeError(c, err)
		return
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		p.decodeError(c, err)
		return
	}
	if strings.TrimSpace(mail.Subject+mail.SendEmail+mail.Text+mail.Content) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty email payload"})
		return
	}
	msg := formatMessage(mail, cfg)
	p.deliveryMu.Lock()
	defer p.deliveryMu.Unlock()
	now := time.Now()
	for id, sent := range p.seen {
		if now.Sub(sent) >= 24*time.Hour {
			delete(p.seen, id)
		}
	}
	if mail.EmailID > 0 {
		if _, found := p.seen[mail.EmailID]; found {
			c.JSON(http.StatusOK, gin.H{"status": "duplicate"})
			return
		}
	}
	if err := handler.SendMessage(msg); err != nil {
		// Do not expose private mail contents or internal errors in the response.
		c.JSON(http.StatusBadGateway, gin.H{"error": "notification delivery failed"})
		return
	}
	if mail.EmailID > 0 {
		if p.seen == nil {
			p.seen = make(map[int64]time.Time)
		}
		if len(p.seen) >= 1024 {
			var oldestID int64
			oldest := now
			for id, sent := range p.seen {
				if sent.Before(oldest) {
					oldestID, oldest = id, sent
				}
			}
			delete(p.seen, oldestID)
		}
		p.seen[mail.EmailID] = now
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (p *Plugin) decodeError(c *gin.Context, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "payload exceeds 8 MiB"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email JSON"})
}

func main() {}
