import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import PaymentProviderDialog from '@/components/payment/PaymentProviderDialog.vue'
import { STRIPE_SDK_API_VERSION } from '@/components/payment/providerConfig'
import type { ProviderInstance } from '@/types/payment'

const messages: Record<string, string> = {
  'admin.settings.payment.providerConfig': 'Credentials',
  'admin.settings.payment.easypayCustomMethods': 'Custom EasyPay methods',
  'admin.settings.payment.easypayCustomMethodsHint': 'Add provider-specific EasyPay type values.',
  'admin.settings.payment.addCustomMethod': 'Add method',
  'admin.settings.payment.customMethodType': 'Payment type',
  'admin.settings.payment.customMethodUpstreamType': 'Upstream type',
  'admin.settings.payment.customMethodDisplayName': 'Display name',
  'admin.settings.payment.customMethodDisplayNamePlaceholder': '信用卡',
  'admin.settings.payment.paymentGuideTrigger': 'View payment guide',
  'admin.settings.payment.alipayGuideSummary': 'Desktop WAP can override QR/redirect; mobile keeps its existing flow.',
  'admin.settings.payment.alipayDesktopWapPay': 'Use WAP payment on desktop',
  'admin.settings.payment.alipayDesktopWapPayHint': 'Only new desktop orders use WAP ahead of QR/redirect; mobile stays unchanged. Checkout depends on Alipay compatibility.',
  'admin.settings.payment.alipayDesktopWapPayModeHint': 'Payment mode is kept and restored when desktop WAP is off.',
  'admin.settings.payment.modeQRCode': 'QR Code',
  'admin.settings.payment.modeRedirect': 'Redirect',
  'admin.settings.payment.wxpayGuideSummary': 'Desktop prefers Native QR; mobile routes to JSAPI or H5 based on browser context.',
  'admin.settings.payment.airwallexGuideSummary': 'Use Payment Acceptance read/write only.',
  'admin.settings.payment.stripeWebhookHint': 'Configure Stripe webhook.',
  'admin.settings.payment.stripeWebhookApiVersionHint': 'Use Stripe API version {version}.',
  'admin.settings.payment.airwallexWebhookHint': 'Select payment_intent.succeeded and use the latest stable API version.',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, string>) => {
      const message = messages[key] ?? key
      if (!params) return message
      return Object.entries(params).reduce(
        (value, [name, replacement]) => value.replaceAll(`{${name}}`, replacement),
        message,
      )
    },
    te: (key: string) => key in messages,
  }),
}))

function providerFactory(overrides: Partial<ProviderInstance> = {}): ProviderInstance {
  return {
    id: 1,
    provider_key: 'airwallex',
    name: 'Airwallex',
    config: {},
    supported_types: ['airwallex'],
    enabled: true,
    payment_mode: '',
    refund_enabled: false,
    allow_user_refund: false,
    limits: '',
    sort_order: 0,
    ...overrides,
  }
}

function mountDialog(options: { editing?: ProviderInstance | null } = {}) {
  return mount(PaymentProviderDialog, {
    props: {
      show: true,
      saving: false,
      editing: options.editing ?? null,
      allKeyOptions: [
        { value: 'easypay', label: 'EasyPay' },
        { value: 'bepusdt', label: 'BEpusdt' },
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
        { value: 'stripe', label: 'Stripe' },
        { value: 'airwallex', label: 'Airwallex' },
      ],
      enabledKeyOptions: [
        { value: 'easypay', label: 'EasyPay' },
        { value: 'bepusdt', label: 'BEpusdt' },
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
        { value: 'airwallex', label: 'Airwallex' },
      ],
      allPaymentTypes: [
        { value: 'alipay', label: 'Alipay' },
        { value: 'wxpay', label: 'WeChat Pay' },
      ],
      redirectLabel: 'Redirect',
    },
    global: {
      stubs: {
        BaseDialog: {
          template: '<div><slot /><slot name="footer" /></div>',
        },
        Select: {
          name: 'Select',
          props: ['modelValue', 'options', 'disabled'],
          template: '<div />',
        },
      },
    },
  })
}

