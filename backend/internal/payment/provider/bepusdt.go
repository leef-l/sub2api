// Package provider contains concrete payment provider implementations.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

const (
	bepusdtCodeSuccess     = 200
	bepusdtStatusPending   = "1" // Order created; awaiting payment.
	bepusdtStatusPaid      = "2" // Payment received successfully.
	bepusdtStatusClosed    = "3" // Order expired or cancelled.
	bepusdtHTTPTimeout     = 10 * time.Second
	bepusdtAmountTolerance = 0.005
)

// Bepusdt implements payment.Provider for the self-hosted BEpusdt gateway.
// Order submission uses EasyPay signing; order queries use BEpusdt's JSON API.
type Bepusdt struct {
	instanceID string
	config     map[string]string
	httpClient *http.Client
}

// NewBepusdt creates a new Bepusdt provider.
// config keys: pid, pkey, apiBase, notifyUrl, returnUrl, currency, cid, cidAlipay, cidWxpay.
func NewBepusdt(instanceID string, config map[string]string) (*Bepusdt, error) {
	for _, k := range []string{"pid", "pkey", "apiBase", "notifyUrl", "returnUrl"} {
		if strings.TrimSpace(config[k]) == "" {
			return nil, fmt.Errorf("bepusdt config missing required key: %s", k)
		}
	}
	cfg := make(map[string]string, len(config))
	for k, v := range config {
		cfg[k] = v
	}
	currency, err := bepusdtCurrency(cfg["currency"])
	if err != nil {
		return nil, err
	}
	cfg["currency"] = currency
	cfg["apiBase"] = normalizeBepusdtAPIBase(cfg["apiBase"])
	return &Bepusdt{
		instanceID: instanceID,
		config:     cfg,
		httpClient: &http.Client{Timeout: bepusdtHTTPTimeout, CheckRedirect: bepusdtStopRedirect},
	}, nil
}

func bepusdtCurrency(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "USD"
	}
	currency, err := payment.NormalizePaymentCurrency(raw)
	if err != nil {
		return "", fmt.Errorf("bepusdt currency: %w", err)
	}
	return currency, nil
}

func normalizeBepusdtAPIBase(apiBase string) string {
	base := strings.TrimSpace(apiBase)
	if base == "" {
		return ""
	}
	if parsed, err := url.Parse(base); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.RawPath = ""
		parsed.Path = trimBepusdtEndpointPath(parsed.Path)
		return strings.TrimRight(parsed.String(), "/")
	}
	return strings.TrimRight(trimBepusdtEndpointPath(base), "/")
}

func trimBepusdtEndpointPath(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	lower := strings.ToLower(path)
	for _, endpoint := range []string{"/submit.php", "/api/v1/pay/info"} {
		if strings.HasSuffix(lower, endpoint) {
			return strings.TrimRight(path[:len(path)-len(endpoint)], "/")
		}
	}
	return path
}

func (e *Bepusdt) apiBase() string {
	if e == nil {
		return ""
	}
	return normalizeBepusdtAPIBase(e.config["apiBase"])
}

func (e *Bepusdt) Name() string        { return "Bepusdt" }
func (e *Bepusdt) ProviderKey() string { return payment.TypeBepusdt }
func (e *Bepusdt) SupportedTypes() []payment.PaymentType {
	types := []payment.PaymentType{payment.TypeAlipay, payment.TypeWxpay}
	for _, method := range e.customMethods() {
		if method.Type != "" {
			types = append(types, method.Type)
		}
	}
	return types
}

func (e *Bepusdt) MerchantIdentityMetadata() map[string]string {
	if e == nil {
		return nil
	}
	pid := strings.TrimSpace(e.config["pid"])
	if pid == "" {
		return nil
	}
	return map[string]string{"pid": pid}
}

func (e *Bepusdt) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	if mode := e.config["paymentMode"]; mode == paymentModePopup || mode == "redirect" {
		return e.createRedirectPayment(req)
	}
	return e.createAPIPayment(ctx, req)
}

