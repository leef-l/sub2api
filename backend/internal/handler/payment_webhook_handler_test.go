//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/iotest"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteSuccessResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name            string
		providerKey     string
		wantCode        int
		wantContentType string
		wantBody        string
		checkJSON       bool
		wantJSONCode    string
		wantJSONMessage string
	}{
		{
			name:            "wxpay returns JSON with code SUCCESS",
			providerKey:     "wxpay",
			wantCode:        http.StatusOK,
			wantContentType: "application/json",
			checkJSON:       true,
			wantJSONCode:    "SUCCESS",
			wantJSONMessage: "成功",
		},
		{
			name:            "stripe returns empty 200",
			providerKey:     "stripe",
			wantCode:        http.StatusOK,
			wantContentType: "text/plain",
			wantBody:        "",
		},
		{
			name:            "airwallex returns empty 200",
			providerKey:     payment.TypeAirwallex,
			wantCode:        http.StatusOK,
			wantContentType: "text/plain",
			wantBody:        "",
		},
		{
			name:            "easypay returns plain text success",
			providerKey:     "easypay",
			wantCode:        http.StatusOK,
			wantContentType: "text/plain",
			wantBody:        "success",
		},
		{
			name:            "bepusdt returns plain text success",
			providerKey:     "bepusdt",
			wantCode:        http.StatusOK,
			wantContentType: "text/plain",
			wantBody:        "success",
		},
		{
			name:            "alipay returns plain text success",
			providerKey:     "alipay",
			wantCode:        http.StatusOK,
			wantContentType: "text/plain",
			wantBody:        "success",
		},
		{
			name:            "unknown provider returns plain text success",
			providerKey:     "unknown_provider",
			wantCode:        http.StatusOK,
			wantContentType: "text/plain",
			wantBody:        "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			writeSuccessResponse(c, tt.providerKey)

			assert.Equal(t, tt.wantCode, w.Code)
			assert.Contains(t, w.Header().Get("Content-Type"), tt.wantContentType)

			if tt.checkJSON {
				var resp wxpaySuccessResponse
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				require.NoError(t, err, "response body should be valid JSON")
				assert.Equal(t, tt.wantJSONCode, resp.Code)
				assert.Equal(t, tt.wantJSONMessage, resp.Message)
			} else {
				assert.Equal(t, tt.wantBody, w.Body.String())
			}
		})
	}
}

// TestUnknownOrderWebhookAcksWithSuccess exercises the response contract that
// handleNotify relies on when HandlePaymentNotification returns ErrOrderNotFound:
// we still need to emit the provider-specific 2xx so the provider stops
// retrying. We can't easily drive handleNotify end-to-end without mocking the
// concrete *service.PaymentService, so this test locks down the two ingredients
// the fix depends on:
//  1. errors.Is recognises the sentinel through fmt.Errorf %w wrapping (which
//     is how service layer wraps it with the out_trade_no context).
//  2. writeSuccessResponse produces the provider-specific body for Stripe
//     (empty 200) — matching what handleNotify calls on the ack path.
//
// If either contract breaks, the Stripe "unknown order → 500 loop" regresses.
func TestUnknownOrderWebhookAcksWithSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1) Sentinel recognition through wrapping.
	wrapped := fmt.Errorf("%w: out_trade_no=sub2_missing_42", service.ErrOrderNotFound)
	require.True(t, errors.Is(wrapped, service.ErrOrderNotFound),
		"handleNotify uses errors.Is on the wrapped service error; regression here "+
			"would mean unknown-order webhooks go back to returning 500 and looping forever")

	// A distinct error must NOT match — otherwise a DB failure would be silently
	// swallowed as an ack.
	other := errors.New("lookup order failed: connection refused")
	require.False(t, errors.Is(other, service.ErrOrderNotFound))

	// 2) Provider-specific success body is what handleNotify emits on the
	// ack path. Asserted again here because this is the shape Stripe expects
	// to consider the webhook acknowledged.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	writeSuccessResponse(c, payment.TypeStripe)
	require.Equal(t, http.StatusOK, w.Code,
		"Stripe requires 2xx to stop retrying; anything else restarts the retry loop")
	require.Empty(t, w.Body.String(), "Stripe expects an empty body on the ack path")
}

func TestWebhookConstants(t *testing.T) {
	t.Run("maxWebhookBodySize is 1MB", func(t *testing.T) {
		assert.Equal(t, int64(1<<20), int64(maxWebhookBodySize))
	})

	t.Run("webhookLogTruncateLen is 200", func(t *testing.T) {
		assert.Equal(t, 200, webhookLogTruncateLen)
	})
}

