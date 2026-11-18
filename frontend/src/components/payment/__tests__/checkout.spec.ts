import { describe, expect, it } from 'vitest'
import {
  alipayGatewayUrl,
  checkoutPayUrl,
  checkoutStatus,
  checkoutTokenFromHash,
} from '../checkout'
import type { PublicCheckoutResult } from '@/types/payment'

const base: PublicCheckoutResult = {
  order_id: 42,
  out_trade_no: 'sub2_checkout',
  pay_amount: 88,
  currency: 'CNY',
  status: 'PENDING',
  expires_at: '2099-01-01T00:10:00.000Z',
  can_pay: true,
  pay_url: 'https://openapi.alipay.com/gateway.do?method=alipay.trade.wap.pay',
}

describe('checkoutTokenFromHash', () => {
  it('accepts exactly one well-formed token from the fragment', () => {
    expect(checkoutTokenFromHash('#token=abc.DEF-123_x~')).toBe('abc.DEF-123_x~')
    expect(checkoutTokenFromHash('#foo=1&token=abc')).toBe('abc')
  })

  it('rejects missing, duplicated, oversized, or illegal tokens', () => {
    expect(checkoutTokenFromHash('')).toBe('')
    expect(checkoutTokenFromHash('#foo=1')).toBe('')
    expect(checkoutTokenFromHash('#token=a&token=b')).toBe('')
    expect(checkoutTokenFromHash(`#token=${'a'.repeat(4097)}`)).toBe('')
    expect(checkoutTokenFromHash('#token=abc%20def')).toBe('')
  })
})

describe('alipayGatewayUrl', () => {
  it('accepts only the official HTTPS WAP gateway', () => {
    expect(alipayGatewayUrl('https://openapi.alipay.com/gateway.do?method=alipay.trade.wap.pay'))
      .toBe('https://openapi.alipay.com/gateway.do?method=alipay.trade.wap.pay')
  })

  it.each([
    'http://openapi.alipay.com/gateway.do',
    'https://evil.example.com/gateway.do',
    'https://openapi.alipay.com/other.do',
    'https://user:pass@openapi.alipay.com/gateway.do',
    'https://openapi.alipay.com:8443/gateway.do',
    'javascript:alert(1)',
    '',
  ])('rejects %s', (value) => {
    expect(alipayGatewayUrl(value)).toBe('')
  })

  it('rejects a missing value', () => {
    expect(alipayGatewayUrl(undefined)).toBe('')
  })
})

describe('checkoutStatus', () => {
  const now = Date.parse('2026-01-01T00:00:00.000Z')

  it('treats backend-confirmed payment as success even after the local deadline', () => {
    expect(checkoutStatus({ ...base, status: 'PAID', expires_at: '2025-01-01T00:00:00Z' }, now)).toBe('success')
    expect(checkoutStatus({ ...base, status: 'RECHARGING' }, now)).toBe('success')
    expect(checkoutStatus({ ...base, status: 'COMPLETED' }, now)).toBe('success')
  })

  it('maps terminal states without inventing success', () => {
    expect(checkoutStatus({ ...base, status: 'CANCELLED' }, now)).toBe('cancelled')
    expect(checkoutStatus({ ...base, status: 'EXPIRED' }, now)).toBe('expired')
    expect(checkoutStatus({ ...base, status: 'FAILED' }, now)).toBe('closed')
    expect(checkoutStatus({ ...base, status: 'PENDING', expires_at: '2025-12-31T23:59:00Z' }, now)).toBe('expired')
    expect(checkoutStatus({ ...base, status: 'PENDING' }, now)).toBe('pending')
  })

  it('returns empty for a missing result', () => {
    expect(checkoutStatus(null, now)).toBe('')
  })
})

describe('checkoutPayUrl', () => {
  const now = Date.parse('2026-01-01T00:00:00Z')

  it('only exposes a validated gateway while the order can still be paid', () => {
    expect(checkoutPayUrl(base, now)).toBe(base.pay_url)
    expect(checkoutPayUrl({ ...base, can_pay: false }, now)).toBe('')
    expect(checkoutPayUrl({ ...base, status: 'PAID' }, now)).toBe('')
    expect(checkoutPayUrl({ ...base, pay_url: 'https://evil.example.com/gateway.do' }, now)).toBe('')
    expect(checkoutPayUrl(null, now)).toBe('')
  })
})
