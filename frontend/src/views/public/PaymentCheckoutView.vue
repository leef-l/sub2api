<template>
  <div class="flex min-h-screen items-center justify-center bg-gray-50 px-4 py-10 dark:bg-dark-950">
    <div class="w-full max-w-md space-y-4">
      <div class="card overflow-hidden">
        <div class="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <div class="flex items-center gap-2">
            <img :src="alipayIcon" alt="Alipay" class="h-5 w-5" />
            <h1 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('payment.checkout.title') }}</h1>
          </div>
          <span
            v-if="status === 'pending'"
            class="rounded-full bg-blue-50 px-2.5 py-1 text-xs font-medium text-blue-600 dark:bg-blue-950/40 dark:text-blue-300"
          >{{ t('payment.checkout.statusPending') }}</span>
        </div>

        <div class="space-y-5 px-5 py-6">
          <!-- Invalid capability link -->
          <div v-if="state === 'invalid'" class="space-y-3 text-center">
            <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.checkout.invalid') }}</p>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.checkout.invalidHint') }}</p>
          </div>

          <!-- Transport failure, never treated as payment state -->
          <div v-else-if="state === 'error'" class="space-y-3 text-center">
            <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.checkout.loadFailed') }}</p>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.checkout.loadFailedHint') }}</p>
            <button type="button" class="btn btn-primary w-full" @click="load">{{ t('payment.checkout.retry') }}</button>
          </div>

          <div v-else-if="state === 'loading'" class="flex flex-col items-center gap-3 py-6">
            <div class="h-8 w-8 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</p>
          </div>

          <!-- Paid -->
          <div v-else-if="status === 'success'" class="space-y-3 text-center">
            <div class="mx-auto flex h-14 w-14 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30">
              <svg class="h-7 w-7 text-green-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
              </svg>
            </div>
            <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.checkout.success') }}</p>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.checkout.successHint') }}</p>
          </div>

          <div v-else-if="status === 'expired'" class="space-y-2 text-center">
            <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.checkout.expired') }}</p>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.checkout.expiredHint') }}</p>
          </div>

          <div v-else-if="status === 'cancelled'" class="space-y-2 text-center">
            <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.checkout.cancelled') }}</p>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.checkout.cancelledHint') }}</p>
          </div>

          <div v-else-if="status === 'closed'" class="space-y-2 text-center">
            <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('payment.checkout.closed') }}</p>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('payment.checkout.closedHint') }}</p>
          </div>

          <!-- Payable order -->
          <template v-else-if="state === 'ready' && status === 'pending' && result">
            <div class="space-y-2 rounded-xl bg-gray-50 px-4 py-3 text-sm dark:bg-dark-800">
              <div class="flex items-start justify-between gap-4">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.checkout.amountLabel') }}</span>
                <span class="text-lg font-bold text-gray-900 dark:text-white">{{ displayAmount }}</span>
              </div>
              <div class="flex items-start justify-between gap-4 border-t border-gray-200 pt-2 dark:border-dark-600">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.checkout.orderLabel') }}</span>
                <span class="max-w-[70%] break-all text-right font-mono text-xs text-gray-900 dark:text-white">{{ result.out_trade_no }}</span>
              </div>
              <div class="flex items-start justify-between gap-4 border-t border-gray-200 pt-2 dark:border-dark-600">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.checkout.expiresIn') }}</span>
                <span class="font-semibold tabular-nums text-gray-900 dark:text-white">{{ countdownDisplay }}</span>
              </div>
            </div>

            <p class="text-center text-xs text-gray-400 dark:text-gray-500">{{ t('payment.checkout.waiting') }}</p>

            <div v-if="isMobile" class="space-y-2">
              <p class="text-center text-sm leading-6 text-gray-600 dark:text-gray-300">{{ t('payment.checkout.mobileHint') }}</p>
              <button v-if="payUrl" type="button" class="btn btn-primary w-full py-3" @click="openAlipay">
                {{ t('payment.checkout.payNow') }}
              </button>
              <p class="text-center text-xs text-gray-400 dark:text-gray-500">{{ t('payment.checkout.mobileButtonHint') }}</p>
            </div>
            <div v-else class="space-y-3 text-center">
              <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">{{ t('payment.checkout.desktopHint') }}</p>
              <button v-if="payUrl" type="button" class="btn btn-secondary w-full text-sm" @click="openAlipay">
                {{ t('payment.checkout.payNow') }}
              </button>
            </div>

            <p v-if="blockedGateway" class="rounded-lg bg-red-50 px-3 py-2 text-center text-xs leading-5 text-red-600 dark:bg-red-950/30 dark:text-red-300">
              {{ t('payment.checkout.gatewayBlockedHint') }}
            </p>
          </template>
        </div>
      </div>

      <button type="button" class="btn btn-secondary w-full" @click="goHome">{{ t('payment.checkout.backHome') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { resolvePublicCheckout } from '@/api/paymentCheckout'
import { alipayGatewayUrl, checkoutPayUrl, checkoutStatus, checkoutTokenFromHash } from '@/components/payment/checkout'
import { formatPaymentAmount } from '@/components/payment/currency'
import { isMobileDevice } from '@/utils/device'
import type { PublicCheckoutResult } from '@/types/payment'
import alipayIcon from '@/assets/icons/alipay.svg'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()

const state = ref<'loading' | 'ready' | 'invalid' | 'error'>('loading')
const result = ref<PublicCheckoutResult | null>(null)
const blockedGateway = ref(false)
const remainingSeconds = ref(0)
const isMobile = isMobileDevice()

let countdownTimer: ReturnType<typeof setInterval> | null = null
let pollTimer: ReturnType<typeof setInterval> | null = null
let controller: AbortController | null = null
let inFlight = false
let disposed = false

const status = computed(() => checkoutStatus(result.value))
const payUrl = computed(() => {
  const raw = checkoutPayUrl(result.value)
  if (result.value?.pay_url && !raw) blockedGateway.value = true
  return raw
})
const displayAmount = computed(() => result.value
  ? formatPaymentAmount(result.value.pay_amount, result.value.currency, String(locale.value))
  : '')
const countdownDisplay = computed(() => {
  const m = Math.floor(remainingSeconds.value / 60)
  const s = remainingSeconds.value % 60
  return `${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`
})

function stopTimers() {
  if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null }
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}

function startCountdown(expiresAt: string) {
  const deadline = Date.parse(expiresAt)
  const tick = () => {
    remainingSeconds.value = Number.isFinite(deadline)
      ? Math.max(0, Math.floor((deadline - Date.now()) / 1000))
      : 0
  }
  tick()
  if (countdownTimer) clearInterval(countdownTimer)
  countdownTimer = setInterval(tick, 1000)
}

async function load() {
  if (inFlight) return
  const token = checkoutTokenFromHash(route.hash)
  if (!token) {
    state.value = 'invalid'
    return
  }
  inFlight = true
  controller?.abort()
  controller = new AbortController()
  try {
    const data = await resolvePublicCheckout(token, controller.signal)
    if (disposed) return
    result.value = data
    state.value = 'ready'
    startCountdown(data.expires_at)
    const settled = ['success', 'expired', 'cancelled', 'closed'].includes(checkoutStatus(data))
    if (settled) stopTimers()
    else if (!pollTimer) pollTimer = setInterval(poll, 5000)
  } catch (err) {
    if (disposed) return
    if (err instanceof DOMException && err.name === 'AbortError') return
    // Invalid or expired capability links are a permanent client error.
    state.value = /CHECKOUT_RESOLVE_FAILED/.test(String(err)) && result.value === null && state.value === 'loading'
      ? 'invalid'
      : 'error'
  } finally {
    inFlight = false
  }
}

async function poll() {
  if (disposed || inFlight) return
  const token = checkoutTokenFromHash(route.hash)
  if (!token) return
  inFlight = true
  try {
    const data = await resolvePublicCheckout(token, controller?.signal)
    if (disposed) return
    result.value = data
    if (['success', 'expired', 'cancelled', 'closed'].includes(checkoutStatus(data))) stopTimers()
  } catch {
    // A transient polling failure never invents a payment result.
  } finally {
    inFlight = false
  }
}

function openAlipay() {
  const url = alipayGatewayUrl(payUrl.value)
  if (!url) {
    blockedGateway.value = true
    return
  }
  window.location.assign(url)
}

function goHome() {
  router.push('/')
}

onMounted(load)
onUnmounted(() => {
  disposed = true
  stopTimers()
  controller?.abort()
})
</script>
