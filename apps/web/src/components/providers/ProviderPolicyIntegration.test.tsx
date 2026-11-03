import { useState } from 'react';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { ProviderTable } from './ProviderTable/ProviderTable';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type { ProviderPolicyStatus } from '@/services/api/providerPolicy';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const providerId = '11111111-1111-4111-8111-111111111111';
const memberId = '66666666-6666-4666-8666-666666666666';
const scope = {
  instance_id: '22222222-2222-4222-8222-222222222222',
  instance_incarnation: '33333333-3333-4333-8333-333333333333',
  boot_epoch: '44444444-4444-4444-8444-444444444444',
  snapshot_revision: 7,
};
const target = {
  resource_id: providerId,
  incarnation: '55555555-5555-4555-8555-555555555555',
  identity_version: 1,
  credential_version: 3,
  config_version: 4,
  membership_version: 1,
};
const binding = {
  scope,
  targets: [target, { ...target, resource_id: memberId, membership_version: 0 }],
};
const row = buildProviderRows({
  gemini: [],
  codex: [],
  claude: [],
  vertex: [],
  openai: [
    {
      name: 'Bound representative',
      disabled: true,
      resourceId: providerId,
      baseUrl: 'https://api.deepseek.com/v1',
      apiKeyEntries: [{ apiKey: 'UNSENT-RAW-KEY', resourceId: memberId }],
    },
  ],
  usageByProvider: new Map(),
})[0];
const connection = { base: 'http://fixture', managementKey: 'panel-key' };
const state: ProviderPolicyStatus = {
  ...binding,
  providerId,
  subjectId: 'issued-subject',
  enabled: true,
  effective: false,
  generation: 5,
  owner: 'issued-owner',
  source: 'explicit',
  updatedAtMs: 0,
  bindingCurrent: true,
  locked: true,
  manualDisabled: true,
  ownPaused: true,
  applied: false,
  state: 'unavailable',
  reason: 'funding_coverage_unverified',
};
let renderer: ReactTestRenderer | undefined;
afterEach(async () => {
  if (renderer) await act(async () => renderer!.unmount());
  renderer = undefined;
  vi.unstubAllGlobals();
});

it('wires representative table/detail to the same proven parent, preserving both manual controls and independent OFF', async () => {
  let saved = state;
  const manual = vi.fn();
  const show = vi.fn();
  const fetch = vi.fn(async (url: string, init: RequestInit) => {
    if (url.includes('/official-balance'))
      return Response.json({
        ...binding,
        version: 0,
        adapter: '',
        endpoint: '',
        configured: false,
        state: 'needs_configuration',
        stale: false,
      });
    if (init.method === 'PUT') saved = { ...state, enabled: false, generation: 6, state: 'off' };
    return Response.json(saved);
  });
  vi.stubGlobal('fetch', fetch);
  function Harness() {
    const [revision, setRevision] = useState(0);
    const shared = { ...connection, revision, onChanged: () => setRevision((value) => value + 1) };
    return (
      <>
        <ProviderTable
          rows={[row]}
          resourceProtectionForRow={() => ({ status: 'identity_only', binding })}
          providerPolicyConnection={shared}
          loading={false}
          actionsDisabled={false}
          toggleDisabled={false}
          resolvedTheme="light"
          emptyState={null}
          onShowDetail={show}
          onToggle={manual}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          onPriorityChange={vi.fn()}
        />
        <ProviderDetailDrawer
          row={row}
          open
          usageByProvider={new Map()}
          resolvedTheme="light"
          actionsDisabled={false}
          toggleDisabled={false}
          providerPolicy={shared}
          officialBalance={{ ...connection, binding }}
          onClose={vi.fn()}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          onToggle={manual}
          onToggleWebsockets={vi.fn()}
          onToggleCloak={vi.fn()}
          onToggleDisableCooling={vi.fn()}
        />
      </>
    );
  }
  await act(async () => {
    renderer = create(<Harness />);
  });
  const policies = () => renderer!.root.findAllByProps({ role: 'switch' });
  expect(policies()).toHaveLength(2);
  expect(policies().map((node) => node.props['aria-checked'])).toEqual([true, true]);
  const label = renderer!.root.findAll(
    (node) => node.type === 'span' && node.props.title === row.label
  )[0];
  const heading = policies()[0].parent!;
  expect(heading.children.indexOf(policies()[0])).toBeLessThan(heading.children.indexOf(label));
  const switches = renderer!.root.findAllByType(ToggleSwitch);
  expect(switches).toHaveLength(2);
  expect(switches.map((node) => node.props.checked)).toEqual([false, false]);
  const stop = vi.fn();
  await act(async () => policies()[0].props.onClick({ stopPropagation: stop }));
  expect(stop).toHaveBeenCalledOnce();
  expect(show).not.toHaveBeenCalled();
  expect(manual).not.toHaveBeenCalled();
  expect(policies().map((node) => node.props['aria-checked'])).toEqual([false, false]);
  expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.off_retains_pause');
  expect(switches.map((node) => node.props.checked)).toEqual([false, false]);
  act(() => switches[0].props.onChange(true));
  expect(manual).toHaveBeenCalledExactlyOnceWith(row, true);
  const writes = fetch.mock.calls.filter(([, init]) => init.method !== 'GET');
  expect(writes).toHaveLength(1);
  expect(JSON.parse(String(writes[0][1].body))).toEqual({
    ...binding,
    providerId,
    expectedGeneration: 5,
    enabled: false,
  });
  expect(JSON.stringify(fetch.mock.calls)).not.toContain('UNSENT-RAW-KEY');
});

