import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { CredentialQuotaPanel } from './CredentialQuotaPanel';

afterEach(() => vi.unstubAllGlobals());
it('reads persisted windows without querying or downloading credentials', async () => {
  const request = vi.fn(
    async (_url: string) =>
      new Response(
        JSON.stringify({
          protocol: 'opencode-go-usage',
          state: 'ready',
          version: 1,
          stale: true,
          attempt: { queryStatus: 'auth_error' },
          lastSuccess: {
            observedAtMs: 1793500000000,
            windowsJson: JSON.stringify({
              complete: false,
              accountProvenance: 'unverified',
              recoveryEligible: false,
              windows: [
                {
                  name: 'rolling',
                  usedPercent: 42,
                  remainingFraction: 0,
                  sourceStatus: 'rate-limited',
                },
              ],
            }),
          },
        }),
        { status: 200 }
      )
  );
  vi.stubGlobal('fetch', request);
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(
      <CredentialQuotaPanel
        base="http://manager"
        managementKey="test"
        authIndex="opaque-current-index"
      />
    );
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('rate-limited');
  expect(JSON.stringify(renderer.toJSON())).toContain('stale');
  expect(JSON.stringify(renderer.toJSON())).toContain('auth_error');
  expect(JSON.stringify(renderer.toJSON())).toContain('42');
  expect(request).toHaveBeenCalledTimes(1);
  expect(request.mock.calls[0]?.[0]).toContain('/usage-service/credential-quota?authIndex=');
  await act(async () => renderer.unmount());
});
