package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const (
	paymentCheckoutPath     = "/payment/checkout"
	paymentCheckoutAudience = "alipay_wap_checkout_v1"
	paymentCheckoutMaxTTL   = 24 * time.Hour
)

// A checkout token is intentionally NOT a resume token. Domain-separated keys
// and different claim names prevent a QR bearer from accessing resume endpoints
// that return a richer order record. No URL or merchant credential is in the QR.
type paymentCheckoutClaims struct {
	Audience   string `json:"aud"`
	OrderID    int64  `json:"co"`
	UserID     int64  `json:"cu"`
	InstanceID string `json:"ci"`
	OrderNo    string `json:"cn"`
	PayURLHash string `json:"ch"`
	IssuedAt   int64  `json:"iat"`
	ExpiresAt  int64  `json:"exp"`
}

// PaymentCheckoutResult is the entire anonymous scan-to-pay contract. The
// gateway URL is returned only while the same stored order remains payable.
// Deliberately omit user identifiers, email, balance, refunds and resume tokens.
type PaymentCheckoutResult struct {
	OrderID    int64     `json:"order_id"`
	OutTradeNo string    `json:"out_trade_no"`
	PayAmount  float64   `json:"pay_amount"`
	Currency   string    `json:"currency"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
	CanPay     bool      `json:"can_pay"`
	PayURL     string    `json:"pay_url,omitempty"`
}

func shouldUseAlipayDesktopCheckout(req CreateOrderRequest, sel *payment.InstanceSelection) bool {
	return !req.IsMobile && sel != nil &&
		strings.EqualFold(strings.TrimSpace(sel.ProviderKey), payment.TypeAlipay) &&
		strings.EqualFold(strings.TrimSpace(sel.Config["desktopWapPay"]), "true")
}

func checkoutSigningKey(key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("sub2api:payment-checkout:v1"))
	return mac.Sum(nil)
}

func (s *PaymentResumeService) checkoutSigner() (*PaymentResumeService, error) {
	if err := s.ensureSigningKey(); err != nil {
		return nil, err
	}
	fallbacks := make([][]byte, 0, len(s.verifyKeys))
	for _, key := range s.verifyKeys {
		fallbacks = append(fallbacks, checkoutSigningKey(key))
	}
	return NewPaymentResumeService(checkoutSigningKey(s.signingKey), fallbacks...), nil
}

func checkoutURLHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func invalidCheckoutToken() error {
	return infraerrors.BadRequest("INVALID_CHECKOUT_TOKEN", "checkout link is invalid or has expired")
}

func (s *PaymentResumeService) parseCheckoutToken(raw string) (*paymentCheckoutClaims, error) {
	signer, err := s.checkoutSigner()
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, invalidCheckoutToken()
	}
	var claims paymentCheckoutClaims
	if err := signer.parseSignedToken(raw, &claims); err != nil {
		return nil, invalidCheckoutToken()
	}
	now := time.Now().Unix()
	if claims.Audience != paymentCheckoutAudience || claims.OrderID <= 0 || claims.UserID <= 0 ||
		claims.InstanceID == "" || claims.OrderNo == "" || len(claims.PayURLHash) != 64 ||
		claims.IssuedAt <= 0 || claims.IssuedAt > now+60 || claims.ExpiresAt <= now ||
		claims.ExpiresAt <= claims.IssuedAt || claims.ExpiresAt-claims.IssuedAt > int64(paymentCheckoutMaxTTL/time.Second) {
		return nil, invalidCheckoutToken()
	}
	return &claims, nil
}

// Validate only the server-created, signed, official Alipay WAP gateway URL.
// Binding its merchant app, amount and order number prevents an accidental
// provider/URL mix-up from turning the public endpoint into an open redirect.
func validateAlipayCheckoutPayURL(raw string, order *dbent.PaymentOrder, appID string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "openapi.alipay.com") ||
		u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Path != "/gateway.do" || u.RawPath != "" || u.Fragment != "" {
		return infraerrors.BadRequest("INVALID_CHECKOUT_URL", "invalid Alipay checkout URL")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return infraerrors.BadRequest("INVALID_CHECKOUT_URL", "invalid Alipay checkout parameters")
	}
	for _, key := range []string{"method", "app_id", "biz_content", "sign", "sign_type"} {
		if len(q[key]) != 1 || q.Get(key) == "" {
			return infraerrors.BadRequest("INVALID_CHECKOUT_URL", "missing Alipay checkout parameters")
		}
	}
	if q.Get("method") != "alipay.trade.wap.pay" || q.Get("sign_type") != "RSA2" || appID == "" || q.Get("app_id") != appID {
		return infraerrors.BadRequest("INVALID_CHECKOUT_URL", "Alipay checkout product or merchant mismatch")
	}
	var biz struct {
		OutTradeNo  string `json:"out_trade_no"`
		TotalAmount string `json:"total_amount"`
		ProductCode string `json:"product_code"`
	}
	if err := json.Unmarshal([]byte(q.Get("biz_content")), &biz); err != nil {
		return infraerrors.BadRequest("INVALID_CHECKOUT_URL", "invalid Alipay checkout content")
	}
	amount, err := decimal.NewFromString(biz.TotalAmount)
	if order == nil || err != nil || biz.OutTradeNo != order.OutTradeNo || biz.ProductCode != "QUICK_WAP_WAY" ||
		!amount.Equal(decimal.NewFromFloat(order.PayAmount)) {
		return infraerrors.BadRequest("INVALID_CHECKOUT_URL", "Alipay checkout order mismatch")
	}
	return nil
}

func (s *PaymentService) createAlipayCheckoutQR(order *dbent.PaymentOrder, sel *payment.InstanceSelection, canonicalReturnURL, payURL string) (string, error) {
	signer, err := s.paymentResume().checkoutSigner()
	if err != nil {
		return "", err
	}
	if order == nil || sel == nil || strings.TrimSpace(sel.InstanceID) == "" {
		return "", invalidCheckoutToken()
	}
	if err := validateAlipayCheckoutPayURL(payURL, order, strings.TrimSpace(sel.Config["appId"])); err != nil {
		return "", err
	}
	u, err := url.Parse(canonicalReturnURL)
	if err != nil || !u.IsAbs() || u.Host == "" || u.Scheme != "https" || u.User != nil || u.Path != paymentResultReturnPath {
		return "", infraerrors.BadRequest("INVALID_CHECKOUT_ORIGIN", "desktop checkout requires an HTTPS site return URL")
	}
	now := time.Now()
	expiry := order.ExpiresAt
	if expiry.After(now.Add(paymentCheckoutMaxTTL)) {
		expiry = now.Add(paymentCheckoutMaxTTL)
	}
	if expiry.Unix() <= now.Unix() {
		return "", invalidCheckoutToken()
	}
	claims := paymentCheckoutClaims{
		Audience: paymentCheckoutAudience, OrderID: order.ID, UserID: order.UserID,
		InstanceID: strings.TrimSpace(sel.InstanceID), OrderNo: order.OutTradeNo,
		PayURLHash: checkoutURLHash(payURL), IssuedAt: now.Unix(), ExpiresAt: expiry.Unix(),
	}
	token, err := signer.createSignedToken(claims)
	if err != nil {
		return "", err
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = paymentCheckoutPath, "", "", "token="+token
	return u.String(), nil
}

func (s *PaymentService) GetPublicPaymentCheckout(ctx context.Context, token string) (*PaymentCheckoutResult, error) {
	claims, err := s.paymentResume().parseCheckoutToken(strings.TrimSpace(token))
	if err != nil {
		return nil, err
	}
	order, err := s.entClient.PaymentOrder.Get(ctx, claims.OrderID)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, invalidCheckoutToken()
		}
		return nil, fmt.Errorf("load payment checkout: %w", err)
	}
	snapshot := psOrderProviderSnapshot(order)
	if snapshot == nil || order.ProviderSnapshot["alipay_desktop_checkout"] != true ||
		order.UserID != claims.UserID || order.OutTradeNo != claims.OrderNo ||
		psStringValue(order.ProviderInstanceID) != claims.InstanceID || snapshot.ProviderInstanceID != claims.InstanceID ||
		psStringValue(order.ProviderKey) != payment.TypeAlipay || snapshot.ProviderKey != payment.TypeAlipay ||
		NormalizeVisibleMethod(order.PaymentType) != payment.TypeAlipay ||
		checkoutURLHash(psStringValue(order.PayURL)) != claims.PayURLHash || claims.ExpiresAt > order.ExpiresAt.Unix() {
		return nil, invalidCheckoutToken()
	}
	// Read persisted state only. This endpoint never creates a second payment,
	// queries Alipay, changes fulfillment, or decides success from a query string.
	result := &PaymentCheckoutResult{
		OrderID: order.ID, OutTradeNo: order.OutTradeNo, PayAmount: order.PayAmount,
		Currency: PaymentOrderCurrency(order), Status: order.Status,
		ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
	}
	if order.Status != OrderStatusPending {
		return result, nil
	}
	if !time.Now().Before(order.ExpiresAt) {
		result.Status = OrderStatusExpired
		return result, nil
	}
	// Stop handing out a checkout link if the originating merchant is disabled,
	// removed or has changed identity since the order was created.
	instance, err := s.resolveSnapshotOrderProviderInstance(ctx, order, snapshot)
	if err != nil || instance == nil || !instance.Enabled {
		return nil, infraerrors.ServiceUnavailable("CHECKOUT_UNAVAILABLE", "payment channel is unavailable")
	}
	if s.configService == nil {
		return nil, infraerrors.ServiceUnavailable("CHECKOUT_UNAVAILABLE", "payment channel is unavailable")
	}
	config, err := s.configService.decryptConfig(instance.Config)
	if err != nil || strings.TrimSpace(config["appId"]) != snapshot.MerchantAppID {
		return nil, infraerrors.ServiceUnavailable("CHECKOUT_UNAVAILABLE", "payment merchant changed")
	}
	payURL := psStringValue(order.PayURL)
	if err := validateAlipayCheckoutPayURL(payURL, order, snapshot.MerchantAppID); err != nil {
		return nil, err
	}
	result.CanPay, result.PayURL = true, payURL
	return result, nil
}
