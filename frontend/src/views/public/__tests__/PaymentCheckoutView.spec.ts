import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const routeState = vi.hoisted(() => ({ hash: '' }))
const routerPush = vi.hoisted(() => vi.fn())
const resolvePublicCheckout = vi.hoisted(() => vi.fn())
const deviceState = vi.hoisted(() => ({ mobile: true }))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({ push: routerPush }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }),
  }
})

vi.mock('@/api/paymentCheckout', () => ({ resolvePublicCheckout }))
vi.mock('@/utils/device', () => ({ isMobileDevice: () => deviceState.mobile }))

import PaymentCheckoutView from '../PaymentCheckoutView.vue'

const payUrl = 'https://openapi.alipay.com/gateway.do?method=alipay.trade.wap.pay&sign=x'
const result = (overrides: Record<string, unknown> = {}) => ({
  order_id: 42,
  out_trade_no: 'sub2_checkout',
  pay_amount: 88,
  currency: 'CNY',
  status: 'PENDING',
  expires_at: new Date(Date.now() + 20 * 60 * 1000).toISOString(),
  can_pay: true,
  pay_url: payUrl,
  ...overrides,
})

const mountView = () => mount(PaymentCheckoutView)

describe('PaymentCheckoutView', () => {
  beforeEach(() => {
    routeState.hash = '#token=signed.token'
    deviceState.mobile = true
    routerPush.mockReset()
    resolvePublicCheckout.mockReset()
    resolvePublicCheckout.mockResolvedValue(result())
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('has no token: shows the invalid state without calling the API', async () => {
    routeState.hash = ''
    const wrapper = mountView()
    await flushPromises()
    expect(resolvePublicCheckout).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.checkout.invalid')
  })

  it('passes the fragment token to the public endpoint and renders the amount and order', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(resolvePublicCheckout).toHaveBeenCalledWith('signed.token', expect.anything())
    expect(wrapper.text()).toContain('sub2_checkout')
    expect(wrapper.text()).toContain('payment.checkout.payNow')
  })

  it('rejects an untrusted gateway link instead of navigating', async () => {
    resolvePublicCheckout.mockResolvedValue(result({ pay_url: 'https://evil.example.com/gateway.do' }))
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('button.btn-primary').exists()).toBe(false)
    expect(assign).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('payment.checkout.gatewayBlockedHint')
  })

  it('opens the Alipay gateway in the same tab on mobile', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('button.btn-primary').trigger('click')
    expect(assign).toHaveBeenCalledWith(payUrl)
  })

  it('shows the scan hint instead of the pay button on desktop', async () => {
    deviceState.mobile = false
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('payment.checkout.desktopHint')
    expect(wrapper.find('button.btn-primary').exists()).toBe(false)
  })

  it('reports a transport failure without pretending the order succeeded', async () => {
    resolvePublicCheckout.mockRejectedValue(new Error('CHECKOUT_RESPONSE_INVALID'))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('payment.checkout.loadFailed')
    expect(wrapper.text()).not.toContain('payment.checkout.success')
  })

  it('stops polling and hides the link once the order is paid', async () => {
    vi.useFakeTimers()
    resolvePublicCheckout
      .mockResolvedValueOnce(result())
      .mockResolvedValue(result({ status: 'PAID', can_pay: false, pay_url: undefined }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('payment.checkout.payNow')

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.text()).toContain('payment.checkout.success')
    expect(wrapper.text()).not.toContain('payment.checkout.payNow')

    const calls = resolvePublicCheckout.mock.calls.length
    await vi.advanceTimersByTimeAsync(20000)
    expect(resolvePublicCheckout.mock.calls.length).toBe(calls)
  })

  it('does not clear the payment link when a poll fails transiently', async () => {
    vi.useFakeTimers()
    resolvePublicCheckout
      .mockResolvedValueOnce(result())
      .mockRejectedValueOnce(new Error('network'))
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.text()).toContain('payment.checkout.payNow')
    expect(wrapper.text()).not.toContain('payment.checkout.loadFailed')
  })

  it('rejects an expired order and stops offering payment', async () => {
    resolvePublicCheckout.mockResolvedValue(result({ expires_at: new Date(Date.now() - 1000).toISOString() }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('payment.checkout.expired')
    expect(wrapper.text()).not.toContain('payment.checkout.payNow')
  })

  it('aborts the pending request when the page unmounts', async () => {
    let captured: AbortSignal | undefined
    resolvePublicCheckout.mockImplementation((_token: string, signal?: AbortSignal) => {
      captured = signal
      return new Promise(() => {})
    })
    const wrapper = mountView()
    await flushPromises()
    wrapper.unmount()
    expect(captured?.aborted).toBe(true)
  })
})