func (e *Bepusdt) submitParams(req payment.CreatePaymentRequest) (url.Values, error) {
	currency, err := bepusdtCurrency(e.config["currency"])
	if err != nil {
		return nil, err
	}
	notifyURL, returnURL := e.resolveURLs(req)
	paymentType := e.upstreamPaymentType(req.PaymentType)
	params := map[string]string{
		"pid": e.config["pid"], "type": paymentType,
		"out_trade_no": req.OrderID, "notify_url": notifyURL,
		"return_url": returnURL, "name": req.Subject,
		"money": req.Amount, "fiat": currency,
	}
	if cid := e.resolveCID(paymentType); cid != "" {
		params["cid"] = cid
	}
	if req.ClientIP != "" {
		params["clientip"] = req.ClientIP
	}
	if req.IsMobile {
		params["device"] = deviceMobile
	}
	// fiat must be present before signing: the gateway's default may be CNY.
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	return form, nil
}

// createRedirectPayment leaves submission to the browser. The upstream trade_id
// becomes available only after the browser follows the signed submit URL.
func (e *Bepusdt) createRedirectPayment(req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	params, err := e.submitParams(req)
	if err != nil {
		return nil, err
	}
	return &payment.CreatePaymentResponse{
		PayURL:   e.apiBase() + "/submit.php?" + params.Encode(),
		Currency: params.Get("fiat"),
	}, nil
}

// createAPIPayment captures the checkout redirect instead of following it.
// Both PayURL and QRCode contain the absolute checkout URL; TradeNo is persisted
// by the caller and must be supplied to QueryOrder as the upstream trade_id.
func (e *Bepusdt) createAPIPayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	params, err := e.submitParams(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.apiBase()+"/submit.php", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("bepusdt create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bepusdt create: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxEasypayResponseSize))
		if readErr != nil {
			return nil, fmt.Errorf("bepusdt create response: %w", readErr)
		}
		return nil, fmt.Errorf("bepusdt create expected HTTP 302, got HTTP %d: %s", resp.StatusCode, summarizeEasyPayResponse(body))
	}
	payURL, err := resolveBepusdtReturnedRef(e.apiBase(), resp.Header.Get("Location"))
	if err != nil {
		return nil, fmt.Errorf("bepusdt create Location: %w", err)
	}
	tradeID, err := bepusdtCheckoutTradeID(payURL)
	if err != nil {
		return nil, fmt.Errorf("bepusdt create: %w", err)
	}
	return &payment.CreatePaymentResponse{
		TradeNo: tradeID, PayURL: payURL, QRCode: payURL, Currency: params.Get("fiat"),
	}, nil
}

func resolveBepusdtReturnedRef(apiBase, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("missing checkout URL")
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(apiBase), "/") + "/")
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return "", fmt.Errorf("invalid apiBase")
	}
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", fmt.Errorf("invalid checkout URL: %w", err)
	}
	resolved := base.ResolveReference(parsed)
	if resolved.Host == "" || (resolved.Scheme != "http" && resolved.Scheme != "https") {
		return "", fmt.Errorf("checkout URL must use HTTP or HTTPS")
	}
	return resolved.String(), nil
}

