//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

const checkoutTestSigningKey = "0123456789abcdef0123456789abcdef"

func checkoutTestPayURL(t *testing.T, appID, outTradeNo, amount string) string {
	t.Helper()
	biz, err := json.Marshal(map[string]string{
		"out_trade_no": outTradeNo,
		"total_amount": amount,
		"product_code": "QUICK_WAP_WAY",
	})
	require.NoError(t, err)
	u, err := url.Parse("https://openapi.alipay.com/gateway.do")
	require.NoError(t, err)
	q := u.Query()
	q.Set("app_id", appID)
	q.Set("method", "alipay.trade.wap.pay")
	q.Set("sign_type", "RSA2")
	q.Set("sign", "fixture-signature")
	q.Set("biz_content", string(biz))
	u.RawQuery = q.Encode()
	return u.String()
}

type checkoutFixture struct {
	svc        *PaymentService
	client     *dbent.Client
	instanceID string
}

func newCheckoutFixture(t *testing.T) *checkoutFixture {
	t.Helper()
	client := newPaymentConfigServiceTestClient(t)
	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("checkout-alipay").
		SetConfig(`{"appId":"checkout-app","desktopWapPay":"true"}`).
		SetSupportedTypes(payment.TypeAlipay).
		SetEnabled(true).
		Save(context.Background())
	require.NoError(t, err)
	return &checkoutFixture{
		svc: &PaymentService{
			entClient:     client,
			configService: &PaymentConfigService{entClient: client},
			resumeService: NewPaymentResumeService([]byte(checkoutTestSigningKey)),
		},
		client:     client,
		instanceID: strconv.FormatInt(int64(inst.ID), 10),
	}
}

func (f *checkoutFixture) seedOrder(t *testing.T, status string, expiresAt time.Time) (*dbent.PaymentOrder, string) {
	t.Helper()
	ctx := context.Background()
	user, err := f.client.User.Create().
		SetEmail("checkout@example.com").
		SetPasswordHash("hash").
		SetUsername("checkout-user").
		Save(ctx)
	require.NoError(t, err)
	payURL := checkoutTestPayURL(t, "checkout-app", "sub2_checkout_order", "88.00")
	order, err := f.client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(80).
		SetPayAmount(88).
		SetFeeRate(0.1).
		SetRechargeCode("CHECKOUT-ORDER").
		SetOutTradeNo("sub2_checkout_order").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-checkout").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(status).
		SetPayURL(payURL).
		SetExpiresAt(expiresAt).
		SetClientIP("127.0.0.1").
		SetSrcHost("app.example.com").
		SetProviderInstanceID(f.instanceID).
		SetProviderKey(payment.TypeAlipay).
		SetProviderSnapshot(map[string]any{
			"schema_version":          2,
			"provider_instance_id":    f.instanceID,
			"provider_key":            payment.TypeAlipay,
			"merchant_app_id":         "checkout-app",
			"alipay_desktop_checkout": true,
		}).
		Save(ctx)
	require.NoError(t, err)
	return order, payURL
}

func (f *checkoutFixture) selection() *payment.InstanceSelection {
	return &payment.InstanceSelection{
		InstanceID:  f.instanceID,
		ProviderKey: payment.TypeAlipay,
		Config:      map[string]string{"appId": "checkout-app", "desktopWapPay": "true"},
	}
}

func (f *checkoutFixture) token(t *testing.T, order *dbent.PaymentOrder, payURL string) string {
	t.Helper()
	qr, err := f.svc.createAlipayCheckoutQR(order, f.selection(), "https://app.example.com/payment/result", payURL)
	require.NoError(t, err)
	u, err := url.Parse(qr)
	require.NoError(t, err)
	require.Equal(t, "app.example.com", u.Host)
	require.Equal(t, paymentCheckoutPath, u.Path)
	require.Empty(t, u.RawQuery, "the capability must travel in the fragment")
	require.True(t, strings.HasPrefix(u.Fragment, "token="))
	return strings.TrimPrefix(u.Fragment, "token=")
}

func TestPaymentCheckoutResolvesOwnOrder(t *testing.T) {
	ctx := context.Background()
	f := newCheckoutFixture(t)
	order, payURL := f.seedOrder(t, OrderStatusPending, time.Now().Add(30*time.Minute))

	result, err := f.svc.GetPublicPaymentCheckout(ctx, f.token(t, order, payURL))
	require.NoError(t, err)
	require.Equal(t, order.ID, result.OrderID)
	require.Equal(t, "sub2_checkout_order", result.OutTradeNo)
	require.Equal(t, 88.0, result.PayAmount)
	require.Equal(t, OrderStatusPending, result.Status)
	require.True(t, result.CanPay)
	require.Equal(t, payURL, result.PayURL)
	require.True(t, result.ExpiresAt.Before(time.Now().Add(31*time.Minute)))
}

