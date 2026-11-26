const AMOUNT = /^-?(?:0|[1-9]\d{0,59})(?:\.\d{1,30})?$/;

export function formatBalanceAmount(total: string): string | undefined {
  if (!AMOUNT.test(total)) return undefined;
  const negative = total.startsWith('-');
  const body = negative ? total.slice(1) : total;
  const [whole, fraction = ''] = body.split('.');
  const padded = `${fraction}000`.slice(0, 3);
  let cents = BigInt(whole) * 100n + BigInt(padded.slice(0, 2));
  if (padded[2] >= '5') cents += 1n;
  if (cents === 0n) return '0.00';
  const units = cents / 100n;
  const minor = (cents % 100n).toString().padStart(2, '0');
  return `${negative ? '-' : ''}${units.toString()}.${minor}`;
}

export function formatBalanceText(currency?: string, total?: string): string | undefined {
  if (!currency || !/^[A-Z]{3}$/.test(currency) || typeof total !== 'string') return undefined;
  const amount = formatBalanceAmount(total);
  return amount ? `${currency} ${amount}` : undefined;
}

const DISPLAYED_BALANCE = /^([A-Z]{3}) (-?(?:0|[1-9]\d{0,59})(?:\.\d{1,30})?)$/;

const DECIMAL_AMOUNT = /^(-?)(\d+)(?:\.(\d+))?$/;

export type BalanceAmountClass = 'positive' | 'nonPositive' | 'unknown';

/**
 * Classifies decimal balance totals without floating point. Any known positive
 * amount wins, every known amount at or below zero reads as non-positive, and
 * input without a usable amount stays unknown. Missing, exponent, or malformed
 * strings are unknown and never become `0`.
 */
export function classifyBalanceAmount(
  totals: ReadonlyArray<string | undefined>
): BalanceAmountClass {
  let sawKnown = false;
  for (const total of totals) {
    if (typeof total !== 'string') continue;
    const match = DECIMAL_AMOUNT.exec(total);
    if (!match) continue;
    sawKnown = true;
    const [, sign, whole, fraction = ''] = match;
    if (/^0+$/.test(`${whole}${fraction}`)) continue;
    if (sign !== '-') return 'positive';
  }
  return sawKnown ? 'nonPositive' : 'unknown';
}

function matchDisplayedBalance(text: string): { currency: string; amount: string } | undefined {
  const match = DISPLAYED_BALANCE.exec(text);
  return match ? { currency: match[1], amount: match[2] } : undefined;
}

/** Decimal amount inside a displayed balance such as `USD -1.00`; `unlimited` has none. */
export function parseDisplayedBalanceAmount(text: string): string | undefined {
  return matchDisplayedBalance(text)?.amount;
}

export function formatBalanceDisplay(text: string): string {
  if (text === 'unlimited') return text;
  const parsed = matchDisplayedBalance(text);
  if (!parsed) return text;
  const amount = formatBalanceAmount(parsed.amount);
  return amount ? `${parsed.currency} ${amount}` : text;
}
