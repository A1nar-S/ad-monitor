import type { Watch, WatchRule } from './api'
import { formatMoney, formatNumber, t } from './i18n'

// Small wording helpers shared by the price-watch list and editor.

// ruleText: "≤ €1,200", or "5% drop · ≤ €1,234.05".
export function ruleText(
  rule: WatchRule,
  thresholdCents: number,
  currency: string,
): string {
  const price = formatMoney(thresholdCents, currency)
  if (rule.kind === 'below') return t('watches.ruleBelow', { price })
  return t('watches.ruleDrop', {
    pct: formatNumber(rule.dropPercent, { maximumFractionDigits: 2 }),
    price,
  })
}

// intervalText: "every 30 minutes", "every 3 hours", "daily".
export function intervalText(minutes: number): string {
  if (minutes >= 1440 && minutes % 1440 === 0) {
    const n = minutes / 1440
    return n === 1 ? t('watches.daily') : t('watches.everyDays', { n })
  }
  if (minutes >= 60 && minutes % 60 === 0)
    return t('watches.everyHours', { n: minutes / 60 })
  return t('watches.everyMinutes', { n: minutes })
}

// changePercent is how far the price moved from the reference, signed.
export function changePercent(w: Watch): number | null {
  if (w.priceCents === null || !w.referenceCents) return null
  return ((w.priceCents - w.referenceCents) / w.referenceCents) * 100
}

export function formatChange(pct: number): string {
  if (Math.abs(pct) < 0.05) return '±0%'
  const sign = pct > 0 ? '+' : '−'
  return `${sign}${formatNumber(Math.abs(pct), { maximumFractionDigits: 1 })}%`
}

export function hostOf(url: string): string {
  try {
    return new URL(url).host.replace(/^www\./, '')
  } catch {
    return url
  }
}
