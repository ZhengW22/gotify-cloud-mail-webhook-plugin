package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	plugin "github.com/gotify/plugin-api"
)

const testSecret = "test-secret-for-cloud-mail"

type recorder struct {
	mu       sync.Mutex
	messages []plugin.Message
	fail     bool
}

func (r *recorder) SendMessage(m plugin.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("private backend details")
	}
	r.messages = append(r.messages, m)
	return nil
}

func setup(t *testing.T) (*Plugin, *recorder, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	p := NewGotifyPluginInstance(plugin.UserContext{}).(*Plugin)
	if err := p.ValidateAndSetConfig(&Config{Secret: testSecret, Priority: 5, MaxBodyChars: 6000}); err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	p.SetMessageHandler(r)
	if err := p.Enable(); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	p.RegisterWebhook("/plugin/test", router.Group("/plugin/test"))
	return p, r, router
}

func request(router http.Handler, body, secret, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/plugin/test/receive", strings.NewReader(body))
	req.Header.Set("Authorization", secret)
	req.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestCloudMailPayload(t *testing.T) {
	_, r, router := setup(t)
	body := `{"emailId":42,"sendEmail":"sender@example.com","sendName":"发件人","toEmail":"me@example.com","toName":"收件人","subject":"登录验证","text":"正文验证码 123456","content":"<p>不应使用 HTML</p>","code":"123456","createTime":"2026-11-12 12:00:00","futureField":true}`
	w := request(router, body, testSecret, "application/json; charset=utf-8")
	if w.Code != 200 || len(r.messages) != 1 {
		t.Fatalf("status=%d, messages=%d: %s", w.Code, len(r.messages), w.Body)
	}
	m := r.messages[0]
	if m.Title != "登录验证" || m.Priority != 5 {
		t.Fatalf("unexpected message: %+v", m)
	}
	for _, s := range []string{"发件人 <sender@example.com>", "收件人 <me@example.com>", "验证码：123456", "时间：2026-11-12", "正文验证码 123456"} {
		if !strings.Contains(m.Message, s) {
			t.Errorf("missing %q: %s", s, m.Message)
		}
	}
	if strings.Contains(m.Message, "不应使用") {
		t.Fatal("HTML chosen over text")
	}
}

func TestReceiveRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, body, secret, contentType string
		status                          int
	}{
		{"missing auth", `{"subject":"mail"}`, "", "application/json", 401},
		{"wrong auth", `{"subject":"mail"}`, "wrong", "application/json", 401},
		{"query token is not accepted", `{"subject":"mail"}`, "", "application/json", 401},
		{"bad media type", `{}`, testSecret, "text/plain", 415},
		{"broken JSON", `{`, testSecret, "application/json", 400},
		{"array", `[]`, testSecret, "application/json", 400},
		{"null", `null`, testSecret, "application/json", 400},
		{"empty", `{}`, testSecret, "application/json", 400},
		{"two objects", `{"subject":"mail"}{}`, testSecret, "application/json", 400},
		{"trailing junk", `{"subject":"mail"}x`, testSecret, "application/json", 400},
		{"wrong field type", `{"subject":42}`, testSecret, "application/json", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, r, router := setup(t)
			w := request(router, tc.body, tc.secret, tc.contentType)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d: %s", w.Code, tc.status, w.Body)
			}
			if len(r.messages) != 0 {
				t.Fatal("invalid request delivered")
			}
		})
	}
}

func TestPayloadLimit(t *testing.T) {
	_, r, router := setup(t)
	body := `{"text":"` + strings.Repeat("a", 8<<20) + `"}`
	w := request(router, body, testSecret, "application/json")
	if w.Code != 413 || len(r.messages) != 0 {
		t.Fatalf("status=%d messages=%d", w.Code, len(r.messages))
	}
}

