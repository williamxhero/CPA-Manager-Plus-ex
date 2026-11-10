import { describe, expect, it } from 'vitest';
import { buildAccountRows, sortAccountRows, type AccountRow } from './accountRows';

const [base] = buildAccountRows([{ name: 'base.json', type: 'codex' }], {
  antigravityQuota: {},
  claudeQuota: {},
  codexQuota: {},
  kimiQuota: {},
  devinQuota: {},
  metaQuota: {},
  xaiQuota: {},
});

const account = (name: string, remaining: number | null, disabled = false): AccountRow => ({
  ...base,
  selectionKey: name,
  fileName: `${name}.json`,
  accountLabel: name,
  disabled,
  quota: {
    ...base.quota,
    status: remaining === 0 ? 'exhausted' : 'unknown',
    remainingPercent: remaining,
  },
});

describe('default credential display order', () => {
  it('places enabled credentials first, then remaining quota descending and name ascending', () => {
    const input = [
      account('Bravo disabled', 100, true),
      account('Spent', 0),
      account('zulu', 90),
      account('Unknown', null),
      account('Low', 10),
      account('alpha', 90),
      account('Alpha disabled', 0, true),
    ];
    expect(sortAccountRows(input).map((row) => row.accountLabel)).toEqual([
      'alpha',
      'zulu',
      'Low',
      'Unknown',
      'Spent',
      'Alpha disabled',
      'Bravo disabled',
    ]);
    expect(input[0].accountLabel).toBe('Bravo disabled');
  });

  it('sorts disabled credentials by displayed name regardless of quota, provider or errors', () => {
    const alpha = account('Alpha', 0, true);
    alpha.fileName = 'z-file.json';
    alpha.provider = 'qwen';
    const zulu = account('Zulu', 100, true);
    zulu.fileName = 'a-file.json';
    zulu.quota.status = 'error';
    expect(sortAccountRows([zulu, alpha]).map((row) => row.accountLabel)).toEqual([
      'Alpha',
      'Zulu',
    ]);
  });

  it('uses the quota shown by the credential service ahead of an older local summary', () => {
    const hm = account('OAI-HM', 0);
    const wxh = account('OAI-WXH', 80);
    const displayedQuota = new Map([
      ['OAI-HM', 93],
      ['OAI-WXH', 30],
    ]);
    expect(
      sortAccountRows([wxh, hm], undefined, displayedQuota).map((row) => row.accountLabel)
    ).toEqual(['OAI-HM', 'OAI-WXH']);
    displayedQuota.set('OAI-HM', 10);
    expect(
      sortAccountRows([wxh, hm], undefined, displayedQuota).map((row) => row.accountLabel)
    ).toEqual(['OAI-WXH', 'OAI-HM']);
  });
});