describe('PaymentProviderDialog callback URLs', () => {
  it.each([
    ['https://notify.example.com/', 'https://return.example.com///', 'https://notify.example.com', 'https://return.example.com'],
    [' https://notify.example.com/sub/ ', ' https://return.example.com/site/ ', 'https://notify.example.com/sub', 'https://return.example.com/site'],
    ['https://notify.example.com', 'https://return.example.com', 'https://notify.example.com', 'https://return.example.com'],
    ['', '', window.location.origin, window.location.origin],
  ])('joins callback paths to %s and %s', async (notify, returnUrl, expectedNotify, expectedReturn) => {
    const provider = providerFactory({
      provider_key: 'easypay', name: 'EasyPay',
      config: { pid: 'pid-1', apiBase: 'https://pay.example.com' },
      supported_types: ['alipay'], payment_mode: 'qrcode',
    })
    const wrapper = mountDialog({ editing: provider })
    ;(wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
    await nextTick()
    const bases = wrapper.findAll('input').filter(input => input.classes().includes('!rounded-r-none'))
    await bases[0].setValue(notify)
    await bases[1].setValue(returnUrl)
    await wrapper.find('form').trigger('submit')
    const payload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string> }
    expect(payload.config.notifyUrl).toBe(expectedNotify + '/api/v1/payment/webhook/easypay')
    expect(payload.config.returnUrl).toBe(expectedReturn + '/payment/result')
    wrapper.unmount()
  })
})

describe('PaymentProviderDialog EasyPay refund protocol', () => {
  it.each([undefined, '1', '0'])('loads and saves the refund success code %s', async (storedCode) => {
    const provider = providerFactory({
      provider_key: 'easypay', name: 'EasyPay', supported_types: ['alipay'],
      config: {
        pid: 'pid-1', apiBase: 'https://pay.example.com',
        ...(storedCode === undefined ? {} : { refundSuccessCode: storedCode }),
      },
    })
    const wrapper = mountDialog({ editing: provider })
    ;(wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
    await nextTick()

    const selector = wrapper.findAllComponents({ name: 'Select' }).find(select =>
      select.props('options').some((option: { value: string }) => option.value === '0'),
    )
    expect(selector, 'refund success code selector').toBeDefined()
    expect(selector!.props('modelValue')).toBe(storedCode ?? '1')
    const selectedCode = storedCode === '0' ? '1' : '0'
    selector!.vm.$emit('update:modelValue', selectedCode)
    await wrapper.find('form').trigger('submit')

    const payload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string> }
    expect(payload.config.refundSuccessCode).toBe(selectedCode)
    wrapper.unmount()
  })
})

