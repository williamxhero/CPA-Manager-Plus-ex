import { describe, expect, it, vi } from 'vitest';
import {
  formatQuotaRefreshErrorMessage,
  resolveQuotaRefreshErrorCode,
} from './quotaRefreshErrorMessage';

describe('quotaRefreshErrorMessage', () => {
  it('extracts the stable machine code prefix', () => {
    expect(resolveQuotaRefreshErrorCode('common_refresh_unavailable:503')).toBe(
      'common_refresh_unavailable'
    );
    expect(resolveQuotaRefreshErrorCode('identity_or_source_changed')).toBe(
      'identity_or_source_changed'
    );
  });

  it('localizes known opaque refresh codes', () => {
    const t = vi.fn((key: string) =>
      key === 'accounts.quota_refresh_error_common_refresh_unavailable'
        ? '额度刷新服务暂时不可用，请稍后重试'
        : ''
    );
    expect(formatQuotaRefreshErrorMessage('common_refresh_unavailable:502', t as never)).toBe(
      '额度刷新服务暂时不可用，请稍后重试'
    );
    expect(t).toHaveBeenCalledWith('accounts.quota_refresh_error_common_refresh_unavailable', {
      defaultValue: '',
    });
  });
});
