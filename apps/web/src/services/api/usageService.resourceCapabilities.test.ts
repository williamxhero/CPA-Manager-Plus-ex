import axios, { AxiosHeaders, type AxiosAdapter } from 'axios';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { setDemoMode } from '@/features/demo/demoMode';
import { usageServiceApi } from './usageService';

const originalAdapter = axios.defaults.adapter;
afterEach(() => {
  axios.defaults.adapter = originalAdapter;
  setDemoMode(false);
  vi.unstubAllGlobals();
});

describe('resource capability support transport', () => {
  it('reads the manager-only summary with authentication and cancellation', async () => {
    const data = { schemaVersion: 1, identity: { state: 'unknown' } };
    const adapter = vi.fn<AxiosAdapter>(async (config) => ({
      data,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }));
    axios.defaults.adapter = adapter;
    const signal = new AbortController().signal;
    await expect(
      usageServiceApi.getResourceCapabilities('http://manager.invalid/', 'test-key', signal)
    ).resolves.toEqual(data);
    expect(adapter).toHaveBeenCalledTimes(1);
    const config = adapter.mock.calls[0][0];
    expect(config.url).toBe('http://manager.invalid/usage-service/resource-capabilities');
    expect(config.method).toBe('get');
    expect(AxiosHeaders.from(config.headers).get('Authorization')).toBe('Bearer test-key');
    expect(config.signal).toBe(signal);
  });

  it('uses honest local demo fixtures without ever invoking the HTTP adapter', async () => {
    setDemoMode(true);
    const adapter = vi.fn<AxiosAdapter>(async () => {
      throw new Error('network forbidden');
    });
    axios.defaults.adapter = adapter;
    await expect(
      usageServiceApi.getResourceCapabilities('http://forbidden.invalid', 'secret')
    ).resolves.toMatchObject({
      schemaVersion: 1,
      identity: { state: 'unknown', reason: 'core_contract_unverified' },
      query: { state: 'unknown', reason: 'query_contract_unverified' },
      independentGate: { state: 'unavailable', reason: 'consumer_contract_unconfigured' },
      policy: { source: expect.any(String) },
    });
    expect(adapter).not.toHaveBeenCalled();
  });

  it('requires both the demo build and demo mode guards', async () => {
    vi.stubGlobal('__DEMO_SITE__', false);
    setDemoMode(true);
    const adapter = vi.fn<AxiosAdapter>(async (config) => ({
      data: {},
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }));
    axios.defaults.adapter = adapter;
    await usageServiceApi.getResourceCapabilities('http://manager.invalid', 'test-key');
    expect(adapter).toHaveBeenCalledTimes(1);
  });
});