it('detail refuses a policy lookup when officialBalance has no issued parent proof even if its label/URL match', async () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  await act(async () => {
    renderer = create(
      <ProviderDetailDrawer
        row={row}
        open
        usageByProvider={new Map()}
        resolvedTheme="light"
        actionsDisabled={false}
        toggleDisabled={false}
        providerPolicy={connection}
        officialBalance={{ ...connection }}
        onClose={vi.fn()}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        onToggle={vi.fn()}
        onToggleWebsockets={vi.fn()}
        onToggleCloak={vi.fn()}
        onToggleDisableCooling={vi.fn()}
      />
    );
  });
  expect(renderer!.root.findByProps({ role: 'switch' }).props.disabled).toBe(true);
  expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.no_parent_proof');
  expect(fetch).not.toHaveBeenCalled();
});

// Exercise both real surfaces and the public HTTP boundary; do not mock the hook
// or its shared authority registry. Each surface must recover by its own GET.
async function mountPolicySurfaces() {
  const manual = vi.fn();
  const show = vi.fn();
  function Harness() {
    const [revision, setRevision] = useState(0);
    const shared = { ...connection, revision, onChanged: () => setRevision((value) => value + 1) };
    return (
      <>
        <ProviderTable
          rows={[row]}
          resourceProtectionForRow={() => ({ status: 'identity_only', binding })}
          providerPolicyConnection={shared}
          loading={false}
          actionsDisabled={false}
          toggleDisabled={false}
          resolvedTheme="light"
          emptyState={null}
          onShowDetail={show}
          onToggle={manual}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          onPriorityChange={vi.fn()}
        />
        <ProviderDetailDrawer
          row={row}
          open
          usageByProvider={new Map()}
          resolvedTheme="light"
          actionsDisabled={false}
          toggleDisabled={false}
          providerPolicy={shared}
          officialBalance={{ ...connection, binding }}
          onClose={vi.fn()}
          onEdit={vi.fn()}
          onDelete={vi.fn()}
          onToggle={manual}
          onToggleWebsockets={vi.fn()}
          onToggleCloak={vi.fn()}
          onToggleDisableCooling={vi.fn()}
        />
      </>
    );
  }
  await act(async () => {
    renderer = create(<Harness />);
  });
  return {
    manual,
    show,
    policies: () => renderer!.root.findAllByProps({ role: 'switch' }),
    reads: () => renderer!.root.findAllByProps({ 'aria-label': 'provider_policy.read_status' }),
    savedReceipts: () =>
      renderer!.root.findAll(
        (node) =>
          node.type === 'span' &&
          node.children.some(
            (child) => typeof child === 'string' && child.includes('provider_policy.saved')
          )
      ),
  };
}

function unconfiguredOfficialBalance() {
  return Response.json({
    ...binding,
    version: 0,
    adapter: '',
    endpoint: '',
    configured: false,
    state: 'needs_configuration',
    stale: false,
  });
}

