import type { PublicCheckoutResult } from '@/types/payment'

/** Accept the bearer capability only from the fragment, never query parameters. */
export function checkoutTokenFromHash(hash: string): string {
  const params = new URLSearchParams(hash.replace(/^#/, ''))
  const values = params.getAll('token')
  if (values.length !== 1) return ''
  const token = values[0]
  return token && token.length <= 4096 && /^[A-Za-z0-9._~-]+$/.test(token) ? token : ''
}

/** Only an HTTPS Alipay gateway supplied by the backend may be opened. */
export function alipayGatewayUrl(value: string | undefined): string {
  if (!value) return ''
  try {
    const url = new URL(value)
    if (url.protocol !== 'https:' || url.hostname !== 'openapi.alipay.com'
      || url.pathname !== '/gateway.do' || url.username || url.password || url.port || url.hash) return ''
    return url.href
  } catch {
    return ''
  }
}

export function checkoutStatus(result: PublicCheckoutResult | null, now = Date.now()): string {
  if (!result) return ''
  // Backend payment confirmation wins over local clock expiry. No URL parameter is authoritative.
  if (['PAID', 'RECHARGING', 'COMPLETED'].includes(result.status)) return 'success'
  if (result.status === 'CANCELLED') return 'cancelled'
  if (result.status === 'EXPIRED') return 'expired'
  if (result.status !== 'PENDING') return 'closed'
  const deadline = Date.parse(result.expires_at)
  if (!Number.isFinite(deadline) || deadline <= now) return 'expired'
  return 'pending'
}

export function checkoutPayUrl(result: PublicCheckoutResult | null, now = Date.now()): string {
  return result?.can_pay === true && checkoutStatus(result, now) === 'pending'
    ? alipayGatewayUrl(result.pay_url)
    : ''
}
