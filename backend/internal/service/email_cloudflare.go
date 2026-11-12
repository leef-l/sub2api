package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"strings"
	"time"
)

// cloudflareEmailEndpoint 是 Cloudflare Email Service 的发信端点（HTTPS 443）。
// 为什么要它：Cloudflare 的 SMTP 端点只开 465 隐式 TLS，部分机房（如 Hetzner）封了
// 出站 465，SMTP 通道不可用；REST 走 443，不受影响。
var cloudflareEmailEndpointTemplate = "https://api.cloudflare.com/client/v4/accounts/%s/email/sending/send"

const cloudflareEmailTimeout = 20 * time.Second

type cloudflareEmailRequest struct {
	To      any    `json:"to"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	HTML    string `json:"html,omitempty"`
	Text    string `json:"text,omitempty"`
}

type cloudflareEmailResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result *struct {
		MessageID string   `json:"message_id"`
		Delivered []string `json:"delivered"`
		Queued    []string `json:"queued"`
	} `json:"result"`
}

// sendWithCloudflare 通过 Cloudflare Email Service REST API 发送邮件。
// body 既可能是 HTML 也可能是纯文本：同时作为 html 与 text 提交会被 CF 接受，
// 但纯文本内容套 HTML 会有可读性问题，因此简单判定是否含标签。
func (s *EmailService) sendWithCloudflare(config *SMTPConfig, to, subject, body string) error {
	if config == nil {
		return ErrEmailNotConfigured
	}
	accountID := strings.TrimSpace(config.CFAccountID)
	apiToken := strings.TrimSpace(config.CFAPIToken)
	from := strings.TrimSpace(config.From)
	if accountID == "" || apiToken == "" || from == "" {
		return ErrEmailNotConfigured
	}
	if strings.TrimSpace(to) == "" {
		return infraerrors.BadRequest("INVALID_EMAIL", "recipient email is required")
	}

	payload := cloudflareEmailRequest{
		To:      to,
		From:    from,
		Subject: subject,
	}
	if strings.Contains(body, "<") {
		payload.HTML = body
		payload.Text = stripHTMLTags(body)
	} else {
		payload.Text = body
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cloudflare email marshal: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cloudflareEmailTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf(cloudflareEmailEndpointTemplate, accountID), bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("cloudflare email request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare email send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("cloudflare email read: %w", err)
	}

	var parsed cloudflareEmailResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return fmt.Errorf("cloudflare email response (http %d): %s", resp.StatusCode, truncateEmailBodyForLog(string(respBody), 300))
	}
	if !parsed.Success {
		var b strings.Builder
		for i, e := range parsed.Errors {
			if i > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(&b, "%d:%s", e.Code, e.Message)
		}
		if b.Len() == 0 {
			b.WriteString(fmt.Sprintf("http %d", resp.StatusCode))
		}
		return fmt.Errorf("cloudflare email rejected: %s", b.String())
	}
	return nil
}

func truncateEmailBodyForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// stripHTMLTags 生成简易纯文本版本，供同时提交 text 字段。
func stripHTMLTags(input string) string {
	var b strings.Builder
	inTag := false
	for _, r := range input {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.ReplaceAll(out, "&nbsp;", " ")
	out = strings.ReplaceAll(out, "&amp;", "&")
	out = strings.ReplaceAll(out, "&lt;", "<")
	out = strings.ReplaceAll(out, "&gt;", ">")
	return out
}
