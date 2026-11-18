//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestShouldUseAlipayDesktopCheckout(t *testing.T) {
	t.Parallel()

	alipay := func(config map[string]string) *payment.InstanceSelection {
		return &payment.InstanceSelection{InstanceID: "6", ProviderKey: payment.TypeAlipay, Config: config}
	}
	cases := []struct {
		name   string
		req    CreateOrderRequest
		sel    *payment.InstanceSelection
		expect bool
	}{
		{name: "desktop opt in", req: CreateOrderRequest{IsMobile: false}, sel: alipay(map[string]string{"desktopWapPay": "true"}), expect: true},
		{name: "normalized opt in", req: CreateOrderRequest{IsMobile: false}, sel: alipay(map[string]string{"desktopWapPay": " TRUE "}), expect: true},
		{name: "mobile keeps existing branch", req: CreateOrderRequest{IsMobile: true}, sel: alipay(map[string]string{"desktopWapPay": "true"}), expect: false},
		{name: "disabled", req: CreateOrderRequest{IsMobile: false}, sel: alipay(map[string]string{"desktopWapPay": "false"}), expect: false},
		{name: "absent", req: CreateOrderRequest{IsMobile: false}, sel: alipay(nil), expect: false},
		{name: "other provider", req: CreateOrderRequest{IsMobile: false}, sel: &payment.InstanceSelection{ProviderKey: payment.TypeEasyPay, Config: map[string]string{"desktopWapPay": "true"}}, expect: false},
		{name: "nil selection", req: CreateOrderRequest{IsMobile: false}, sel: nil, expect: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expect, shouldUseAlipayDesktopCheckout(tc.req, tc.sel))
		})
	}
}
