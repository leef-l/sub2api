//go:build unit

package provider

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/smartwalle/alipay/v3"
	"github.com/stretchr/testify/require"
)

func TestNewAlipayDesktopWapPayValidation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "true", "false", " TRUE ", "False"} {
		t.Run("accepted_"+value, func(t *testing.T) {
			t.Parallel()
			_, err := NewAlipay("test", map[string]string{"appId": "test", "privateKey": "test", alipayCfgDesktopWapPay: value})
			require.NoError(t, err)
		})
	}
	for _, value := range []string{"1", "yes", "wap", "not-a-bool"} {
		t.Run("invalid_"+value, func(t *testing.T) {
			t.Parallel()
			_, err := NewAlipay("test", map[string]string{"appId": "test", "privateKey": "test", alipayCfgDesktopWapPay: value})
			require.ErrorContains(t, err, "desktopWapPay")
		})
	}
}

func TestAlipayDesktopWapPayRouting(t *testing.T) {
	// SDK hook globals are shared with existing tests; keep this test sequential.
	originalWap, originalPage, originalPrecreate := alipayTradeWapPay, alipayTradePagePay, alipayTradePreCreate
	t.Cleanup(func() {
		alipayTradeWapPay, alipayTradePagePay, alipayTradePreCreate = originalWap, originalPage, originalPrecreate
	})
	for _, tc := range []struct {
		name, enabled, mode, want string
		mobile, mobilePrecreate   bool
	}{
		{name: "desktop opt in", enabled: "true", want: "wap"},
		{name: "desktop opt in overrides redirect", enabled: "true", mode: "redirect", want: "wap"},
		{name: "desktop opt in overrides qrcode", enabled: "true", mode: "qrcode", want: "wap"},
		{name: "normalized flag", enabled: " TRUE ", want: "wap"},
		{name: "missing flag retains QR", want: "precreate"},
		{name: "disabled flag retains QR", enabled: "false", want: "precreate"},
		{name: "disabled flag retains page pay", enabled: "false", mode: "redirect", want: "page"},
		{name: "missing flag retains page pay", mode: "redirect", want: "page"},
		{name: "desktop ignores mobile-only precreate flag", enabled: "true", mobilePrecreate: true, want: "wap"},
		{name: "mobile keeps WAP", enabled: "true", mobile: true, want: "wap"},
		{name: "mobile keeps existing precreate", enabled: "true", mobile: true, mobilePrecreate: true, want: "precreate"},
		{name: "mobile precreate without desktop opt in", enabled: "false", mobile: true, mobilePrecreate: true, want: "precreate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			alipayTradeWapPay = func(_ *alipay.Client, p alipay.TradeWapPay) (*url.URL, error) {
				calls = append(calls, "wap")
				require.Equal(t, "QUICK_WAP_WAY", p.ProductCode)
				require.Equal(t, "order-desktop-wap", p.OutTradeNo)
				require.Equal(t, "10.00", p.TotalAmount)
				require.Equal(t, "Recharge", p.Subject)
				require.Equal(t, "https://merchant.example/api/v1/payment/webhook/alipay", p.NotifyURL)
				require.Equal(t, "https://merchant.example/payment/result?order_id=1&resume_token=opaque", p.ReturnURL)
				return url.Parse("https://openapi.alipay.com/gateway.do?method=alipay.trade.wap.pay")
			}
			alipayTradePagePay = func(_ *alipay.Client, _ alipay.TradePagePay) (*url.URL, error) {
				calls = append(calls, "page")
				return url.Parse("https://openapi.alipay.com/gateway.do?method=alipay.trade.page.pay")
			}
			alipayTradePreCreate = func(_ context.Context, _ *alipay.Client, _ alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
				calls = append(calls, "precreate")
				return &alipay.TradePreCreateRsp{Error: alipay.Error{Code: alipay.CodeSuccess}, QRCode: "https://qr.alipay.example/fixture"}, nil
			}
			prov := &Alipay{client: &alipay.Client{}, config: map[string]string{
				alipayCfgDesktopWapPay: tc.enabled, "paymentMode": tc.mode,
				"notifyUrl": "https://unused.example/notify", "returnUrl": "https://unused.example/return",
			}}
			resp, err := prov.CreatePayment(context.Background(), payment.CreatePaymentRequest{
				OrderID: "order-desktop-wap", Amount: "10.00", Subject: "Recharge",
				IsMobile: tc.mobile, AlipayMobilePrecreate: tc.mobilePrecreate,
				NotifyURL: "https://merchant.example/api/v1/payment/webhook/alipay",
				ReturnURL: "https://merchant.example/payment/result?order_id=1&resume_token=opaque",
			})
			require.NoError(t, err)
			require.Equal(t, []string{tc.want}, calls)
			if tc.want == "precreate" {
				require.Empty(t, resp.PayURL)
				require.NotEmpty(t, resp.QRCode)
			} else {
				require.Empty(t, resp.QRCode)
				require.NotEmpty(t, resp.PayURL)
			}
		})
	}
}

