package provider

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

const (
	bepusdtTestTradeID = "dpefwLLTIuP0jVV4xt"
	bepusdtTestOrderID = "ORDER-123"
	bepusdtTestKey     = "BEPUSDT_TEST_SECRET"
	bepusdtTestToken   = "TEST_TRC20_WALLET"
)

func newBepusdtTestProvider(t *testing.T, apiBase string, overrides map[string]string) *Bepusdt {
	t.Helper()
	config := map[string]string{
		"pid": "1000", "pkey": bepusdtTestKey, "apiBase": apiBase,
		"notifyUrl": "https://merchant.test/notify", "returnUrl": "https://merchant.test/result",
	}
	for k, v := range overrides {
		config[k] = v
	}
	p, err := NewBepusdt("bepusdt-test", config)
	if err != nil {
		t.Fatalf("NewBepusdt: %v", err)
	}
	return p
}

func bepusdtTestCreateRequest() payment.CreatePaymentRequest {
	return payment.CreatePaymentRequest{
		OrderID: bepusdtTestOrderID, Amount: "10.00", PaymentType: "usdt.trc20", Subject: "Balance recharge",
	}
}

func bepusdtTestForm(params map[string]string) url.Values {
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	return values
}

func TestBepusdtCreatePaymentCapturesCheckoutRedirect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		basePath string
		location string
	}{
		{name: "absolute", location: "absolute"},
		{name: "root_relative", location: "/pay/checkout/" + bepusdtTestTradeID},
		{name: "relative", location: "pay/checkout/" + bepusdtTestTradeID},
		{name: "relative_under_base_path", basePath: "/gateway", location: "pay/checkout/" + bepusdtTestTradeID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var submitCalls, checkoutCalls atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.basePath+"/submit.php" {
					checkoutCalls.Add(1)
					http.Error(w, "checkout must not be followed", http.StatusInternalServerError)
					return
				}
				submitCalls.Add(1)
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Errorf("submission = %s %s", r.Method, r.Header.Get("Content-Type"))
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm: %v", err)
				}
				if r.PostForm.Get("out_trade_no") != bepusdtTestOrderID {
					t.Errorf("out_trade_no = %q", r.PostForm.Get("out_trade_no"))
				}
				location := tc.location
				if location == "absolute" {
					location = server.URL + "/pay/checkout/" + bepusdtTestTradeID
				}
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusFound)
			}))
			defer server.Close()
			p := newBepusdtTestProvider(t, server.URL+tc.basePath, nil)
			// An injected client with the default redirect policy must also stop at 302.
			p.httpClient = server.Client()
			response, err := p.CreatePayment(context.Background(), bepusdtTestCreateRequest())
			if err != nil {
				t.Fatalf("CreatePayment: %v", err)
			}
			wantURL := server.URL + tc.basePath + "/pay/checkout/" + bepusdtTestTradeID
			if response.PayURL != wantURL || response.QRCode != wantURL || response.TradeNo != bepusdtTestTradeID {
				t.Fatalf("response = %+v, want checkout %q and trade_id %q", response, wantURL, bepusdtTestTradeID)
			}
			if response.Currency != "USD" || submitCalls.Load() != 1 || checkoutCalls.Load() != 0 {
				t.Fatalf("currency = %q, submit calls = %d, checkout calls = %d", response.Currency, submitCalls.Load(), checkoutCalls.Load())
			}
			if p.httpClient.CheckRedirect != nil {
				t.Fatal("shared HTTP client's redirect policy was mutated")
			}
		})
	}
}

