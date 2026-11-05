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

export function formatBalanceDisplay(text: string): string {
  if (text === 'unlimited') return text;
  const match = DISPLAYED_BALANCE.exec(text);
  if (!match) return text;
  const amount = formatBalanceAmount(match[2]);
  return amount ? `${match[1]} ${amount}` : text;
}
