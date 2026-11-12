package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withCloudflareEndpoint(t *testing.T, url string) {
	t.Helper()
	prev := cloudflareEmailEndpointTemplate
	cloudflareEmailEndpointTemplate = url
	t.Cleanup(func() { cloudflareEmailEndpointTemplate = prev })
}

func TestSendWithCloudflareSuccess(t *testing.T) {
	var gotPath, gotAuth string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"message_id":"<m@x>","queued":["rcpt@example.com"]}}`))
	}))
	defer srv.Close()
	withCloudflareEndpoint(t, srv.URL+"/accounts/%s/email/sending/send")

	svc := &EmailService{}
	cfg := &SMTPConfig{
		Provider:    EmailProviderCloudflare,
		CFAccountID: "acc123",
		CFAPIToken:  "tok123",
		From:        "noreply@api.xgwise.com",
	}
	err := svc.SendEmailWithConfig(cfg, "rcpt@example.com", "hi", "<p>hello</p>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/accounts/acc123/email/sending/send" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if payload["from"] != "noreply@api.xgwise.com" || payload["subject"] != "hi" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload["html"] != "<p>hello</p>" {
		t.Fatalf("html = %#v", payload["html"])
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %#v", payload["text"])
	}
}

func TestSendWithCloudflareRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10202,"message":"email.sending.error.email.invalid"}],"result":null}`))
	}))
	defer srv.Close()
	withCloudflareEndpoint(t, srv.URL+"/accounts/%s/email/sending/send")

	svc := &EmailService{}
	err := svc.SendEmailWithConfig(&SMTPConfig{
		Provider:    EmailProviderCloudflare,
		CFAccountID: "acc123",
		CFAPIToken:  "tok123",
		From:        "nope@example.com",
	}, "rcpt@example.com", "hi", "hello")
	if err == nil {
		t.Fatal("expected error for rejected request")
	}
	if !strings.Contains(err.Error(), "10202") {
		t.Fatalf("error should surface CF code: %v", err)
	}
}

func TestSendWithCloudflareMissingConfig(t *testing.T) {
	svc := &EmailService{}
	err := svc.SendEmailWithConfig(&SMTPConfig{Provider: EmailProviderCloudflare}, "rcpt@example.com", "s", "b")
	if err == nil {
		t.Fatal("expected ErrEmailNotConfigured")
	}
}
