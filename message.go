package main

import (
	"fmt"
	"strings"
	"unicode"

	plugin "github.com/gotify/plugin-api"
	"golang.org/x/net/html"
)

// Email matches cloud-mail's webhook-service.js; unknown fields are allowed.
type Email struct {
	EmailID    int64  `json:"emailId"`
	SendEmail  string `json:"sendEmail"`
	SendName   string `json:"sendName"`
	ToEmail    string `json:"toEmail"`
	ToName     string `json:"toName"`
	Subject    string `json:"subject"`
	Text       string `json:"text"`
	Content    string `json:"content"`
	Code       string `json:"code"`
	CreateTime string `json:"createTime"`
}

func formatMessage(mail Email, cfg Config) plugin.Message {
	title := oneLine(mail.Subject)
	if title == "" {
		title = "新邮件"
	}
	title = truncate(title, 200)
	var lines []string
	if from := address(mail.SendName, mail.SendEmail); from != "" {
		lines = append(lines, "发件人："+from)
	}
	if to := address(mail.ToName, mail.ToEmail); to != "" {
		lines = append(lines, "收件人："+to)
	}
	if code := oneLine(mail.Code); code != "" {
		lines = append(lines, "验证码："+code)
	}
	if at := oneLine(mail.CreateTime); at != "" {
		lines = append(lines, "时间："+at)
	}
	body := strings.TrimSpace(mail.Text)
	if body == "" {
		body = htmlText(mail.Content)
	}
	if body == "" {
		body = "（无正文）"
	}
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n\n" + body
	}
	return plugin.Message{Title: title, Message: truncate(body, cfg.MaxBodyChars), Priority: cfg.Priority}
}

func address(name, email string) string {
	name, email = oneLine(name), oneLine(email)
	if name == "" || name == email {
		return email
	}
	if email == "" {
		return name
	}
	return fmt.Sprintf("%s <%s>", name, email)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	const suffix = "…（已截断）"
	return string(runes[:max-len([]rune(suffix))]) + suffix
}

// htmlText converts email HTML to plain text without executing or fetching anything.
// Tracking pixels, scripts, CSS, and document metadata are intentionally omitted.
func htmlText(source string) string {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	var out strings.Builder
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, pre bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "head", "script", "style", "noscript", "template", "svg":
				return
			}
			for _, a := range n.Attr {
				if a.Key == "hidden" {
					return
				}
			}
			if n.Data == "br" {
				out.WriteByte('\n')
				return
			}
			if blockElement(n.Data) {
				out.WriteByte('\n')
			}
			if n.Data == "li" {
				out.WriteString("• ")
			}
			pre = pre || n.Data == "pre"
		}
		if n.Type == html.TextNode {
			if pre {
				out.WriteString(n.Data)
			} else {
				// Collapse HTML whitespace but preserve spacing between inline nodes.
				space := false
				for _, r := range n.Data {
					if unicode.IsSpace(r) {
						if !space {
							out.WriteByte(' ')
						}
						space = true
					} else {
						out.WriteRune(r)
						space = false
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, pre)
		}
		if n.Type == html.ElementNode {
			if n.Data == "a" {
				for _, a := range n.Attr {
					href := strings.TrimSpace(a.Val)
					if a.Key == "href" && (strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "mailto:")) {
						out.WriteString(" (" + href + ")")
					}
				}
			}
			if n.Data == "td" || n.Data == "th" {
				out.WriteByte(' ')
			}
			if blockElement(n.Data) {
				out.WriteByte('\n')
			}
		}
	}
	walk(root, false)
	var lines []string
	blank := false
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(lines) > 0 && !blank {
				lines = append(lines, "")
			}
			blank = true
		} else {
			lines = append(lines, line)
			blank = false
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func blockElement(tag string) bool {
	switch tag {
	case "p", "div", "section", "article", "header", "footer", "blockquote", "pre", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "table", "tr", "hr":
		return true
	}
	return false
}