func TestBepusdtCreatePaymentFiatSignature(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		currency string
		mode     string
		wantFiat string
	}{
		{name: "default_USD_qrcode", mode: "qrcode", wantFiat: "USD"},
		{name: "normalized_USD_qrcode", currency: " usd ", mode: "qrcode", wantFiat: "USD"},
		{name: "configured_CNY_qrcode", currency: " cny ", mode: "qrcode", wantFiat: "CNY"},
		{name: "default_USD_redirect", mode: "redirect", wantFiat: "USD"},
		{name: "configured_EUR_popup", currency: " eur ", mode: "popup", wantFiat: "EUR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			submitted := make(chan url.Values, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/submit.php" {
					t.Errorf("unexpected submission: %s %s", r.Method, r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm: %v", err)
				}
				submitted <- r.PostForm
				w.Header().Set("Location", "/pay/checkout/"+bepusdtTestTradeID)
				w.WriteHeader(http.StatusFound)
			}))
			defer server.Close()
			p := newBepusdtTestProvider(t, server.URL, map[string]string{"currency": tc.currency, "paymentMode": tc.mode})
			response, err := p.CreatePayment(context.Background(), bepusdtTestCreateRequest())
			if err != nil {
				t.Fatalf("CreatePayment: %v", err)
			}
			var values url.Values
			if tc.mode == "qrcode" {
				values = <-submitted
			} else {
				parsed, err := url.Parse(response.PayURL)
				if err != nil {
					t.Fatalf("parse submit URL: %v", err)
				}
				if parsed.Path != "/submit.php" || response.TradeNo != "" {
					t.Fatalf("unexpected redirect response: %+v", response)
				}
				values = parsed.Query()
				if len(submitted) != 0 {
					t.Fatal("redirect mode submitted an HTTP request")
				}
			}
			// Use a fixed canonical string rather than the production signing helper.
			canonical := "fiat=" + tc.wantFiat + "&money=10.00&name=Balance recharge&notify_url=https://merchant.test/notify&out_trade_no=ORDER-123&pid=1000&return_url=https://merchant.test/result&type=usdt.trc20" + bepusdtTestKey
			digest := md5.Sum([]byte(canonical))
			wantSign := hex.EncodeToString(digest[:])
			if values.Get("fiat") != tc.wantFiat || values.Get("sign") != wantSign || values.Get("sign_type") != "MD5" {
				t.Fatalf("fiat = %q, sign = %q, sign_type = %q; want %s, %s, MD5", values.Get("fiat"), values.Get("sign"), values.Get("sign_type"), tc.wantFiat, wantSign)
			}
			if response.Currency != tc.wantFiat {
				t.Fatalf("response currency = %q, want %s", response.Currency, tc.wantFiat)
			}
		})
	}
}

func TestBepusdtRejectsInvalidCurrency(t *testing.T) {
	t.Parallel()
	_, err := NewBepusdt("invalid", map[string]string{
		"pid": "1000", "pkey": bepusdtTestKey, "apiBase": "https://gateway.test",
		"notifyUrl": "https://merchant.test/notify", "returnUrl": "https://merchant.test/result", "currency": "USDT",
	})
	if err == nil || !strings.Contains(err.Error(), "currency") {
		t.Fatalf("invalid currency error = %v", err)
	}
}

func TestBepusdtCreatePaymentRejectsInvalidCheckout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		status   int
		location string
	}{
		{name: "not_redirect", status: http.StatusOK},
		{name: "missing_Location", status: http.StatusFound},
		{name: "missing_trade_id", status: http.StatusFound, location: "/pay/checkout/"},
		{name: "wrong_path", status: http.StatusFound, location: "/pay/error"},
		{name: "unsafe_scheme", status: http.StatusFound, location: "javascript:alert(1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			p := newBepusdtTestProvider(t, server.URL, nil)
			if response, err := p.CreatePayment(context.Background(), bepusdtTestCreateRequest()); err == nil || response != nil {
				t.Fatalf("invalid checkout response = %+v, error = %v", response, err)
			}
		})
	}
}

func assertBepusdtQueryRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/pay/info" || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("unexpected query: %s %s (%s)", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
	}
	var payload map[string]string
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Errorf("decode query: %v", err)
		return
	}
	if len(payload) != 1 || payload["trade_id"] != bepusdtTestTradeID {
		t.Errorf("query payload = %+v; want only trade_id=%s", payload, bepusdtTestTradeID)
	}
}

