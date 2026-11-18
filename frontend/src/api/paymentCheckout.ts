import { buildApiUrl } from './url'
import type { PublicCheckoutResult } from '@/types/payment'

/** Deliberately independent of apiClient: no JWT, cookies, refresh flow, or auth redirect. */
export async function resolvePublicCheckout(checkoutToken: string, signal?: AbortSignal): Promise<PublicCheckoutResult> {
  const response = await fetch(buildApiUrl('/payment/public/checkout/resolve'), {
    method: 'POST',
    credentials: 'omit',
    cache: 'no-store',
    referrerPolicy: 'no-referrer',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ checkout_token: checkoutToken }),
    signal,
  })
  if (!response.ok) throw new Error('CHECKOUT_RESOLVE_FAILED')
  const envelope = await response.json()
  const data = envelope?.data
  if ((envelope.code != null && envelope.code !== 0) || !data
    || !Number.isSafeInteger(data.order_id) || data.order_id <= 0
    || typeof data.out_trade_no !== 'string'
    || typeof data.pay_amount !== 'number' || !Number.isFinite(data.pay_amount) || data.pay_amount < 0
    || typeof data.currency !== 'string' || !/^[A-Z]{3}$/.test(data.currency)
    || typeof data.status !== 'string' || typeof data.expires_at !== 'string'
    || !Number.isFinite(Date.parse(data.expires_at)) || typeof data.can_pay !== 'boolean'
    || (data.pay_url != null && typeof data.pay_url !== 'string')) {
    throw new Error('CHECKOUT_RESPONSE_INVALID')
  }
  return data as PublicCheckoutResult
}
