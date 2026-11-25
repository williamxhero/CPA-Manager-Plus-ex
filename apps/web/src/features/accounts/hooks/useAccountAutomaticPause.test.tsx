import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useAccountAutomaticPause } from './useAccountAutomaticPause';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface ProbeProps {
  base?: string;
  managementKey?: string;
  authIndex?: string;
  openToken: number;
}

function Probe(props: ProbeProps) {
  const read = useAccountAutomaticPause(
    props.base,
    props.managementKey,
    props.authIndex,
    props.openToken
  );
  return (
    <span
      data-kind={read.kind}
      data-subject={read.kind === 'ready' ? read.status.subjectId : ''}
      data-applied={read.kind === 'ready' ? String(read.status.applied === true) : ''}
    />
  );
}

const readOf = (renderer: ReactTestRenderer) => renderer.root.findByType('span').props;

const policyStatus = (overrides: Record<string, unknown> = {}) => ({
  subjectId: 'subject-1',
  bindingCurrent: true,
  enabled: true,
  generation: 1,
  state: 'armed',
  reason: 'policy_enabled',
  availability: { state: 'available', reason: 'current_source_gate_ready' },
  applied: false,
  ...overrides,
});

type Call = { url: string; signal?: AbortSignal; settle: (status: Record<string, unknown>) => void };

const stubDeferredPolicyFetch = () => {
  const calls: Call[] = [];
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (!url.includes('/usage-service/credential-policy?authIndex=')) {
      return Promise.resolve(Response.json({}));
    }
    return new Promise<Response>((resolve) => {
      calls.push({
        url,
        signal: init?.signal ?? undefined,
        settle: (status) => resolve(Response.json(status)),
      });
    });
  });
  vi.stubGlobal('fetch', fetchMock);
  return { calls, fetchMock };
};

const policyCalls = (calls: Call[]) => calls.filter((call) => call.url.includes('authIndex='));

const renderProbe = async (props: ProbeProps) => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(<Probe {...props} />);
  });
  return renderer;
};

const updateProbe = async (renderer: ReactTestRenderer, props: ProbeProps) => {
  await act(async () => {
    renderer.update(<Probe {...props} />);
  });
};

const settle = async (call: Call, status: Record<string, unknown>) => {
  await act(async () => {
    call.settle(status);
    await Promise.resolve();
    await Promise.resolve();
  });
};

const OPEN = {
  base: 'http://manager.local:18317',
  managementKey: 'manager-key',
  authIndex: 'auth-1',
  openToken: 1,
};

describe('useAccountAutomaticPause', () => {
  beforeEach(() => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('reads nothing without an identity and reports a missing identity', async () => {
    const { calls } = stubDeferredPolicyFetch();
    const renderer = await renderProbe({ base: 'http://manager.local:18317', openToken: 1 });
    expect(policyCalls(calls)).toHaveLength(0);
    expect(readOf(renderer)['data-kind']).toBe('missing');
    renderer.unmount();
  });

  it('reads once per open and keeps that answer while the tabs switch', async () => {
    const { calls } = stubDeferredPolicyFetch();
    const renderer = await renderProbe(OPEN);
    expect(policyCalls(calls)).toHaveLength(1);
    expect(readOf(renderer)['data-kind']).toBe('reading');
    await settle(policyCalls(calls)[0], policyStatus());
    expect(readOf(renderer)['data-kind']).toBe('ready');

    // A tab switch re-renders the drawer without starting a new open.
    await updateProbe(renderer, OPEN);
    expect(policyCalls(calls)).toHaveLength(1);
    expect(readOf(renderer)['data-kind']).toBe('ready');
    renderer.unmount();
  });

  it('abandons the answer on close and reads again on reopen', async () => {
    const { calls } = stubDeferredPolicyFetch();
    const renderer = await renderProbe(OPEN);
    await settle(policyCalls(calls)[0], policyStatus());
    expect(readOf(renderer)['data-kind']).toBe('ready');

    await updateProbe(renderer, { ...OPEN, authIndex: undefined });
    expect(readOf(renderer)['data-kind']).toBe('missing');

    await updateProbe(renderer, { ...OPEN, openToken: 2 });
    expect(policyCalls(calls)).toHaveLength(2);
    expect(readOf(renderer)['data-kind']).toBe('reading');
    renderer.unmount();
  });

  it('aborts the in-flight request when the drawer closes', async () => {
    const { calls } = stubDeferredPolicyFetch();
    const renderer = await renderProbe(OPEN);
    const [first] = policyCalls(calls);
    expect(first.signal?.aborted).toBe(false);
    await updateProbe(renderer, { ...OPEN, authIndex: undefined });
    expect(first.signal?.aborted).toBe(true);
    renderer.unmount();
  });

  it('never hands a new open the answer read for the previous one', async () => {
    const { calls } = stubDeferredPolicyFetch();
    const renderer = await renderProbe(OPEN);
    await settle(policyCalls(calls)[0], policyStatus({ applied: true, subjectId: 'former' }));
    expect(readOf(renderer)['data-subject']).toBe('former');
    expect(readOf(renderer)['data-applied']).toBe('true');

    // A new open of the same auth index with a credential that changed behind it.
    await updateProbe(renderer, { ...OPEN, openToken: 2 });
    expect(policyCalls(calls)).toHaveLength(2);
    expect(readOf(renderer)['data-kind']).toBe('reading');
    expect(readOf(renderer)['data-subject']).toBe('');

    await settle(policyCalls(calls)[1], policyStatus({ subjectId: 'current' }));
    expect(readOf(renderer)['data-subject']).toBe('current');
    expect(readOf(renderer)['data-applied']).toBe('false');
    renderer.unmount();
  });

  it('reports a failed read without inventing a state', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      return url.includes('authIndex=')
        ? Promise.reject(new Error('network down'))
        : Promise.resolve(Response.json({}));
    });
    vi.stubGlobal('fetch', fetchMock);
    const renderer = await renderProbe(OPEN);
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(readOf(renderer)['data-kind']).toBe('failed');
    renderer.unmount();
  });
});