func TestAlipayDesktopWapPayErrorDoesNotFallBack(t *testing.T) {
	originalWap, originalPage, originalPrecreate := alipayTradeWapPay, alipayTradePagePay, alipayTradePreCreate
	t.Cleanup(func() {
		alipayTradeWapPay, alipayTradePagePay, alipayTradePreCreate = originalWap, originalPage, originalPrecreate
	})
	failure := errors.New("wap signing failure")
	alipayTradeWapPay = func(_ *alipay.Client, _ alipay.TradeWapPay) (*url.URL, error) { return nil, failure }
	alipayTradePagePay = func(_ *alipay.Client, _ alipay.TradePagePay) (*url.URL, error) {
		t.Fatal("desktop WAP failure must not silently switch to page pay")
		return nil, nil
	}
	alipayTradePreCreate = func(_ context.Context, _ *alipay.Client, _ alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		t.Fatal("desktop WAP failure must not silently switch to precreate")
		return nil, nil
	}
	prov := &Alipay{client: &alipay.Client{}, config: map[string]string{alipayCfgDesktopWapPay: "true"}}
	resp, err := prov.CreatePayment(context.Background(), payment.CreatePaymentRequest{OrderID: "test", Amount: "1.00"})
	require.Nil(t, resp)
	require.ErrorIs(t, err, failure)
	require.ErrorContains(t, err, "TradeWapPay")
}

func TestAlipayDesktopWapPaySDKURLWithoutNetwork(t *testing.T) {
	// TradeWapPay only creates and signs the checkout URL; this performs no API
	// requests and uses generated fixture keys, not production merchant material.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	// Verification always uses Alipay's key, never the merchant's own, so the
	// fixture public key deliberately belongs to a different pair.
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	publicDER, err := x509.MarshalPKIXPublicKey(&platformKey.PublicKey)
	require.NoError(t, err)
	prov, err := NewAlipay("fixture", map[string]string{
		"appId": "fixture-app", "privateKey": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})),
		"publicKey":            string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
		alipayCfgDesktopWapPay: "true", "paymentMode": "redirect",
		"notifyUrl": "https://merchant.example/api/v1/payment/webhook/alipay",
		"returnUrl": "https://merchant.example/payment/result?order_id=42&resume_token=opaque&status=success",
	})
	require.NoError(t, err)
	resp, err := prov.CreatePayment(context.Background(), payment.CreatePaymentRequest{OrderID: "fixture-42", Amount: "10.00", Subject: "Fixture", IsMobile: false})
	require.NoError(t, err)
	require.Empty(t, resp.QRCode)
	u, err := url.Parse(resp.PayURL)
	require.NoError(t, err)
	require.Equal(t, "https", u.Scheme)
	require.Equal(t, "openapi.alipay.com", u.Host)
	require.Equal(t, "alipay.trade.wap.pay", u.Query().Get("method"))
	require.Equal(t, prov.config["notifyUrl"], u.Query().Get("notify_url"))
	require.Equal(t, prov.config["returnUrl"], u.Query().Get("return_url"))
	require.NotEmpty(t, u.Query().Get("sign"))
	require.Equal(t, "RSA2", u.Query().Get("sign_type"))
	var biz map[string]any
	require.NoError(t, json.Unmarshal([]byte(u.Query().Get("biz_content")), &biz))
	require.Equal(t, "QUICK_WAP_WAY", biz["product_code"])
	require.Equal(t, "10.00", biz["total_amount"])
	require.Equal(t, "fixture-42", biz["out_trade_no"])
	require.False(t, strings.Contains(u.Query().Get("method"), "precreate"))
}
