//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAlipayDesktopWapPayConfigRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}
	config := validAlipayProviderConfig(t)
	config["desktopWapPay"] = "true"
	instance, err := svc.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey: payment.TypeAlipay, Name: "desktop-wap", Enabled: true,
		SupportedTypes: []string{payment.TypeAlipay}, Config: config, PaymentMode: "redirect",
	})
	require.NoError(t, err)

	// UI must be able to read the non-secret switch while merchant secrets stay omitted.
	masked, err := svc.decryptAndMaskConfig(payment.TypeAlipay, instance.Config)
	require.NoError(t, err)
	require.Equal(t, "true", masked["desktopWapPay"])
	require.NotContains(t, masked, "privateKey")

	// Changing the checkout product only affects new orders. It must not require
	// rewriting/cancelling pending orders or modifying merchant verification keys.
	createPendingProviderConfigOrder(t, ctx, client, instance)
	for _, value := range []string{"false", "true", "false"} {
		instance, err = svc.UpdateProviderInstance(ctx, instance.ID, UpdateProviderInstanceRequest{
			Config: map[string]string{"desktopWapPay": value, "privateKey": ""},
		})
		require.NoError(t, err)
		stored, err := svc.decryptConfig(instance.Config)
		require.NoError(t, err)
		require.Equal(t, value, stored["desktopWapPay"])
		require.Equal(t, config["privateKey"], stored["privateKey"])
		require.Equal(t, config["appId"], stored["appId"])
		require.Equal(t, config["notifyUrl"], stored["notifyUrl"])
		require.Equal(t, config["returnUrl"], stored["returnUrl"])
		require.Equal(t, "redirect", instance.PaymentMode)
	}

	// A partial admin save that omits the switch must retain its previous value.
	newName := "desktop-wap-renamed"
	instance, err = svc.UpdateProviderInstance(ctx, instance.ID, UpdateProviderInstanceRequest{Name: &newName})
	require.NoError(t, err)
	stored, err := svc.decryptConfig(instance.Config)
	require.NoError(t, err)
	require.Equal(t, "false", stored["desktopWapPay"])

	count, err := svc.countPendingOrders(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestAlipayDesktopWapPayInvalidConfigRejectedAtSave(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}
	instance, err := svc.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey: payment.TypeAlipay, Name: "desktop-wap-validation", Enabled: true,
		SupportedTypes: []string{payment.TypeAlipay}, Config: validAlipayProviderConfig(t),
	})
	require.NoError(t, err)
	_, err = svc.UpdateProviderInstance(ctx, instance.ID, UpdateProviderInstanceRequest{
		Config: map[string]string{"desktopWapPay": "wap"},
	})
	require.Error(t, err)
	require.Equal(t, "INVALID_ALIPAY_DESKTOP_WAP_PAY", infraerrors.Reason(err))
	stored, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, instance.Config, stored.Config)
}