it.each(
  [
    { stage: 'GET', code: 401 },
    { stage: 'GET', code: 403 },
    { stage: 'PUT', code: 401 },
    { stage: 'PUT', code: 403 },
    { stage: 'post-PUT-GET', code: 401 },
    { stage: 'post-PUT-GET', code: 403 },
  ].flatMap((testCase) => ['list', 'detail'].map((surface) => ({ ...testCase, surface })))
)(
  '$surface $stage $code revokes both marks and captured sibling callbacks; each surface recovers only by explicit GET',
  async ({ stage, code, surface }) => {
    const initiating = surface === 'list' ? 0 : 1;
    const sibling = 1 - initiating;
    const accepted: ProviderPolicyStatus = {
      ...state,
      enabled: false,
      generation: 6,
      state: 'off',
    };
    const recovered: ProviderPolicyStatus = {
      ...state,
      generation: 7,
      locked: false,
      applied: true,
      state: 'paused',
    };
    let phase: 'initial' | 'reject' | 'recover' = 'initial';
    const recoveryReads: ((response: Response) => void)[] = [];
    const fetch = vi.fn(async (url: string, init: RequestInit) => {
      if (url.includes('/official-balance')) return unconfiguredOfficialBalance();
      if (!url.includes('/provider-policy')) throw new Error('Unexpected integration endpoint');
      if (phase === 'initial') return Response.json(state);
      if (phase === 'reject') {
        if (init.method === 'PUT' && stage === 'post-PUT-GET') return Response.json(accepted);
        return new Response('PRIVATE-AUTH-REJECTION', { status: code });
      }
      return new Promise<Response>((resolve) => {
        recoveryReads.push(resolve);
      });
    });
    vi.stubGlobal('fetch', fetch);
    const { policies, reads, savedReceipts, manual, show } = await mountPolicySurfaces();
    const policyCalls = () => fetch.mock.calls.filter(([url]) => url.includes('/provider-policy'));
    const methods = () => policyCalls().map(([, init]) => init.method);
    expect(policies()).toHaveLength(2);
    expect(reads()).toHaveLength(2);
    expect(policies().map((node) => node.props['aria-checked'])).toEqual([true, true]);
    expect(policies().map((node) => node.props.disabled)).toEqual([false, false]);
    expect(savedReceipts()).toHaveLength(2);
    expect(methods()).toEqual(['GET', 'GET']);

    const oldClicks = policies().map((node) => node.props.onClick);
    const oldReads = reads().map((node) => node.props.onClick);
    phase = 'reject';
    await act(async () => {
      if (stage === 'GET') oldReads[initiating]({ stopPropagation: vi.fn() });
      else oldClicks[initiating]({ stopPropagation: vi.fn() });
    });

    const rejectedMethods =
      stage === 'GET'
        ? ['GET', 'GET', 'GET']
        : stage === 'PUT'
          ? ['GET', 'GET', 'PUT']
          : ['GET', 'GET', 'PUT', 'GET'];
    // The rejection must not cause an onChanged-driven auth retry on either
    // surface, or leave the other surface's previously verified ON mark alive.
    expect(methods()).toEqual(rejectedMethods);
    expect(policies().map((node) => node.props['aria-checked'])).toEqual([false, false]);
    expect(policies().map((node) => node.props.disabled)).toEqual([true, true]);
    expect(savedReceipts()).toHaveLength(0);
    expect(JSON.stringify(renderer!.toJSON())).not.toContain('provider_policy.effective');
    expect(JSON.stringify(renderer!.toJSON())).not.toContain('PRIVATE-AUTH-REJECTION');
    await act(async () => {
      oldClicks[sibling]({ stopPropagation: vi.fn() });
      oldReads[sibling]({ stopPropagation: vi.fn() });
      oldClicks[initiating]({ stopPropagation: vi.fn() });
      oldReads[initiating]({ stopPropagation: vi.fn() });
    });
    expect(methods()).toEqual(rejectedMethods);

    phase = 'recover';
    const firstRecoveryRead = reads()[initiating].props.onClick;
    await act(async () => firstRecoveryRead({ stopPropagation: vi.fn() }));
    expect(methods()).toEqual([...rejectedMethods, 'GET']);
    expect(recoveryReads).toHaveLength(1);
    expect(savedReceipts()).toHaveLength(0);
    expect(policies().map((node) => node.props['aria-checked'])).toEqual([false, false]);
    await act(async () => recoveryReads.shift()!(Response.json(recovered)));

    const firstMarks = [false, false];
    firstMarks[initiating] = true;
    const firstDisabled = [true, true];
    firstDisabled[initiating] = false;
    expect(policies().map((node) => node.props['aria-checked'])).toEqual(firstMarks);
    expect(policies().map((node) => node.props.disabled)).toEqual(firstDisabled);
    expect(savedReceipts()).toHaveLength(1);
    expect(methods()).toEqual([...rejectedMethods, 'GET']);
    await act(async () => {
      oldClicks.forEach((click) => click({ stopPropagation: vi.fn() }));
      oldReads.forEach((read) => read({ stopPropagation: vi.fn() }));
      firstRecoveryRead({ stopPropagation: vi.fn() });
    });
    expect(methods()).toEqual([...rejectedMethods, 'GET']);

    // A successful read in one view is not permission to resurrect the other
    // view's receipt. Its current explicit read must obtain a fresh receipt too.
    await act(async () => reads()[sibling].props.onClick({ stopPropagation: vi.fn() }));
    expect(methods()).toEqual([...rejectedMethods, 'GET', 'GET']);
    expect(recoveryReads).toHaveLength(1);
    expect(savedReceipts()).toHaveLength(1);
    await act(async () => recoveryReads.shift()!(Response.json(recovered)));
    expect(policies().map((node) => node.props['aria-checked'])).toEqual([true, true]);
    expect(policies().map((node) => node.props.disabled)).toEqual([false, false]);
    expect(savedReceipts()).toHaveLength(2);
    await act(async () => {
      oldClicks.forEach((click) => click({ stopPropagation: vi.fn() }));
      oldReads.forEach((read) => read({ stopPropagation: vi.fn() }));
      firstRecoveryRead({ stopPropagation: vi.fn() });
    });
    expect(methods()).toEqual([...rejectedMethods, 'GET', 'GET']);
    expect(recoveryReads).toHaveLength(0);
    expect(manual).not.toHaveBeenCalled();
    expect(show).not.toHaveBeenCalled();
    expect(JSON.stringify(policyCalls())).not.toContain('UNSENT-RAW-KEY');
    if (stage !== 'GET') {
      const writes = policyCalls().filter(([, init]) => init.method === 'PUT');
      expect(writes).toHaveLength(1);
      expect(JSON.parse(String(writes[0][1].body))).toEqual({
        ...binding,
        providerId,
        expectedGeneration: 5,
        enabled: false,
      });
    }
  }
);