func TestBepusdtQueryOrder(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		statusJSON string
		wantStatus string
		lookupURL  bool
	}{
		{name: "pending", statusJSON: "1", wantStatus: payment.ProviderStatusPending},
		{name: "paid", statusJSON: "2", wantStatus: payment.ProviderStatusPaid},
		{name: "closed", statusJSON: "3", wantStatus: payment.ProviderStatusFailed},
		{name: "unknown_is_pending", statusJSON: "99", wantStatus: payment.ProviderStatusPending},
		{name: "string_status_checkout_URL", statusJSON: `"2"`, wantStatus: payment.ProviderStatusPaid, lookupURL: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertBepusdtQueryRequest(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"status_code":200,"message":"success","data":{"trade_id":%q,"order_id":%q,"money":"10","actual_amount":"10.02","fiat":"usd","status":%s}}`, bepusdtTestTradeID, bepusdtTestOrderID, tc.statusJSON)
			}))
			defer server.Close()
			p := newBepusdtTestProvider(t, server.URL+"/api/v1/pay/info", nil)
			lookup := bepusdtTestTradeID
			if tc.lookupURL {
				lookup = server.URL + "/pay/checkout/" + bepusdtTestTradeID
			}
			response, err := p.QueryOrder(context.Background(), lookup)
			if err != nil {
				t.Fatalf("QueryOrder: %v", err)
			}
			if response.TradeNo != bepusdtTestTradeID || response.Status != tc.wantStatus || response.Amount != 10.02 {
				t.Fatalf("unexpected query response: %+v", response)
			}
			if response.Metadata["currency"] != "USD" || response.Metadata["money"] != "10" || response.Metadata["actual_amount"] != "10.02" || response.Metadata["order_id"] != bepusdtTestOrderID || response.Metadata["pid"] != "1000" {
				t.Fatalf("unexpected query metadata: %+v", response.Metadata)
			}
			if response.PaidAt != "" {
				t.Fatalf("PaidAt = %q without an upstream payment timestamp", response.PaidAt)
			}
		})
	}
}

func TestBepusdtQueryOrderNotFound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertBepusdtQueryRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		// The application error must override even a paid-looking data object.
		_, _ = fmt.Fprint(w, `{"status_code":400,"message":"order not found","data":{"status":2}}`)
	}))
	defer server.Close()
	p := newBepusdtTestProvider(t, server.URL, nil)
	response, err := p.QueryOrder(context.Background(), bepusdtTestTradeID)
	if response != nil || err == nil || !strings.Contains(err.Error(), "status_code 400") || !strings.Contains(err.Error(), "order not found") {
		t.Fatalf("response = %+v, error = %v", response, err)
	}
}

func TestBepusdtQueryOrderRejectsMissingTradeID(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	p := newBepusdtTestProvider(t, server.URL, nil)
	for _, lookup := range []string{"", " \t", server.URL + "/pay/checkout/"} {
		response, err := p.QueryOrder(context.Background(), lookup)
		if response != nil || err == nil || !strings.Contains(err.Error(), "trade_id") {
			t.Fatalf("lookup %q: response = %+v, error = %v", lookup, response, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("missing trade_id made %d HTTP calls", calls.Load())
	}
}

func TestBepusdtQueryOrderRejectsInvalidResponse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "HTTP_error", status: http.StatusBadGateway, body: `{"message":"unavailable"}`},
		{name: "malformed_JSON", status: http.StatusOK, body: `<html>error</html>`},
		{name: "trade_id_mismatch", status: http.StatusOK, body: `{"status_code":200,"data":{"trade_id":"OTHER","fiat":"USD","actual_amount":"10.02","status":2}}`},
		{name: "missing_trade_id", status: http.StatusOK, body: `{"status_code":200,"data":{"fiat":"USD","actual_amount":"10.02","status":2}}`},
		{name: "missing_fiat", status: http.StatusOK, body: `{"status_code":200,"data":{"trade_id":"dpefwLLTIuP0jVV4xt","actual_amount":"10.02","status":2}}`},
		{name: "nonfinite_actual_amount", status: http.StatusOK, body: `{"status_code":200,"data":{"trade_id":"dpefwLLTIuP0jVV4xt","fiat":"USD","actual_amount":"NaN","status":2}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assertBepusdtQueryRequest(t, r)
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			p := newBepusdtTestProvider(t, server.URL, nil)
			if response, err := p.QueryOrder(context.Background(), bepusdtTestTradeID); response != nil || err == nil {
				t.Fatalf("response = %+v, error = %v", response, err)
			}
		})
	}
}