func bepusdtCheckoutTradeID(checkoutURL string) (string, error) {
	parsed, err := url.Parse(checkoutURL)
	if err != nil {
		return "", fmt.Errorf("invalid checkout URL: %w", err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 3 || parts[len(parts)-3] != "pay" || parts[len(parts)-2] != "checkout" {
		return "", fmt.Errorf("checkout URL missing trade_id")
	}
	return bepusdtQueryTradeID(parts[len(parts)-1])
}

func bepusdtQueryTradeID(tradeNo string) (string, error) {
	tradeID := strings.TrimSpace(tradeNo)
	if tradeID == "" {
		return "", fmt.Errorf("missing trade_id; use the TradeNo returned by BEpusdt checkout")
	}
	if strings.Contains(tradeID, "/") {
		return bepusdtCheckoutTradeID(tradeID)
	}
	if strings.ContainsAny(tradeID, " \t\r\n?&#=") {
		return "", fmt.Errorf("invalid trade_id")
	}
	return tradeID, nil
}

func (e *Bepusdt) resolveURLs(req payment.CreatePaymentRequest) (string, string) {
	notifyURL := req.NotifyURL
	if notifyURL == "" {
		notifyURL = e.config["notifyUrl"]
	}
	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = e.config["returnUrl"]
	}
	return notifyURL, returnURL
}

func (e *Bepusdt) customMethods() []easyPayCustomMethod {
	if e == nil {
		return nil
	}
	raw := strings.TrimSpace(e.config["customMethods"])
	if raw == "" {
		return nil
	}
	var methods []easyPayCustomMethod
	if err := json.Unmarshal([]byte(raw), &methods); err != nil {
		return nil
	}
	result := make([]easyPayCustomMethod, 0, len(methods))
	for _, method := range methods {
		method.Type = strings.TrimSpace(method.Type)
		method.UpstreamType = strings.TrimSpace(method.UpstreamType)
		method.DisplayName = strings.TrimSpace(method.DisplayName)
		if method.Type == "" || method.UpstreamType == "" {
			continue
		}
		result = append(result, method)
	}
	return result
}

func (e *Bepusdt) upstreamPaymentType(paymentType string) string {
	paymentType = strings.TrimSpace(paymentType)
	for _, method := range e.customMethods() {
		if paymentType == method.Type {
			return method.UpstreamType
		}
	}
	return paymentType
}

type bepusdtQueryResult struct {
	tradeID      string
	orderID      string
	status       string
	money        string
	actualAmount string
	currency     string
	token        string
}

func (e *Bepusdt) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	result, err := e.queryOrder(ctx, tradeNo)
	if err != nil {
		return nil, err
	}
	amount, err := bepusdtParseAmount(result.actualAmount)
	if err != nil {
		return nil, fmt.Errorf("bepusdt query actual_amount: %w", err)
	}
	metadata := e.MerchantIdentityMetadata()
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["currency"] = result.currency
	metadata["order_id"] = result.orderID
	metadata["money"] = result.money
	metadata["actual_amount"] = result.actualAmount
	return &payment.QueryOrderResponse{
		TradeNo: result.tradeID, Status: result.status, Amount: amount, Metadata: metadata,
	}, nil
}

