//go:build unit

package provider

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
)

func alipayFixtureKeys(t *testing.T) (privPEM, appPubBase64, otherPubBase64 string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	otherDER, err := x509.MarshalPKIXPublicKey(&other.PublicKey)
	require.NoError(t, err)

	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		base64.StdEncoding.EncodeToString(pubDER),
		base64.StdEncoding.EncodeToString(otherDER)
}

func TestNewAlipayRejectsApplicationPublicKey(t *testing.T) {
	priv, appPub, _ := alipayFixtureKeys(t)
	_, err := NewAlipay("test", map[string]string{
		"appId": "2021000000000000", "privateKey": priv, "publicKey": appPub,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ALIPAY_APP_PUBLIC_KEY_MISMATCH")
}

func TestNewAlipayRejectsApplicationPublicKeyWithPEMHeader(t *testing.T) {
	priv, appPub, _ := alipayFixtureKeys(t)
	der, err := base64.StdEncoding.DecodeString(appPub)
	require.NoError(t, err)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	_, err = NewAlipay("test", map[string]string{
		"appId": "2021000000000000", "privateKey": priv, "publicKey": pubPEM,
	})
	require.ErrorContains(t, err, "ALIPAY_APP_PUBLIC_KEY_MISMATCH")
}

func TestNewAlipayAcceptsAlipayPlatformPublicKey(t *testing.T) {
	priv, _, platformPub := alipayFixtureKeys(t)
	a, err := NewAlipay("test", map[string]string{
		"appId": "2021000000000000", "privateKey": priv, "publicKey": platformPub,
	})
	require.NoError(t, err)
	require.NotNil(t, a)
}

func TestNewAlipaySkipsKeyCheckForCertificateMode(t *testing.T) {
	priv, appPub, _ := alipayFixtureKeys(t)
	// Certificate mode is selected by any non-empty cert field; the publicKey
	// value is then irrelevant and must not block saving.
	a, err := NewAlipay("test", map[string]string{
		"appId": "2021000000000000", "privateKey": priv, "publicKey": appPub,
		"appCertPublicKey": "fixture-cert", "alipayPublicCert": "fixture-cert", "alipayRootCert": "fixture-cert",
	})
	require.NoError(t, err)
	require.NotNil(t, a)
}

func TestNewAlipayTolerantOfUnparseableKeys(t *testing.T) {
	// Legacy/odd formats must not be rejected by the safety check; the SDK still
	// validates at request time.
	_, err := NewAlipay("test", map[string]string{
		"appId": "2021000000000000", "privateKey": "not-a-key", "publicKey": "also-not-a-key",
	})
	require.NoError(t, err)
}