func bepusdtTestNotifyParams() map[string]string {
	return map[string]string{
		"trade_id": bepusdtTestTradeID, "order_id": bepusdtTestOrderID,
		"amount": "10.00", "actual_amount": "10.02", "status": "2", "token": bepusdtTestToken,
	}
}

func bepusdtTestSignedNotify(params map[string]string) url.Values {
	values := bepusdtTestForm(params)
	values.Set("sign", easyPaySign(params, bepusdtTestKey))
	values.Set("sign_type", signTypeMD5)
	return values
}

func TestBepusdtVerifyNotification(t *testing.T) {
	t.Parallel()
	p := newBepusdtTestProvider(t, "https://gateway.test", nil)
	values := bepusdtTestSignedNotify(bepusdtTestNotifyParams())
	n, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
	if err != nil {
		t.Fatalf("genuine callback: %v", err)
	}
	if n.OrderID != bepusdtTestOrderID || n.TradeNo != bepusdtTestTradeID || n.Amount != 10 || n.Status != payment.NotificationStatusSuccess {
		t.Fatalf("unexpected notification: %+v", n)
	}
	if n.Metadata["actual_amount"] != "10.02" || n.Metadata["currency"] != "USD" || n.Metadata["token"] != bepusdtTestToken {
		t.Fatalf("unexpected notification metadata: %+v", n.Metadata)
	}
	for _, field := range []string{"trade_id", "order_id", "amount", "actual_amount", "status", "token"} {
		t.Run("tampered_"+field, func(t *testing.T) {
			forged := bepusdtTestSignedNotify(bepusdtTestNotifyParams())
			forged.Set(field, "TAMPERED")
			if _, err := p.VerifyNotification(context.Background(), forged.Encode(), nil); err == nil || !strings.Contains(err.Error(), "invalid signature") {
				t.Fatalf("tampered %s error = %v", field, err)
			}
		})
	}
}

func TestBepusdtNotifyRejectsForgedSignReuseCallback(t *testing.T) {
	t.Parallel()
	p := newBepusdtTestProvider(t, "https://gateway.test", nil)
	params := map[string]string{
		"pid": "1000", "type": "usdt.trc20", "out_trade_no": bepusdtTestOrderID,
		"notify_url": p.config["notifyUrl"], "return_url": "https://merchant.test/result?order_id=99&status=2",
		"name": "Balance recharge", "money": "10.00", "fiat": "USD",
	}
	replayedSign := easyPaySign(params, bepusdtTestKey)
	params["return_url"] = "https://merchant.test/result?order_id=99"
	params["status"] = "2"
	if easyPaySign(params, bepusdtTestKey) != replayedSign {
		t.Fatal("PoC must reuse the exact submission signature")
	}
	values := bepusdtTestForm(params)
	values.Set("sign", replayedSign)
	values.Set("sign_type", signTypeMD5)
	if _, err := p.VerifyNotification(context.Background(), values.Encode(), nil); err == nil {
		t.Fatal("forged sign-reuse callback accepted")
	}
}

func TestBepusdtNotifyRejectsOrderURLReplay(t *testing.T) {
	t.Parallel()
	p := newBepusdtTestProvider(t, "https://gateway.test", map[string]string{"paymentMode": "redirect"})
	response, err := p.CreatePayment(context.Background(), bepusdtTestCreateRequest())
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	parsed, err := url.Parse(response.PayURL)
	if err != nil {
		t.Fatalf("parse submit URL: %v", err)
	}
	if _, err := p.VerifyNotification(context.Background(), parsed.RawQuery, nil); err == nil {
		t.Fatal("replayed signed order URL accepted")
	}
}