// queryOrder accepts an upstream trade_id (or its checkout URL), never a
// merchant order_id. The caller obtains it from the persisted creation result.
func (e *Bepusdt) queryOrder(ctx context.Context, tradeNo string) (*bepusdtQueryResult, error) {
	tradeID, err := bepusdtQueryTradeID(tradeNo)
	if err != nil {
		return nil, fmt.Errorf("bepusdt query: %w", err)
	}
	payload, err := json.Marshal(map[string]string{"trade_id": tradeID})
	if err != nil {
		return nil, fmt.Errorf("bepusdt query encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.apiBase()+"/api/v1/pay/info", strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("bepusdt query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.do(req)
	if err != nil {
		return nil, fmt.Errorf("bepusdt query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEasypayResponseSize))
	if err != nil {
		return nil, fmt.Errorf("bepusdt query response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bepusdt query HTTP %d: %s", resp.StatusCode, summarizeEasyPayResponse(body))
	}
	var result struct {
		StatusCode int    `json:"status_code"`
		Message    string `json:"message"`
		Data       struct {
			TradeID      string          `json:"trade_id"`
			OrderID      string          `json:"order_id"`
			Money        json.RawMessage `json:"money"`
			ActualAmount json.RawMessage `json:"actual_amount"`
			Fiat         string          `json:"fiat"`
			Status       json.RawMessage `json:"status"`
			Token        string          `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("bepusdt parse query: %w", err)
	}
	// BEpusdt reports application errors, including missing orders, over HTTP 200.
	if result.StatusCode != bepusdtCodeSuccess {
		return nil, fmt.Errorf("bepusdt query failed (status_code %d): %s", result.StatusCode, result.Message)
	}
	if result.Data.TradeID == "" {
		return nil, fmt.Errorf("bepusdt query response missing trade_id")
	}
	if result.Data.TradeID != tradeID {
		return nil, fmt.Errorf("bepusdt query trade_id mismatch: expected %s, got %s", tradeID, result.Data.TradeID)
	}
	if strings.TrimSpace(result.Data.Fiat) == "" {
		return nil, fmt.Errorf("bepusdt query response missing fiat")
	}
	currency, err := payment.NormalizePaymentCurrency(result.Data.Fiat)
	if err != nil {
		return nil, fmt.Errorf("bepusdt query fiat: %w", err)
	}
	return &bepusdtQueryResult{
		tradeID: result.Data.TradeID, orderID: result.Data.OrderID,
		status: bepusdtProviderStatus(easyPayJSONScalar(result.Data.Status)),
		money:  easyPayJSONScalar(result.Data.Money), actualAmount: easyPayJSONScalar(result.Data.ActualAmount),
		currency: currency, token: result.Data.Token,
	}, nil
}

func bepusdtProviderStatus(status string) string {
	switch status {
	case bepusdtStatusPaid:
		return payment.ProviderStatusPaid
	case bepusdtStatusClosed:
		// QueryOrderResponse has no closed status; failed is its terminal state.
		return payment.ProviderStatusFailed
	case bepusdtStatusPending:
		return payment.ProviderStatusPending
	default:
		return payment.ProviderStatusPending
	}
}

func bepusdtParseAmount(raw string) (float64, error) {
	amount, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return 0, fmt.Errorf("invalid amount %q", raw)
	}
	return amount, nil
}

// ConfirmNotification binds the signed callback to the upstream order identity,
// merchant amount, actual payment amount and currency before fulfillment.
func (e *Bepusdt) ConfirmNotification(ctx context.Context, n *payment.PaymentNotification) error {
	if n == nil || n.Status != payment.NotificationStatusSuccess {
		return nil
	}
	result, err := e.queryOrder(ctx, n.TradeNo)
	if err != nil {
		return fmt.Errorf("bepusdt confirm: %w", err)
	}
	if result.orderID == "" || result.orderID != n.OrderID {
		return fmt.Errorf("bepusdt confirm: order_id mismatch: notified %s, upstream %s", n.OrderID, result.orderID)
	}
	if result.status != payment.ProviderStatusPaid {
		return fmt.Errorf("bepusdt confirm: upstream reports order %s as not paid", n.OrderID)
	}
	// amount/money is the merchant's fiat amount. actual_amount may include fees.
	queriedAmount, err := bepusdtParseAmount(result.money)
	if err != nil || math.IsNaN(n.Amount) || math.IsInf(n.Amount, 0) || math.Abs(queriedAmount-n.Amount) > bepusdtAmountTolerance {
		return fmt.Errorf("bepusdt confirm: amount mismatch: notified %v, upstream %q", n.Amount, result.money)
	}
	actualAmount, err := bepusdtParseAmount(result.actualAmount)
	if err != nil {
		return fmt.Errorf("bepusdt confirm actual_amount: %w", err)
	}
	if notified, ok := n.Metadata["actual_amount"]; ok {
		amount, err := bepusdtParseAmount(notified)
		if err != nil || math.Abs(actualAmount-amount) > bepusdtAmountTolerance {
			return fmt.Errorf("bepusdt confirm: actual_amount mismatch")
		}
	}
	currency, err := bepusdtCurrency(e.config["currency"])
	if err != nil {
		return err
	}
	if result.currency != currency || (n.Metadata["currency"] != "" && n.Metadata["currency"] != result.currency) {
		return fmt.Errorf("bepusdt confirm: fiat mismatch: expected %s, upstream %s", currency, result.currency)
	}
	if token := n.Metadata["token"]; token != "" && result.token != "" && token != result.token {
		return fmt.Errorf("bepusdt confirm: token mismatch")
	}
	n.TradeNo = result.tradeID
	return nil
}

// Only BEpusdt callback fields are allowed. In particular, signed submission
// fields (return_url, notify_url, fiat on submission, etc.) cannot be replayed as
// payment notifications. No callback value may contain signing delimiters.
var bepusdtNotifyAllowedParams = map[string]bool{
	"trade_id": true, "order_id": true, "amount": true, "actual_amount": true,
	"status": true, "token": true, "sign": true, "sign_type": true,
}

func (e *Bepusdt) VerifyNotification(_ context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	values, err := url.ParseQuery(rawBody)
	if err != nil {
		return nil, fmt.Errorf("parse notify: %w", err)
	}
	params := make(map[string]string, len(values))
	for k, entries := range values {
		if !bepusdtNotifyAllowedParams[k] {
			return nil, fmt.Errorf("unexpected notify param: %s", k)
		}
		if len(entries) != 1 {
			return nil, fmt.Errorf("duplicate notify param: %s", k)
		}
		if strings.ContainsAny(entries[0], "&=") {
			return nil, fmt.Errorf("invalid notify param value: %s", k)
		}
		params[k] = entries[0]
	}
	if params["sign"] == "" {
		return nil, fmt.Errorf("missing sign")
	}
	if params["sign_type"] != "" && params["sign_type"] != signTypeMD5 {
		return nil, fmt.Errorf("unsupported notify sign_type")
	}
	if !easyPayVerifySign(params, e.config["pkey"], params["sign"]) {
		return nil, fmt.Errorf("invalid signature")
	}
	for _, key := range []string{"trade_id", "order_id", "amount", "actual_amount", "status", "token"} {
		if strings.TrimSpace(params[key]) == "" {
			return nil, fmt.Errorf("missing notify param: %s", key)
		}
	}
	amount, err := bepusdtParseAmount(params["amount"])
	if err != nil || amount <= 0 {
		return nil, fmt.Errorf("invalid notify amount")
	}
	actualAmount, err := bepusdtParseAmount(params["actual_amount"])
	if err != nil || actualAmount <= 0 {
		return nil, fmt.Errorf("invalid notify actual_amount")
	}
	currency, err := bepusdtCurrency(e.config["currency"])
	if err != nil {
		return nil, err
	}
	metadata := e.MerchantIdentityMetadata()
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["currency"] = currency
	metadata["actual_amount"] = params["actual_amount"]
	metadata["token"] = params["token"]
	status := payment.ProviderStatusFailed
	if params["status"] == bepusdtStatusPaid {
		status = payment.ProviderStatusSuccess
	}
	return &payment.PaymentNotification{
		TradeNo: params["trade_id"], OrderID: params["order_id"], Amount: amount,
		Status: status, RawData: rawBody, Metadata: metadata,
	}, nil
}

// BEpusdt exposes no refund API; callers must not record a fabricated success.
func (e *Bepusdt) Refund(_ context.Context, _ payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, fmt.Errorf("bepusdt refund: BEpusdt does not support refunds: %w", errors.ErrUnsupported)
}

func (e *Bepusdt) resolveCID(paymentType string) string {
	if strings.HasPrefix(paymentType, "alipay") {
		if v := e.config["cidAlipay"]; v != "" {
			return v
		}
		return e.config["cid"]
	}
	if v := e.config["cidWxpay"]; v != "" {
		return v
	}
	return e.config["cid"]
}

func bepusdtStopRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func (e *Bepusdt) do(req *http.Request) (*http.Response, error) {
	client := http.Client{Timeout: bepusdtHTTPTimeout}
	if e.httpClient != nil {
		client = *e.httpClient
	}
	// Copy the client so injected/shared clients also stop at the redirect,
	// without mutating another request's CheckRedirect policy.
	client.CheckRedirect = bepusdtStopRedirect
	return client.Do(req)
}
