import type { Money } from '@modura/pos-sdk';

const formatters = new Map<string, Intl.NumberFormat>();

/**
 * Formats integer minor units using the currency's own exponent (ADR-0006), so USD
 * divides by 100 while a zero-decimal currency like JPY divides by 1.
 */
export function formatMoney({ amountMinor, currency }: Money): string {
  let formatter = formatters.get(currency);
  if (formatter === undefined) {
    formatter = new Intl.NumberFormat('en-US', { style: 'currency', currency });
    formatters.set(currency, formatter);
  }
  const exponent = formatter.resolvedOptions().maximumFractionDigits ?? 2;
  return formatter.format(amountMinor / 10 ** exponent);
}

export function multiply(money: Money, quantity: number): Money {
  return { amountMinor: money.amountMinor * quantity, currency: money.currency };
}
