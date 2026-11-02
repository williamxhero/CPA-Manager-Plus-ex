import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { CredentialAutoStartStopToggle } from './CredentialAutoStartStopToggle';
import type { CredentialPolicyStatus } from '@/services/api/credentialPolicy';
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k: string) => k }) }));
const source = (auth_index = 'a', enabled = false): CredentialPolicyStatus => ({
  auth_index,
  sourceId: `source-${auth_index}`,
  subjectId: `issued-${auth_index}`,
  queryVersion: 0,
  scope: {
    instance_id: 'instance',
    instance_incarnation: 'incarnation',
    boot_epoch: 'boot',
    snapshot_revision: 1,
  },
  targets: [
    {
      resource_id: auth_index,
      incarnation: 'one',
      identity_version: 1,
      credential_version: 1,
      config_version: 1,
      membership_version: 0,
    },
  ],
  sourceVersion: 'v1',
  protocol: 'codex',
  accountProvenance: 'oauth',
  manualDisabled: false,
  bindingCurrent: true,
  enabled,
  generation: 0,
  owner: 'server-owner',
  state: 'off',
  reason: '',
  applied: false,
  availability: { state: 'available', reason: '' },
  queryAvailability: { state: 'ready', reason: '' },
});
const flush = async () => {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });
};
const toggle = (r: ReactTestRenderer, i = 0) => r.root.findAllByType(ToggleSwitch)[i];
afterEach(() => vi.unstubAllGlobals());
it('ticket93 saves one issued subject and requires separate server readback, leaving B OFF', async () => {
  const states = { a: source(), b: source('b') };
  const writes: unknown[] = [];
  let readback!: (v: Response) => void;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === 'PUT') {
        const body = JSON.parse(String(init.body));
        writes.push(body);
        states.a = { ...states.a, enabled: true, generation: 1 };
        return Response.json(states.a);
      }
      if (url.includes('subjectId='))
        return new Promise<Response>((resolve) => {
          readback = resolve;
        });
      return Response.json(url.includes('authIndex=b') ? states.b : states.a);
    })
  );
  let r!: ReactTestRenderer;
  await act(async () => {
    r = create(
      <>
        <CredentialAutoStartStopToggle
          managerServiceBase="http://fixture"
          managementKey="test"
          authIndex="a"
        />
        <CredentialAutoStartStopToggle
          managerServiceBase="http://fixture"
          managementKey="test"
          authIndex="b"
        />
      </>
    );
  });
  await flush();
  await act(async () => {
    toggle(r).props.onChange(true);
  });
  expect(toggle(r).props.checked).toBe(false);
  expect(toggle(r).props.disabled).toBe(true);
  expect(toggle(r, 1).props.checked).toBe(false);
  expect(writes).toEqual([
    {
      scope: states.a.scope,
      targets: states.a.targets,
      auth_index: 'a',
      sourceVersion: 'v1',
      expectedGeneration: 0,
      owner: 'server-owner',
      enabled: true,
    },
  ]);
  await act(async () => {
    readback(Response.json(states.a));
  });
  expect(toggle(r).props.checked).toBe(true);
  expect(toggle(r, 1).props.checked).toBe(false);
  await act(async () => r.unmount());
});

it('ticket93 rejects ON without fresh common76 evidence, preserves OFF and explains refresh without retry', async () => {
  let puts = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      if (init.method === 'PUT') {
        puts++;
        return Response.json({ error: 'fresh_authoritative_evidence_required' }, { status: 503 });
      }
      return Response.json(source());
    })
  );
  let r!: ReactTestRenderer;
  await act(async () => {
    r = create(
      <CredentialAutoStartStopToggle
        managerServiceBase="http://fixture"
        managementKey="test"
        authIndex="a"
      />
    );
  });
  await act(async () => {
    toggle(r).props.onChange(true);
  });
  expect(toggle(r).props.checked).toBe(false);
  expect(JSON.stringify(r.toJSON())).toContain('fresh_authoritative_evidence_required');
  expect(puts).toBe(1);
  await act(async () => r.unmount());
});