func TestBepusdtNotifyRejectsUnsafeParams(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(map[string]string)
		append string
	}{
		{name: "unknown_empty_param", mutate: func(p map[string]string) { p["device"] = "" }},
		{name: "creation_only_param", mutate: func(p map[string]string) { p["return_url"] = "https://merchant.test/result" }},
		{name: "delimiter_smuggling", mutate: func(p map[string]string) { p["order_id"] = "ORDER&status=2" }},
		{name: "nonfinite_amount", mutate: func(p map[string]string) { p["amount"] = "NaN" }},
		{name: "nonfinite_actual_amount", mutate: func(p map[string]string) { p["actual_amount"] = "+Inf" }},
		{name: "missing_token", mutate: func(p map[string]string) { delete(p, "token") }},
		{name: "unsupported_signature_type", mutate: func(p map[string]string) { p["sign_type"] = "RSA" }},
		{name: "duplicate_status", append: "&status=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newBepusdtTestProvider(t, "https://gateway.test", nil)
			params := bepusdtTestNotifyParams()
			if tc.mutate != nil {
				tc.mutate(params)
			}
			values := bepusdtTestSignedNotify(params)
			if params["sign_type"] != "" {
				values.Set("sign_type", params["sign_type"])
			}
			if _, err := p.VerifyNotification(context.Background(), values.Encode()+tc.append, nil); err == nil {
				t.Fatal("unsafe signed callback accepted")
			}
		})
	}
}

func TestBepusdtConfirmNotification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		field     string
		value     any
		missingID bool
		wantErr   string
	}{
		{name: "paid_with_fee"},
		{name: "pending", field: "status", value: 1, wantErr: "not paid"},
		{name: "different_order", field: "order_id", value: "OTHER", wantErr: "order_id mismatch"},
		{name: "different_money", field: "money", value: "11", wantErr: "amount mismatch"},
		{name: "different_actual_amount", field: "actual_amount", value: "11.02", wantErr: "actual_amount mismatch"},
		{name: "different_currency", field: "fiat", value: "CNY", wantErr: "fiat mismatch"},
		{name: "different_token", field: "token", value: "OTHER_WALLET", wantErr: "token mismatch"},
		{name: "missing_trade_id", missingID: true, wantErr: "missing trade_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assertBepusdtQueryRequest(t, r)
				data := map[string]any{
					"trade_id": bepusdtTestTradeID, "order_id": bepusdtTestOrderID,
					"money": "10", "actual_amount": "10.02", "fiat": "USD", "status": 2, "token": bepusdtTestToken,
				}
				if tc.field != "" {
					data[tc.field] = tc.value
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"status_code": 200, "message": "success", "data": data}); err != nil {
					t.Errorf("encode response: %v", err)
				}
			}))
			defer server.Close()
			p := newBepusdtTestProvider(t, server.URL, nil)
			n, err := p.VerifyNotification(context.Background(), bepusdtTestSignedNotify(bepusdtTestNotifyParams()).Encode(), nil)
			if err != nil {
				t.Fatalf("VerifyNotification: %v", err)
			}
			if tc.missingID {
				n.TradeNo = ""
			}
			err = p.ConfirmNotification(context.Background(), n)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ConfirmNotification: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			if tc.missingID && calls.Load() != 0 {
				t.Fatal("missing trade_id made an upstream request")
			}
		})
	}
}

func TestBepusdtRefundUnsupported(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	p := newBepusdtTestProvider(t, server.URL, nil)
	response, err := p.Refund(context.Background(), payment.RefundRequest{TradeNo: bepusdtTestTradeID, OrderID: bepusdtTestOrderID, Amount: "10.00"})
	if response != nil || !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), "does not support refunds") {
		t.Fatalf("response = %+v, error = %v", response, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("unsupported refund made %d HTTP calls", calls.Load())
	}
}