describe('PaymentProviderDialog desktop Alipay WAP', () => {
  const wapSwitchSelector = 'button[role="switch"][aria-label="Use WAP payment on desktop"]'

  function loadProvider(wrapper: ReturnType<typeof mountDialog>, provider: ProviderInstance) {
    (wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
  }

  function resetProvider(wrapper: ReturnType<typeof mountDialog>, key: string) {
    (wrapper.vm as unknown as { reset: (key: string) => void }).reset(key)
  }

  it.each([
    [undefined, 'false'],
    ['', 'false'],
    ['false', 'false'],
    ['true', 'true'],
    [' TRUE ', 'true'],
  ])('loads %s and saves the explicit boolean string %s', async (storedValue, expectedValue) => {
    const provider = providerFactory({
      provider_key: 'alipay', name: 'Alipay', supported_types: ['alipay'], payment_mode: 'redirect',
      config: { appId: 'app-1', ...(storedValue === undefined ? {} : { desktopWapPay: storedValue }) },
    })
    const wrapper = mountDialog({ editing: provider })
    loadProvider(wrapper, provider)
    await nextTick()

    const toggle = wrapper.get(wapSwitchSelector)
    expect(toggle.attributes('aria-checked')).toBe(expectedValue)
    expect(toggle.attributes('aria-describedby')).toBe('alipay-desktop-wap-hint')
    expect(wrapper.get('#alipay-desktop-wap-hint').text()).toBe(messages['admin.settings.payment.alipayDesktopWapPayHint'])
    await wrapper.get('form').trigger('submit')

    const payload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string>; payment_mode: string }
    expect(payload.config.desktopWapPay).toBe(expectedValue)
    expect(payload.payment_mode).toBe('redirect')
    wrapper.unmount()
  })

  it.each(['', 'redirect'])('saves true then false and preserves the previous PC mode %s', async (paymentMode) => {
    const provider = providerFactory({
      provider_key: 'alipay', name: 'Alipay', supported_types: ['alipay'], payment_mode: paymentMode,
      config: {
        appId: 'app-1', desktopWapPay: 'false',
        appCertPublicKey: 'fixture-app-cert', alipayPublicCert: 'fixture-alipay-cert', alipayRootCert: 'fixture-root-cert',
      },
    })
    const wrapper = mountDialog({ editing: provider })
    loadProvider(wrapper, provider)
    await nextTick()
    const modeButtons = wrapper.findAll('button[aria-pressed]')
    const selectedMode = paymentMode === 'redirect' ? 'Redirect' : 'QR Code'
    expect(modeButtons).toHaveLength(2)
    expect(wrapper.get('button[aria-pressed="true"]').text()).toBe(selectedMode)

    await wrapper.get(wapSwitchSelector).trigger('click')
    expect(modeButtons.every(button => (button.element as HTMLButtonElement).disabled)).toBe(true)
    expect(modeButtons.every(button => button.attributes('aria-describedby') === 'alipay-desktop-wap-mode-hint')).toBe(true)
    expect(wrapper.get('#alipay-desktop-wap-mode-hint').text()).toBe(messages['admin.settings.payment.alipayDesktopWapPayModeHint'])
    await modeButtons.find(button => button.text() !== selectedMode)!.trigger('click')
    expect(wrapper.get('button[aria-pressed="true"]').text()).toBe(selectedMode)
    await wrapper.get('form').trigger('submit')

    const enabledPayload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string>; payment_mode: string }
    expect(enabledPayload.config).toMatchObject({ ...provider.config, desktopWapPay: 'true' })
    expect(enabledPayload.payment_mode).toBe(paymentMode)

    await wrapper.get(wapSwitchSelector).trigger('click')
    expect(modeButtons.every(button => !(button.element as HTMLButtonElement).disabled)).toBe(true)
    expect(wrapper.find('#alipay-desktop-wap-mode-hint').exists()).toBe(false)
    expect(wrapper.get('button[aria-pressed="true"]').text()).toBe(selectedMode)
    await wrapper.get('form').trigger('submit')

    const disabledPayload = wrapper.emitted('save')?.[1]?.[0] as { config: Record<string, string>; payment_mode: string }
    expect(disabledPayload.config.desktopWapPay).toBe('false')
    expect(disabledPayload.payment_mode).toBe(paymentMode)
    wrapper.unmount()
  })

  it('defaults a new provider to false and resets an enabled edit without leaking its config', async () => {
    const provider = providerFactory({
      provider_key: 'alipay', name: 'Alipay', supported_types: ['alipay'], payment_mode: 'redirect',
      config: { appId: 'old-app', desktopWapPay: 'true', alipayRootCert: 'old-cert' },
    })
    const wrapper = mountDialog({ editing: provider })
    loadProvider(wrapper, provider)
    await nextTick()
    expect(wrapper.get(wapSwitchSelector).attributes('aria-checked')).toBe('true')

    await wrapper.setProps({ editing: null })
    resetProvider(wrapper, 'alipay')
    await nextTick()
    expect(wrapper.get(wapSwitchSelector).attributes('aria-checked')).toBe('false')
    expect(wrapper.get('button[aria-pressed="true"]').text()).toBe('QR Code')
    const textInputs = wrapper.findAll('input[type="text"]')
    expect((textInputs[0].element as HTMLInputElement).value).toBe('')
    expect((textInputs[1].element as HTMLInputElement).value).toBe('')
    await textInputs[0].setValue('New Alipay')
    await textInputs[1].setValue('new-app')
    await wrapper.findAll('textarea')[0].setValue('fixture-private-key')
    await wrapper.get('form').trigger('submit')

    const payload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string>; payment_mode: string }
    expect(payload.config.desktopWapPay).toBe('false')
    expect(payload.config.appId).toBe('new-app')
    expect(payload.config.alipayRootCert).toBeUndefined()
    expect(payload.payment_mode).toBe('')
    wrapper.unmount()
  })

  it('clears the override on provider-type changes and preserves BEpusdt custom channels', async () => {
    const wrapper = mountDialog()
    resetProvider(wrapper, 'alipay')
    await nextTick()
    await wrapper.get(wapSwitchSelector).trigger('click')
    const selector = wrapper.findComponent({ name: 'Select' })

    selector.vm.$emit('update:modelValue', 'bepusdt')
    selector.vm.$emit('change', 'bepusdt')
    await nextTick()
    expect(wrapper.find(wapSwitchSelector).exists()).toBe(false)
    expect(wrapper.get('button[aria-pressed="true"]').text()).toBe('Redirect')
    const providerInputs = wrapper.findAll('input[type="text"]')
    await providerInputs[0].setValue('BEpusdt')
    await providerInputs[1].setValue('fixture-pid')
    await providerInputs[2].setValue('https://pay.example.com')
    await wrapper.get('input[type="password"]').setValue('fixture-key')
    await wrapper.get('button.btn-sm').trigger('click')
    const methodInputs = wrapper.findAll('input[placeholder="usdt_trc20"]')
    await methodInputs[0].setValue('usdt_trc20')
    await methodInputs[1].setValue('usdt.trc20')
    await wrapper.get('input[placeholder="信用卡"]').setValue('USDT')
    await wrapper.get('form').trigger('submit')

    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      config: Record<string, string>; supported_types: string[]; payment_mode: string
    }
    expect(payload.config.desktopWapPay).toBeUndefined()
    expect(payload.config.currency).toBe('USD')
    expect(payload.payment_mode).toBe('redirect')
    expect(payload.supported_types).toEqual(['usdt_trc20'])
    expect(JSON.parse(payload.config.customMethods)).toEqual([
      { type: 'usdt_trc20', upstreamType: 'usdt.trc20', displayName: 'USDT' },
    ])

    selector.vm.$emit('update:modelValue', 'alipay')
    selector.vm.$emit('change', 'alipay')
    await nextTick()
    expect(wrapper.get(wapSwitchSelector).attributes('aria-checked')).toBe('false')
    expect(wrapper.get('button[aria-pressed="true"]').text()).toBe('QR Code')
    wrapper.unmount()
  })

  it('does not leak an enabled override when loading another provider instance', async () => {
    const enabled = providerFactory({
      provider_key: 'alipay', name: 'Alipay 1', supported_types: ['alipay'],
      config: { appId: 'app-1', desktopWapPay: 'true' },
    })
    const legacy = providerFactory({
      id: 2, provider_key: 'alipay', name: 'Alipay 2', supported_types: ['alipay'],
      config: { appId: 'app-2' },
    })
    const wrapper = mountDialog({ editing: enabled })
    loadProvider(wrapper, enabled)
    await nextTick()
    expect(wrapper.get(wapSwitchSelector).attributes('aria-checked')).toBe('true')
    await wrapper.setProps({ editing: legacy })
    loadProvider(wrapper, legacy)
    await nextTick()
    expect(wrapper.get(wapSwitchSelector).attributes('aria-checked')).toBe('false')
    await wrapper.get('form').trigger('submit')
    const payload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string> }
    expect(payload.config.desktopWapPay).toBe('false')
    expect(payload.config.appId).toBe('app-2')
    wrapper.unmount()
  })

  it.each(['easypay', 'bepusdt', 'wxpay', 'stripe', 'airwallex'])('hides the Alipay-only switch for %s', async (key) => {
    const wrapper = mountDialog()
    resetProvider(wrapper, key)
    await nextTick()
    expect(wrapper.find(wapSwitchSelector).exists()).toBe(false)
    expect(wrapper.find('#alipay-desktop-wap-hint').exists()).toBe(false)
    wrapper.unmount()
  })
})

