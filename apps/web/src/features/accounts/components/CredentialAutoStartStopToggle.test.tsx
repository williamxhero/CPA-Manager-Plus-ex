import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { CredentialAutoStartStopToggle } from './CredentialAutoStartStopToggle';
import {
  credentialCreditsRemain,
  credentialHasRemainingQuota,
  formatCredentialCredits,
} from '@/features/accounts/model/accountQuotaSummary';
import type { CredentialPolicyStatus } from '@/services/api/credentialPolicy';
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (k: string) => k }) }));
const source = (auth_index = 'a', enabled = false): CredentialPolicyStatus => ({
  auth_index,
  sourceId: `source-${auth_index}`,
  subjectId: `issued-${auth_index}`,
  querySubjectId: `query-${auth_index}`,
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
const power = (r: ReactTestRenderer, i = 0) => r.root.findAllByType('button')[i];
const press = (r: ReactTestRenderer, i = 0) => power(r, i).props.onClick();
const intent = (authIndex: string, enabled: boolean) =>
  Response.json([{ authIndex, enabled }]);
afterEach(() => vi.unstubAllGlobals());
it('does not offer a shared quota refresh button on the auto start/stop icon', async () => {
  const policy = { ...source(), querySubjectId: 'quota-subject-a' };
  const issued = {
    kind: 'credential_quota',
    credential: {
      scope: policy.scope,
      targets: policy.targets,
      auth_index: 'a',
      sourceId: 'source-a',
      subjectId: 'quota-subject-a',
      sourceVersion: 'v1',
      version: 0,
    },
  };
  const posts: unknown[] = [];
  let reads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      if (url.endsWith('/full-refresh')) {
        if (init.method === 'POST') {
          posts.push(JSON.parse(String(init.body)));
          return Response.json({ id: 'selected-a', state: 'complete', pending: 0, failed: 0 });
        }
        return Response.json({
          scope: policy.scope,
          sources: [
            {
              id: 'selected-a',
              queryCapable: true,
              request: issued,
              targets: policy.targets,
              sourceId: 'source-a',
              subjectId: 'quota-subject-a',
              sourceVersion: 'v1',
            },
          ],
        });
      }
      return Response.json({ ...policy, reason: reads++ ? 'refreshed-selected-a' : '' });
    })
  );
  let r!: ReactTestRenderer;
  try {
    await act(async () => {
      r = create(
        <CredentialAutoStartStopToggle
          managerServiceBase="http://fixture"
          managementKey="test"
          authIndex="a"
        />
      );
    });
    expect(r.root.findAllByType('button')).toHaveLength(1);
    expect(power(r).props.title).toBe('accounts.auto_start_stop_aria');
    expect(posts).toEqual([]);
    expect(r.root.findAllByProps({ role: 'alert' })).toHaveLength(0);
    expect(JSON.stringify(r.toJSON())).not.toContain('credential_policy.refresh');
  } finally {
    if (r) await act(async () => r.unmount());
  }
});

it('ticket93 saves one issued subject and requires separate server readback, leaving B OFF', async () => {
  const states = { a: source(), b: source('b') };
  const writes: unknown[] = [];
  const storedReads: string[] = [];
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
      if (url.includes('subjectId=')) {
        storedReads.push(url);
        return new Promise<Response>((resolve) => {
          readback = resolve;
        });
      }
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
    press(r);
  });
  expect(power(r).props['aria-pressed']).toBe(true);
  expect(power(r).props.disabled).toBe(true);
  expect(power(r, 1).props['aria-pressed']).toBe(false);
  expect(storedReads).toEqual([
    'http://fixture/usage-service/credential-policy?subjectId=issued-a',
  ]);
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
  expect(power(r).props['aria-pressed']).toBe(true);
  expect(power(r, 1).props['aria-pressed']).toBe(false);
  await act(async () => r.unmount());
});

it.each(['missing', 'mismatched'] as const)(
  'fails shared refresh closed for a %s query subject without a policy/target fallback',
  async (identity) => {
    const policy = {
      ...source(),
      querySubjectId: identity === 'missing' ? undefined : 'wrong-query-subject',
    };
    let posts = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init: RequestInit) => {
        if (!url.endsWith('/full-refresh')) return Response.json(policy);
        if (init.method === 'POST') {
          posts++;
          return Response.json({ id: 'forbidden', state: 'complete', pending: 0, failed: 0 });
        }
        // The missing-field case deliberately offers a policy-shaped subject:
        // an old-server policy hash fallback would improperly accept it.
        const subjectId = identity === 'missing' ? 'issued-a' : 'query-a';
        return Response.json({
          scope: policy.scope,
          sources: [
            {
              queryCapable: true,
              sourceId: 'source-a',
              subjectId,
              targets: policy.targets,
              request: {
                kind: 'credential_quota',
                credential: {
                  scope: policy.scope,
                  targets: policy.targets,
                  auth_index: 'a',
                  sourceId: 'source-a',
                  subjectId,
                  sourceVersion: 'v1',
                  version: 0,
                },
              },
            },
          ],
        });
      })
    );
    let r!: ReactTestRenderer;
    try {
      await act(async () => {
        r = create(
          <CredentialAutoStartStopToggle
            managerServiceBase="http://fixture"
            managementKey="test"
            authIndex="a"
          />
        );
      });
      await act(async () => press(r));
      await flush();
      expect(posts).toBe(0);
      expect(r.root.findAllByProps({ role: 'alert' })).toHaveLength(0);
      expect(JSON.stringify(r.toJSON())).not.toContain('common_refresh_unavailable');
      expect(JSON.stringify(r.toJSON())).not.toContain('credential_policy.saved_off');
    } finally {
      if (r) await act(async () => r.unmount());
    }
  }
);

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
    press(r);
  });
  expect(power(r).props['aria-pressed']).toBe(false);
  expect(JSON.stringify(r.toJSON())).toContain('fresh_authoritative_evidence_required');
  expect(puts).toBe(1);
  await act(async () => r.unmount());
});