it.each(
  ['503', 'transport'].flatMap((failure) =>
    ['list', 'detail'].map((surface) => ({ failure, surface }))
  )
)(
  '$surface accepted OFF with $failure readback still notifies its sibling to read without replaying PUT',
  async ({ failure, surface }) => {
    const initiating = surface === 'list' ? 0 : 1;
    const sibling = 1 - initiating;
    const accepted: ProviderPolicyStatus = {
      ...state,
      enabled: false,
      generation: 6,
      state: 'off',
    };
    let writeAccepted = false;
    let failedReadback = false;
    let postWriteReads = 0;
    const fetch = vi.fn(async (url: string, init: RequestInit) => {
      if (url.includes('/official-balance')) return unconfiguredOfficialBalance();
      if (!url.includes('/provider-policy')) throw new Error('Unexpected integration endpoint');
      if (init.method === 'PUT') {
        writeAccepted = true;
        return Response.json(accepted);
      }
      if (!writeAccepted) return Response.json(state);
      postWriteReads += 1;
      if (!failedReadback) {
        failedReadback = true;
        if (failure === 'transport') throw new TypeError('PRIVATE-READBACK-TRANSPORT-ERROR');
        return new Response('PRIVATE-READBACK-PROXY-ERROR', { status: 503 });
      }
      return Response.json(accepted);
    });
    vi.stubGlobal('fetch', fetch);
    const { policies, savedReceipts, manual, show } = await mountPolicySurfaces();
    const policyCalls = () => fetch.mock.calls.filter(([url]) => url.includes('/provider-policy'));
    expect(policies().map((node) => node.props['aria-checked'])).toEqual([true, true]);
    expect(savedReceipts()).toHaveLength(2);
    const oldSiblingClick = policies()[sibling].props.onClick;
    await act(async () => policies()[initiating].props.onClick({ stopPropagation: vi.fn() }));

    expect(failedReadback).toBe(true);
    expect(postWriteReads).toBeGreaterThanOrEqual(2);
    expect(policies()[sibling].props['aria-checked']).toBe(false);
    // A transient readback failure must not be broadcast as auth revocation:
    // the sibling obtains the accepted OFF receipt and retains own/manual pause.
    expect(savedReceipts()).toHaveLength(2);
    expect(JSON.stringify(renderer!.toJSON())).toContain('provider_policy.off_retains_pause');
    expect(JSON.stringify(renderer!.toJSON())).not.toContain('PRIVATE-READBACK-TRANSPORT-ERROR');
    expect(JSON.stringify(renderer!.toJSON())).not.toContain('PRIVATE-READBACK-PROXY-ERROR');
    await act(async () => oldSiblingClick({ stopPropagation: vi.fn() }));
    const writes = policyCalls().filter(([, init]) => init.method !== 'GET');
    expect(writes).toHaveLength(1);
    expect(writes[0][1].method).toBe('PUT');
    expect(JSON.parse(String(writes[0][1].body))).toEqual({
      ...binding,
      providerId,
      expectedGeneration: 5,
      enabled: false,
    });
    expect(manual).not.toHaveBeenCalled();
    expect(show).not.toHaveBeenCalled();
    expect(JSON.stringify(policyCalls())).not.toContain('UNSENT-RAW-KEY');
  }
);
