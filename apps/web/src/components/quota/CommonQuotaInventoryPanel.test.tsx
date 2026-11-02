import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { CommonQuotaInventoryPanel } from './CommonQuotaInventoryPanel';
afterEach(() => vi.unstubAllGlobals());
const inventory = (id: string) => ({
  sources: [
    { id, request: { kind: 'credential_quota' }, targets: [], state: 'ready', stale: false },
  ],
});
const completedRound = {
  id: 'manual-round',
  state: 'complete',
  pending: 0,
  succeeded: 1,
  failed: 0,
};
it.each(['success', 'failure'])(
  'keeps refreshed inventory and progress after a late initial %s',
  async (outcome) => {
    let finishInitial!: (response: Response) => void;
    let failInitial!: (error: Error) => void;
    const initial = new Promise<Response>((resolve, reject) => {
      finishInitial = resolve;
      failInitial = reject;
    });
    let reads = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, options?: RequestInit) => {
        if (options?.method === 'POST') {
          return Response.json(completedRound);
        }
        return ++reads === 1 ? initial : Response.json(inventory('new-inventory'));
      })
    );
    let renderer!: ReactTestRenderer;
    try {
      await act(async () => {
        renderer = create(<CommonQuotaInventoryPanel base="http://fixture" managementKey="test" />);
      });
      await act(async () => renderer.root.findByType('button').props.onClick());
      expect(JSON.stringify(renderer.toJSON())).toContain('new-inventory');
      await act(async () => {
        if (outcome === 'success') finishInitial(Response.json(inventory('old-inventory')));
        else failInitial(new Error('late bootstrap failure'));
      });
      const visible = JSON.stringify(renderer.toJSON());
      expect(visible).toContain('new-inventory');
      expect(visible).toContain('complete');
      expect(visible).not.toContain('old-inventory');
      expect(visible).not.toContain('common_inventory_unavailable');
    } finally {
      await act(async () => renderer.unmount());
    }
  }
);

it('invalidates the old connection operation without unlocking a newer refresh', async () => {
  let finishOld!: (response: Response) => void;
  let finishNew!: (response: Response) => void;
  const oldJob = new Promise<Response>((resolve) => {
    finishOld = resolve;
  });
  const newJob = new Promise<Response>((resolve) => {
    finishNew = resolve;
  });
  const request = vi.fn(async (url: string, options?: RequestInit) => {
    const oldConnection = url.startsWith('http://first/');
    if (options?.method === 'POST') return oldConnection ? oldJob : newJob;
    return Response.json(inventory(oldConnection ? 'first-source' : 'second-source'));
  });
  vi.stubGlobal('fetch', request);
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(<CommonQuotaInventoryPanel base="http://first" managementKey="test" />);
    });
    await act(async () => renderer.root.findByType('button').props.onClick());
    await act(async () => {
      renderer.update(<CommonQuotaInventoryPanel base="http://second" managementKey="test" />);
    });
    await act(async () => renderer.root.findByType('button').props.onClick());
    await act(async () => finishOld(Response.json(completedRound)));
    expect(request.mock.calls.filter(([url]) => url.startsWith('http://first/'))).toHaveLength(2);
    expect(renderer.root.findByType('button').props.disabled).toBe(true);
    expect(JSON.stringify(renderer.toJSON())).toContain('second-source');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('first-source');
    await act(async () => finishNew(Response.json(completedRound)));
    expect(renderer.root.findByType('button').props.disabled).toBeFalsy();
    expect(JSON.stringify(renderer.toJSON())).toContain('complete');
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('does not continue a manual refresh after unmount', async () => {
  let finishJob!: (response: Response) => void;
  const job = new Promise<Response>((resolve) => {
    finishJob = resolve;
  });
  const request = vi.fn(async (_url: string, options?: RequestInit) =>
    options?.method === 'POST' ? job : Response.json(inventory('retained-source'))
  );
  vi.stubGlobal('fetch', request);
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(<CommonQuotaInventoryPanel base="http://fixture" managementKey="test" />);
  });
  await act(async () => renderer.root.findByType('button').props.onClick());
  await act(async () => renderer.unmount());
  await act(async () => finishJob(Response.json(completedRound)));
  expect(request).toHaveBeenCalledTimes(2);
});

it.each(['failure', 'empty'])(
  'retains stale content on failure and replaces successful empty snapshots: %s',
  async (outcome) => {
    let reads = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, options?: RequestInit) => {
        if (options?.method === 'POST') {
          if (outcome === 'failure') throw new Error('offline');
          return Response.json(completedRound);
        }
        return Response.json(++reads === 1 ? inventory('retained-source') : { sources: [] });
      })
    );
    let renderer!: ReactTestRenderer;
    try {
      await act(async () => {
        renderer = create(<CommonQuotaInventoryPanel base="http://fixture" managementKey="test" />);
      });
      expect(JSON.stringify(renderer.toJSON())).toContain('retained-source');
      await act(async () => renderer.root.findByType('button').props.onClick());
      const visible = JSON.stringify(renderer.toJSON());
      if (outcome === 'failure') {
        expect(visible).toContain('retained-source');
        expect(visible).toContain('common_refresh_unavailable');
      } else {
        expect(visible).not.toContain('retained-source');
        expect(visible).toContain('complete');
      }
      expect(renderer.root.findByType('button').props.disabled).toBeFalsy();
    } finally {
      await act(async () => renderer.unmount());
    }
  }
);
it('separates native selected-key quota from wallet money and keeps unavailable unknown, not zero', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({
        sources: [
          {
            id: 'key',
            request: { kind: 'official_balance' },
            targets: [],
            state: 'ready',
            stale: false,
            lastSuccess: {
              semantic: 'key_remaining',
              nativeQuota: { unit: 'newapi_quota', state: 'finite', remaining: '123' },
            },
          },
          {
            id: 'wallet',
            request: { kind: 'official_balance' },
            targets: [],
            state: 'ready',
            stale: true,
            lastSuccess: { semantic: 'account_wallet', currency: 'USD', amountDecimal: '-2.50' },
          },
          {
            id: 'unknown',
            request: { kind: 'unavailable' },
            targets: [],
            state: 'unsupported',
            reason: 'no_protocol',
            stale: false,
          },
        ],
      })
    )
  );
  let r!: ReactTestRenderer;
  await act(async () => {
    r = create(<CommonQuotaInventoryPanel base="http://fixture" managementKey="test" />);
  });
  const text = JSON.stringify(r.toJSON());
  expect(text).toContain('123');
  expect(text).toContain('key_remaining');
  expect(text).toContain('-2.50');
  expect(text).toContain('account_wallet');
  expect(text).toContain('unknown');
  expect(text).not.toContain('USD 0');
  await act(async () => r.unmount());
});