it('ticket93 rechecks current capability after readback so a saved OFF can explicitly turn ON again', async () => {
  let state = source('a', true);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      if (url.includes('view=intents')) return intent('a', state.enabled);
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
  await flush();
  await act(async () => {
    press(r);
  });
  expect(power(r).props['aria-pressed']).toBe(false);
  expect(power(r).props.disabled).toBe(false);
  await act(async () => {
    press(r);
  });
  expect(power(r).props['aria-pressed']).toBe(true);
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
    vi.fn(async (url: string, init: RequestInit) => {
      if (url.includes('view=intents')) return intent('a', state.enabled);
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
  await flush();
  expect(power(r).props.disabled).toBe(false);
  expect(power(r).props['aria-pressed']).toBe(true);
  await act(async () => {
    press(r);
    press(r);
  });
  expect(puts).toBe(1);
  state = { ...state, enabled: false, generation: 1 };
  await act(async () => {
    done(Response.json(state));
  });
  expect(power(r).props['aria-pressed']).toBe(false);
  expect(power(r).props.disabled).toBe(true);
  expect(JSON.stringify(r.toJSON())).not.toContain('credential_policy.saved_off');
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
    press(r);
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
  expect(power(r).props['aria-pressed']).toBe(false);
  expect(JSON.stringify(r.toJSON())).not.toContain('credential_policy.saved_on');
  await act(async () => r.unmount());
});
it('management lock cannot enable a false healthy ON without a current operation', async () => {
  let puts = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      if (url.includes('view=intents')) return Response.json([]);
      if (init.method === 'PUT') puts++;
      return Response.json({
        ...source(),
        availability: { state: 'unavailable', reason: 'environment_locked_off' },
      });
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
  await flush();
  await act(async () => {
    press(r);
  });
  expect(puts).toBe(0);
  expect(power(r).props['aria-pressed']).toBe(false);
  expect(power(r).props.disabled).toBe(true);
  expect(JSON.stringify(r.toJSON())).toContain('environment_locked_off');
  expect(JSON.stringify(r.toJSON())).not.toContain('credential available');
  await act(async () => r.unmount());
});

it('drops old source authority when readback discovers a new incarnation under the same selector', async () => {
  let reads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit) => {
      if (url.includes('view=intents')) return Response.json([]);
      if (url.includes('/usage-service/credential-quota')) return Response.json({});
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
    press(r);
  });
  expect(power(r).props['aria-pressed']).toBe(false);
  expect(power(r).props.disabled).toBe(true);
  expect(JSON.stringify(r.toJSON())).toContain('identity_changed');
  await act(async () => r.unmount());
});

it('keeps the power button gray when off and colors it from remaining quota when on', async () => {
  const renderTone = async (enabled: boolean, hasQuota: boolean) => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) =>
        url.includes('view=intents') ? intent('a', enabled) : Response.json(source('a', enabled))
      )
    );
    let r!: ReactTestRenderer;
    await act(async () => {
      r = create(
        <CredentialAutoStartStopToggle
          managerServiceBase="http://fixture"
          managementKey="test"
          authIndex="a"
          hasQuota={hasQuota}
        />
      );
    });
    await flush();
    return r;
  };
  const off = await renderTone(false, true);
  expect(power(off).props['data-credential-auto-start-stop']).toBe('off');
  expect(power(off).props['data-auto-quota']).toBeUndefined();
  await act(async () => off.unmount());
  const ready = await renderTone(true, true);
  expect(power(ready).props['data-credential-auto-start-stop']).toBe('on');
  expect(power(ready).props['data-auto-quota']).toBe('present');
  await act(async () => ready.unmount());
  const empty = await renderTone(true, false);
  expect(power(empty).props['data-credential-auto-start-stop']).toBe('on');
  expect(power(empty).props['data-auto-quota']).toBe('absent');
  await act(async () => empty.unmount());
});

it('treats exhausted quota as empty and any remaining window as present', () => {
  expect(
    credentialHasRemainingQuota({
      status: 'ok',
      groupedAvailabilityState: 'exhausted',
      remainingPercent: 40,
    } as never)
  ).toBe(false);
  expect(
    credentialHasRemainingQuota({
      status: 'unknown',
      groupedAvailabilityState: 'available',
      remainingPercent: null,
    } as never)
  ).toBe(true);
  expect(
    credentialHasRemainingQuota({
      status: 'unknown',
      remainingPercent: null,
    } as never)
  ).toBe(false);
  expect(
    credentialHasRemainingQuota({
      status: 'exhausted',
      groupedAvailabilityState: 'exhausted',
      remainingPercent: 0,
      creditsBalance: '18.4',
    } as never)
  ).toBe(true);
  expect(credentialCreditsRemain({ creditsBalance: '0', creditsUnlimited: false })).toBe(false);
  expect(credentialCreditsRemain({ creditsUnlimited: true, creditsBalance: '0' })).toBe(true);
  expect(credentialCreditsRemain({ creditsBalance: '1', creditsOverageLimitReached: true })).toBe(
    false
  );
  expect(formatCredentialCredits({ creditsBalance: '18.4' })).toBe('18.40');
  expect(formatCredentialCredits({ creditsUnlimited: true })).toBe('∞');
  expect(formatCredentialCredits({ creditsHasCredits: false })).toBe('0.00');
  expect(formatCredentialCredits({})).toBe('—');
});