it('ticket93 rechecks current capability after readback so a saved OFF can explicitly turn ON again', async () => {
  let state = source('a', true);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === 'PUT') {
        state = {
          ...state,
          enabled: JSON.parse(String(init.body)).enabled,
          generation: state.generation + 1,
        };
        return Response.json(state);
      }
      return Response.json(
        url.includes('subjectId=')
          ? {
              ...state,
              bindingCurrent: false,
              availability: { state: 'unavailable', reason: 'current_binding_unverified' },
            }
          : state
      );
    })
  );
  let r!: ReactTestRenderer;
  await act(async () => {
    r = create(
      <CredentialAutoStartStopToggle
        managerServiceBase="http://fixture"
        managementKey="test"
        authIndex="a"
      />
    );
  });
  await act(async () => {
    toggle(r).props.onChange(false);
  });
  expect(toggle(r).props.checked).toBe(false);
  expect(toggle(r).props.disabled).toBe(false);
  await act(async () => {
    toggle(r).props.onChange(true);
  });
  expect(toggle(r).props.checked).toBe(true);
  await act(async () => r.unmount());
});

it('keeps a known ON subject OFF-capable while Core unavailable and ignores a rapid duplicate submission', async () => {
  let state = {
    ...source('a', true),
    bindingCurrent: false,
    availability: { state: 'unavailable' as const, reason: 'gate_core_unavailable' },
  };
  let done!: (r: Response) => void;
  let puts = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      if (init.method === 'PUT') {
        puts++;
        return new Promise<Response>((r) => {
          done = r;
        });
      }
      return Response.json(state);
    })
  );
  let r!: ReactTestRenderer;
  await act(async () => {
    r = create(
      <CredentialAutoStartStopToggle
        managerServiceBase="http://fixture"
        managementKey="test"
        authIndex="a"
      />
    );
  });
  expect(toggle(r).props.disabled).toBe(false);
  await act(async () => {
    toggle(r).props.onChange(false);
    toggle(r).props.onChange(false);
  });
  expect(puts).toBe(1);
  state = { ...state, enabled: false, generation: 1 };
  await act(async () => {
    done(Response.json(state));
  });
  expect(toggle(r).props.checked).toBe(false);
  expect(toggle(r).props.disabled).toBe(true);
  expect(JSON.stringify(r.toJSON())).toContain('credential_policy.saved_off');
  await act(async () => r.unmount());
});
it('never publishes a late old-instance ON result or a reused-selector mutation result', async () => {
  let done!: (r: Response) => void;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === 'PUT')
        return new Promise<Response>((r) => {
          done = r;
        });
      return Response.json(
        url.startsWith('http://old') ? source() : { ...source('b'), sourceVersion: 'v2' }
      );
    })
  );
  let r!: ReactTestRenderer;
  await act(async () => {
    r = create(
      <CredentialAutoStartStopToggle
        managerServiceBase="http://old"
        managementKey="test"
        authIndex="a"
      />
    );
  });
  await act(async () => {
    toggle(r).props.onChange(true);
  });
  await act(async () => {
    r.update(
      <CredentialAutoStartStopToggle
        managerServiceBase="http://new"
        managementKey="test"
        authIndex="a"
        revision={2}
      />
    );
  });
  await act(async () => {
    done(Response.json({ ...source(), enabled: true, generation: 1 }));
  });
  expect(toggle(r).props.checked).toBe(false);
  expect(JSON.stringify(r.toJSON())).not.toContain('credential_policy.saved_on');
  await act(async () => r.unmount());
});
it('management lock cannot enable a false healthy ON without a current operation', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      Response.json({
        ...source(),
        availability: { state: 'unavailable', reason: 'environment_locked_off' },
      })
    )
  );
  let r!: ReactTestRenderer;
  await act(async () => {
    r = create(
      <CredentialAutoStartStopToggle
        managerServiceBase="http://fixture"
        managementKey="test"
        authIndex="a"
      />
    );
  });
  expect(toggle(r).props.disabled).toBe(true);
  expect(JSON.stringify(r.toJSON())).toContain('environment_locked_off');
  expect(JSON.stringify(r.toJSON())).not.toContain('credential available');
  await act(async () => r.unmount());
});

it('drops old source authority when readback discovers a new incarnation under the same selector', async () => {
  let reads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      if (init.method === 'PUT' || url.includes('subjectId='))
        return Response.json({ ...source(), enabled: true, generation: 1 });
      return Response.json(
        reads++ ? { ...source(), subjectId: 'new-incarnation', sourceVersion: 'v2' } : source()
      );
    })
  );
  let r!: ReactTestRenderer;
  await act(async () => {
    r = create(
      <CredentialAutoStartStopToggle
        managerServiceBase="http://fixture"
        managementKey="test"
        authIndex="a"
      />
    );
  });
  await act(async () => {
    toggle(r).props.onChange(true);
  });
  expect(toggle(r).props.checked).toBe(false);
  expect(toggle(r).props.disabled).toBe(true);
  expect(JSON.stringify(r.toJSON())).toContain('identity_changed');
  await act(async () => r.unmount());
});