describe('PaymentProviderDialog payment guide', () => {
  it('shows no payment guide for providers without a flow guide', () => {
    const wrapper = mountDialog()

    expect(wrapper.text()).not.toContain(messages['admin.settings.payment.alipayGuideSummary'])
    expect(wrapper.text()).not.toContain(messages['admin.settings.payment.wxpayGuideSummary'])
    expect(wrapper.find('button[title="View payment guide"]').exists()).toBe(false)
  })

  it.each([
    ['alipay', 'admin.settings.payment.alipayGuideSummary'],
    ['wxpay', 'admin.settings.payment.wxpayGuideSummary'],
    ['airwallex', 'admin.settings.payment.airwallexGuideSummary'],
  ])('shows the payment guide summary for %s', async (providerKey, summaryKey) => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset(providerKey)
    await nextTick()

    expect(wrapper.text()).toContain(messages[summaryKey])
    expect(wrapper.find('button[title="View payment guide"]').exists()).toBe(true)
  })

  it('shows Airwallex webhook event and API version guidance with the webhook URL', async () => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset('airwallex')
    await nextTick()

    expect(wrapper.text()).toContain(messages['admin.settings.payment.airwallexWebhookHint'])
    expect(wrapper.text()).toContain('/api/v1/payment/webhook/airwallex')
  })

  it('shows Stripe webhook API version guidance with the integrated SDK version', async () => {
    const wrapper = mountDialog()

    ;(wrapper.vm as unknown as { reset: (key: string) => void }).reset('stripe')
    await nextTick()

    expect(wrapper.text()).toContain(messages['admin.settings.payment.stripeWebhookHint'])
    expect(wrapper.text()).toContain(`Use Stripe API version ${STRIPE_SDK_API_VERSION}.`)
    expect(wrapper.text()).toContain('/api/v1/payment/webhook/stripe')
  })

  it('emits an empty Airwallex accountId when the admin clears it', async () => {
    const provider = providerFactory({
      config: {
        clientId: 'cid_123',
        apiBase: 'https://api.airwallex.com/api/v1',
        countryCode: 'CN',
        currency: 'CNY',
        accountId: 'acct_123',
      },
    })
    const wrapper = mountDialog({ editing: provider })

    ;(wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
    await nextTick()

    const accountIdInput = wrapper
      .findAll('input[type="text"]')
      .find(input => (input.element as HTMLInputElement).value === 'acct_123')
    if (!accountIdInput) throw new Error('accountId input not found')

    await accountIdInput.setValue('')
    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as { config: Record<string, string> }
    expect(payload.config.accountId).toBe('')
  })

  it.each(['epay', 'usdt.trc20'])('serializes EasyPay upstream type %s and adds the local type to supported_types', async (upstreamType) => {
    const provider = providerFactory({
      provider_key: 'easypay',
      name: 'EasyPay',
      config: {
        pid: 'pid-1',
        apiBase: 'https://pay.example.com',
        notifyUrl: 'https://example.com/api/v1/payment/webhook/easypay',
        returnUrl: 'https://example.com/payment/result',
      },
      supported_types: ['alipay', 'wxpay'],
      payment_mode: 'qrcode',
    })
    const wrapper = mountDialog({ editing: provider })

    ;(wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
    await nextTick()

    await wrapper.find('button.btn-sm').trigger('click')
    await nextTick()

    const inputs = wrapper.findAll('input[type="text"]')
    const customTypeInputs = inputs.filter(input => (input.element as HTMLInputElement).placeholder === 'credit_card')
    const ldcTypeInput = customTypeInputs[0]
    const upstreamTypeInput = customTypeInputs[1]
    const displayNameInput = inputs.find(input => (input.element as HTMLInputElement).placeholder === '信用卡')
    if (!ldcTypeInput || !upstreamTypeInput || !displayNameInput) {
      throw new Error('custom method inputs not found')
    }

    await ldcTypeInput.setValue('ldc')
    await upstreamTypeInput.setValue(upstreamType)
    await displayNameInput.setValue('LDC')
    await wrapper.find('form').trigger('submit.prevent')

    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      config: Record<string, string>
      supported_types: string[]
    }
    expect(JSON.parse(payload.config.customMethods)).toEqual([{ type: 'ldc', upstreamType, displayName: 'LDC' }])
    expect(payload.supported_types).toEqual(['alipay', 'wxpay', 'ldc'])
  })

  it.each([
    ['alipay_hk', 'hkpay'],
    ['usdt.trc20', 'usdt.trc20'],
    ['usdt_trc20', 'usdt/trc20'],
  ])('rejects invalid EasyPay mapping %s to %s', async (type, upstreamType) => {
    const provider = providerFactory({
      provider_key: 'easypay',
      name: 'EasyPay',
      config: {
        pid: 'pid-1',
        apiBase: 'https://pay.example.com',
        notifyUrl: 'https://example.com/api/v1/payment/webhook/easypay',
        returnUrl: 'https://example.com/payment/result',
      },
      supported_types: ['alipay', 'wxpay'],
      payment_mode: 'qrcode',
    })
    const wrapper = mountDialog({ editing: provider })

    ;(wrapper.vm as unknown as { loadProvider: (provider: ProviderInstance) => void }).loadProvider(provider)
    await nextTick()

    await wrapper.find('button.btn-sm').trigger('click')
    await nextTick()

    const inputs = wrapper.findAll('input[type="text"]')
    const customTypeInputs = inputs.filter(input => (input.element as HTMLInputElement).placeholder === 'credit_card')
    const typeInput = customTypeInputs[0]
    const upstreamTypeInput = customTypeInputs[1]
    const displayNameInput = inputs.find(input => (input.element as HTMLInputElement).placeholder === '信用卡')
    if (!typeInput || !upstreamTypeInput || !displayNameInput) {
      throw new Error('custom method inputs not found')
    }

    await typeInput.setValue(type)
    await upstreamTypeInput.setValue(upstreamType)
    await displayNameInput.setValue('Custom payment')
    await wrapper.find('form').trigger('submit.prevent')

    expect(wrapper.emitted('save')).toBeUndefined()
  })
})