func TestExtractOutTradeNo(t *testing.T) {
	tests := []struct {
		name        string
		providerKey string
		rawBody     string
		want        string
	}{
		{
			name:        "easypay query payload",
			providerKey: "easypay",
			rawBody:     "out_trade_no=sub2_123&trade_status=TRADE_SUCCESS",
			want:        "sub2_123",
		},
		{
			name:        "bepusdt form payload",
			providerKey: "bepusdt",
			rawBody:     "pid=merchant_1&out_trade_no=sub2_be_123&status=1&amount=10.00&actual_amount=1.25",
			want:        "sub2_be_123",
		},
		{
			name:        "alipay query payload",
			providerKey: "alipay",
			rawBody:     "notify_time=2026-04-20+12%3A00%3A00&out_trade_no=sub2_456",
			want:        "sub2_456",
		},
		{
			name:        "unknown provider",
			providerKey: "wxpay",
			rawBody:     "{}",
			want:        "",
		},
		{
			name:        "airwallex payment intent payload",
			providerKey: payment.TypeAirwallex,
			rawBody:     `{"name":"payment_intent.succeeded","data":{"object":{"merchant_order_id":"sub2_awx_123"}}}`,
			want:        "sub2_awx_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractOutTradeNo(tt.rawBody, tt.providerKey))
		})
	}
}

func TestVerifyNotificationWithProvidersReturnsMatchedProvider(t *testing.T) {
	tests := []struct {
		name        string
		providerKey string
		rawBody     string
		headers     map[string]string
	}{
		{
			name:        "wxpay successful callback",
			providerKey: payment.TypeWxpay,
			rawBody:     "{}",
			headers:     map[string]string{"wechatpay-signature": "sig"},
		},
		{
			name:        "bepusdt successful callback",
			providerKey: "bepusdt",
			rawBody:     "out_trade_no=sub2_42&trade_id=trade-42&status=1&sign=valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			firstErr := errors.New("wrong provider")
			providers := []payment.Provider{
				webhookHandlerProviderStub{
					key:       tt.providerKey,
					verifyErr: firstErr,
				},
				webhookHandlerProviderStub{
					key: tt.providerKey,
					notification: &payment.PaymentNotification{
						OrderID: "sub2_42",
						TradeNo: "trade-42",
						Status:  payment.NotificationStatusSuccess,
					},
				},
			}

			provider, notification, err := verifyNotificationWithProviders(context.Background(), providers, tt.rawBody, tt.headers)
			require.NoError(t, err)
			require.Equal(t, tt.providerKey, provider.ProviderKey())
			require.NotNil(t, notification)
			require.Equal(t, "sub2_42", notification.OrderID)
			require.Equal(t, "trade-42", notification.TradeNo)
			require.Equal(t, payment.NotificationStatusSuccess, notification.Status)
		})
	}
}

func TestVerifyNotificationWithProvidersFailsWhenAllProvidersReject(t *testing.T) {
	tests := []struct {
		name        string
		providerKey string
		rawBody     string
		wantError   string
	}{
		{
			name:        "wxpay all providers reject",
			providerKey: payment.TypeWxpay,
			rawBody:     "{}",
			wantError:   "verify failed b",
		},
		{
			name:        "bepusdt invalid signature",
			providerKey: "bepusdt",
			rawBody:     "out_trade_no=sub2_42&trade_id=trade-42&status=1&sign=invalid",
			wantError:   "invalid signature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providers := []payment.Provider{
				webhookHandlerProviderStub{
					key:       tt.providerKey,
					verifyErr: errors.New("verify failed a"),
				},
				webhookHandlerProviderStub{
					key:       tt.providerKey,
					verifyErr: errors.New(tt.wantError),
				},
			}

			provider, notification, err := verifyNotificationWithProviders(context.Background(), providers, tt.rawBody, nil)
			require.EqualError(t, err, tt.wantError)
			require.Nil(t, provider)
			require.Nil(t, notification)
		})
	}
}

func TestEasyPayCompatibleNotifyReadFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &PaymentWebhookHandler{}
	tests := []struct {
		providerKey string
		notify      gin.HandlerFunc
	}{
		{providerKey: "easypay", notify: handler.EasyPayNotify},
		{providerKey: "bepusdt", notify: handler.BepusdtNotify},
	}

	for _, tt := range tests {
		t.Run(tt.providerKey, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/payment/webhook/"+tt.providerKey, iotest.ErrReader(errors.New("read failed")))
			c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			tt.notify(c)

			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Equal(t, "failed to read body", w.Body.String())
		})
	}
}

type webhookHandlerProviderStub struct {
	key          string
	notification *payment.PaymentNotification
	verifyErr    error
}

func (p webhookHandlerProviderStub) Name() string        { return p.key }
func (p webhookHandlerProviderStub) ProviderKey() string { return p.key }
func (p webhookHandlerProviderStub) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.PaymentType(p.key)}
}
func (p webhookHandlerProviderStub) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	panic("unexpected call")
}
func (p webhookHandlerProviderStub) QueryOrder(context.Context, string) (*payment.QueryOrderResponse, error) {
	panic("unexpected call")
}
func (p webhookHandlerProviderStub) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	if p.verifyErr != nil {
		return nil, p.verifyErr
	}
	return p.notification, nil
}
func (p webhookHandlerProviderStub) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	panic("unexpected call")
}
