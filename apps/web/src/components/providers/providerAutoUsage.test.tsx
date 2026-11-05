import { useSyncExternalStore } from 'react';
import { act, create } from 'react-test-renderer';
import { beforeEach, expect, it, vi } from 'vitest';
import {
  readProviderAutoUsage,
  SERVER_PROVIDER_AUTO_USAGE,
  subscribeProviderAutoUsage,
  writeProviderAutoUsage,
} from './providerAutoUsage';
import { readRuntimeControls, setRuntimeControlsForTests } from '@/utils/quota/runtimeControls';

beforeEach(() => {
  setRuntimeControlsForTests({
    autoStartStopEnabled: false,
    autoStartStopIds: [],
    quotaFetchedAtMs: 0,
  });
});

it('returns one snapshot object for unchanged auto start/stop state', () => {
  expect(readProviderAutoUsage()).toBe(readProviderAutoUsage());
  expect(SERVER_PROVIDER_AUTO_USAGE).toBe(SERVER_PROVIDER_AUTO_USAGE);
});

it('toggles auto start/stop once without a nested update loop', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify(readRuntimeControls()), { status: 200 }))
  );
  let renders = 0;
  function Probe() {
    const usage = useSyncExternalStore(
      subscribeProviderAutoUsage,
      readProviderAutoUsage,
      () => SERVER_PROVIDER_AUTO_USAGE
    );
    renders += 1;
    return (
      <button type="button" onClick={() => writeProviderAutoUsage({ ...usage, enabled: !usage.enabled })}>
        {usage.enabled ? 'on' : 'off'}
      </button>
    );
  }
  let renderer: ReturnType<typeof create> | undefined;
  await act(async () => {
    renderer = create(<Probe />);
  });
  const button = renderer?.root.findByType('button');
  await act(async () => {
    button?.props.onClick();
    await Promise.resolve();
    await Promise.resolve();
  });
  expect(renderer?.root.findByType('button').children).toEqual(['on']);
  expect(renders).toBeLessThan(10);
  expect(readRuntimeControls().autoStartStopEnabled).toBe(true);
  vi.unstubAllGlobals();
});

it('drops secret-shaped ids before they are stored', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init?: RequestInit) => new Response(String(init?.body ?? '{}'), { status: 200 }))
  );
  await act(async () => {
    writeProviderAutoUsage({
      enabled: true,
      ids: ['openai:068a7783-00cf-40e3-a900-ced04834afbe', 'claude:sk-secret'],
    });
    await Promise.resolve();
  });
  expect(readRuntimeControls().autoStartStopIds).toEqual(['openai:068a7783-00cf-40e3-a900-ced04834afbe']);
  vi.unstubAllGlobals();
});