func TestRetryAfterDeliveryFailureAndDeduplication(t *testing.T) {
	p, r, router := setup(t)
	body := `{"emailId":7,"subject":"hello"}`
	r.fail = true
	if w := request(router, body, testSecret, "application/json"); w.Code != 502 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("unexpected error: %d %s", w.Code, w.Body)
	}
	r.fail = false
	for i := 0; i < 2; i++ {
		if w := request(router, body, testSecret, "application/json"); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if len(r.messages) != 1 {
		t.Fatalf("retry produced %d messages", len(r.messages))
	}
	_ = p.Disable()
	if w := request(router, `{"subject":"other"}`, testSecret, "application/json"); w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestConcurrentDuplicateRequests(t *testing.T) {
	_, r, router := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request(router, `{"emailId":99,"text":"hello"}`, testSecret, "application/json")
			if w.Code != 200 {
				t.Errorf("status=%d", w.Code)
			}
		}()
	}
	wg.Wait()
	if len(r.messages) != 1 {
		t.Fatalf("delivered %d duplicates", len(r.messages))
	}
}

func TestConfigAndDisplay(t *testing.T) {
	p, _, _ := setup(t)
	first, second := p.DefaultConfig().(*Config), p.DefaultConfig().(*Config)
	if len(first.Secret) != 64 || first.Secret == second.Secret {
		t.Fatal("default secrets are not unique")
	}
	for _, cfg := range []*Config{
		{Secret: "short", Priority: 5, MaxBodyChars: 100},
		{Secret: "中文密钥中文密钥中文密钥", Priority: 5, MaxBodyChars: 100},
		{Secret: testSecret, Priority: -1, MaxBodyChars: 100},
		{Secret: testSecret, Priority: 11, MaxBodyChars: 100},
		{Secret: testSecret, Priority: 5, MaxBodyChars: 99},
		{Secret: testSecret, Priority: 5, MaxBodyChars: 20001},
	} {
		if p.ValidateAndSetConfig(cfg) == nil {
			t.Fatalf("accepted invalid config %+v", cfg)
		}
	}
	loc, _ := url.Parse("https://notify.example.com/prefix")
	display := p.GetDisplay(loc)
	if !strings.Contains(display, "https://notify.example.com/plugin/test/receive") || strings.Contains(display, testSecret) {
		t.Fatal(display)
	}
	if err := p.ValidateAndSetConfig(&Config{Secret: "rotated-secret-for-mail", Priority: 0, MaxBodyChars: 100}); err != nil {
		t.Fatal(err)
	}
	if p.config.Priority != 0 {
		t.Fatal("zero priority lost")
	}
}

func TestHTMLOnlyAndTruncation(t *testing.T) {
	cfg := Config{Priority: 5, MaxBodyChars: 6000}
	m := formatMessage(Email{Subject: "HTML mail", Content: `<html><head><title>ignored</title><style>.x{color:red}</style></head><body><h1>你好 &amp; welcome</h1><p>验证码：<b>123456</b><br>第二行</p><a href="https://example.com/verify?a=1&amp;b=2">验证链接</a><script>alert('secret')</script><img src="https://tracking.example/pixel"><span hidden>hidden secret</span></body></html>`}, cfg)
	for _, s := range []string{"你好 & welcome", "验证码：123456", "第二行", "https://example.com/verify?a=1&b=2"} {
		if !strings.Contains(m.Message, s) {
			t.Errorf("missing %q: %s", s, m.Message)
		}
	}
	for _, s := range []string{"<html>", "alert(", "tracking.example", "hidden secret", "color:red", "ignored"} {
		if strings.Contains(m.Message, s) {
			t.Errorf("leaked %q", s)
		}
	}
	cfg.MaxBodyChars = 100
	m = formatMessage(Email{Text: strings.Repeat("汉字", 100)}, cfg)
	if len([]rune(m.Message)) != 100 || !strings.HasSuffix(m.Message, "…（已截断）") || !json.Valid([]byte(fmt.Sprintf("%q", m.Message))) {
		t.Fatal(m.Message)
	}
	m = formatMessage(Email{Content: "<p></p>"}, cfg)
	if m.Title != "新邮件" || m.Message != "（无正文）" {
		t.Fatalf("%+v", m)
	}
}