func TestPaymentCheckoutRejectsTamperedAndForeignTokens(t *testing.T) {
	ctx := context.Background()
	f := newCheckoutFixture(t)
	order, payURL := f.seedOrder(t, OrderStatusPending, time.Now().Add(30*time.Minute))
	token := f.token(t, order, payURL)

	last := token[len(token)-1]
	replacement := "A"
	if last == 'A' {
		replacement = "B"
	}
	_, err := f.svc.GetPublicPaymentCheckout(ctx, token[:len(token)-1]+replacement)
	require.Error(t, err)

	// A resume token for the same order must not unlock the checkout contract.
	resumeToken, err := f.svc.paymentResume().CreateToken(ResumeTokenClaims{OrderID: order.ID, UserID: order.UserID})
	require.NoError(t, err)
	_, err = f.svc.GetPublicPaymentCheckout(ctx, resumeToken)
	require.Error(t, err)

	_, err = f.svc.GetPublicPaymentCheckout(ctx, "not-a-token")
	require.Error(t, err)
}

func TestPaymentCheckoutRejectsChangedOrderState(t *testing.T) {
	ctx := context.Background()
	f := newCheckoutFixture(t)
	order, payURL := f.seedOrder(t, OrderStatusPending, time.Now().Add(30*time.Minute))
	token := f.token(t, order, payURL)

	// Re-pointing the stored checkout URL must invalidate an outstanding QR.
	_, err := f.client.PaymentOrder.UpdateOneID(order.ID).
		SetPayURL(checkoutTestPayURL(t, "checkout-app", "sub2_checkout_order", "1.00")).Save(ctx)
	require.NoError(t, err)
	_, err = f.svc.GetPublicPaymentCheckout(ctx, token)
	require.Error(t, err)

	// Restoring the URL but moving the order to another channel is rejected too.
	_, err = f.client.PaymentOrder.UpdateOneID(order.ID).SetPayURL(payURL).SetProviderInstanceID("999999").Save(ctx)
	require.NoError(t, err)
	_, err = f.svc.GetPublicPaymentCheckout(ctx, token)
	require.Error(t, err)
}

func TestPaymentCheckoutStopsHandingOutURLWhenNotPayable(t *testing.T) {
	ctx := context.Background()
	f := newCheckoutFixture(t)
	order, payURL := f.seedOrder(t, OrderStatusPending, time.Now().Add(30*time.Minute))
	token := f.token(t, order, payURL)

	_, err := f.client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusPaid).SetPaidAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	result, err := f.svc.GetPublicPaymentCheckout(ctx, token)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPaid, result.Status)
	require.False(t, result.CanPay)
	require.Empty(t, result.PayURL)

	// Disabling the merchant after QR creation must not keep an old link alive.
	_, err = f.client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusPending).Save(ctx)
	require.NoError(t, err)
	instID, err := strconv.ParseInt(f.instanceID, 10, 64)
	require.NoError(t, err)
	_, err = f.client.PaymentProviderInstance.UpdateOneID(instID).SetEnabled(false).Save(ctx)
	require.NoError(t, err)
	_, err = f.svc.GetPublicPaymentCheckout(ctx, token)
	require.Error(t, err)
}

func TestPaymentCheckoutRefusesExpiredOrderToken(t *testing.T) {
	f := newCheckoutFixture(t)
	order, payURL := f.seedOrder(t, OrderStatusPending, time.Now().Add(-1*time.Minute))
	_, err := f.svc.createAlipayCheckoutQR(order, f.selection(), "https://app.example.com/payment/result", payURL)
	require.Error(t, err)
}

func TestValidateAlipayCheckoutPayURL(t *testing.T) {
	order := &dbent.PaymentOrder{OutTradeNo: "sub2_checkout_order", PayAmount: 88}
	base := checkoutTestPayURL(t, "checkout-app", "sub2_checkout_order", "88.00")
	require.NoError(t, validateAlipayCheckoutPayURL(base, order, "checkout-app"))

	cases := map[string]string{
		"plain http":       strings.Replace(base, "https://", "http://", 1),
		"other host":       strings.Replace(base, "openapi.alipay.com", "evil.example.com", 1),
		"credentials":      strings.Replace(base, "https://", "https://user:pass@", 1),
		"duplicate method": base + "&method=alipay.trade.page.pay",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			require.Error(t, validateAlipayCheckoutPayURL(raw, order, "checkout-app"))
		})
	}
	require.Error(t, validateAlipayCheckoutPayURL(base, order, "other-app"), "merchant mismatch")
	require.Error(t, validateAlipayCheckoutPayURL(base, &dbent.PaymentOrder{OutTradeNo: "sub2_checkout_order", PayAmount: 1}, "checkout-app"), "amount mismatch")
	require.Error(t, validateAlipayCheckoutPayURL(base, &dbent.PaymentOrder{OutTradeNo: "other", PayAmount: 88}, "checkout-app"), "order mismatch")
}
