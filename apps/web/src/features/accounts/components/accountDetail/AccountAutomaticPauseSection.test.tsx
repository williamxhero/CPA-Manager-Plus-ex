import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { formatTimestamp } from '@/features/accounts/model/accountsPagePresentation';
import type { AccountDetailViewModel } from '@/features/accounts/model/accountDetailViewModel';
import { AccountAutomaticPauseSection } from './AccountAutomaticPauseSection';
import { AccountOverviewTab } from './AccountOverviewTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('react-i18next', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-i18next')>();
  return {
    ...actual,
    useTranslation: () => ({
      t: (key: string, options?: Record<string, unknown>) => {
        const params = options?.defaultValue;
        if (params !== undefined) return key;
        if (!options) return key;
        const rendered = Object.entries(options)
          .filter(([name]) => name !== 'defaultValue')
          .map(([name, value]) => `${name}=${String(value)}`)
          .join(',');
        return rendered ? `${key}:${rendered}` : key;
      },
      i18n: { language: 'en' },
    }),
  };
});

const readText = (value: unknown): string => {
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.map(readText).join('');
  if (value && typeof value === 'object' && 'children' in value) {
    return readText((value as { children?: unknown }).children);
  }
  return '';
};

const treeText = (renderer: ReactTestRenderer) => readText(renderer.toJSON());

const flushPromises = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
};

const stubPolicyRead = (respond: () => Promise<Response>) => {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    calls.push(String(input));
    return respond();
  });
  vi.stubGlobal('fetch', fetchMock);
  return { calls, fetchMock };
};

const renderSection = async (props: {
  managerServiceBase?: string;
  managementKey?: string;
  authIndex?: string;
}) => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(<AccountAutomaticPauseSection {...props} />);
  });
  return renderer;
};

const rendersNoControls = (renderer: ReactTestRenderer) => {
  const interactive = renderer.root.findAll(
    (node) =>
      typeof node.type === 'string' &&
      ['button', 'input', 'select', 'textarea'].includes(node.type)
  );
  const switches = renderer.root.findAll(
    (node) => node.props.role === 'switch' || node.props.role === 'button'
  );
  return interactive.length === 0 && switches.length === 0;
};

const findHint = (renderer: ReactTestRenderer, hintKey: string) =>
  renderer.root.findAll((node) => node.props.title === hintKey);

const makeDetailView = (): AccountDetailViewModel =>
  ({
    health: { tooltipKey: 'accounts.health_tip_raw', tooltipParams: {}, resetAtMs: null },
    overview: {
      decision: {
        status: 'unknown',
        labelKey: 'accounts.health_status_unknown',
        reasonKey: 'accounts.health_detail_unknown',
        reasonParams: {},
        basisLabelKey: 'accounts.detail_overview_basis_credential_state',
        observedAtMs: null,
      },
      capacity: {
        kind: 'remaining',
        statusLabelKey: 'accounts.detail_overview_capacity_missing',
        remainingPercent: null,
        fields: [],
      },
      credential: {
        statusLabelKey: 'accounts.credential_status_manual_disabled',
        sourceLabelKey: 'accounts.credential_source_local',
        fields: [],
      },
      recentStatus: { recentRequests: [], statusMessage: '' },
      activity: {
        scope: 'recent_snapshot',
        metrics: [],
        sourceLabelKey: 'accounts.detail_overview_activity_scope_recent',
        hasActivity: false,
        emptyStateKey: 'accounts.detail_overview_activity_empty_recent',
      },
      attention: null,
    },
    quota: { cooldown: null },
  }) as unknown as AccountDetailViewModel;

describe('AccountAutomaticPauseSection', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows a reading state before the backend answers, with no controls', async () => {
    stubPolicyRead(() => new Promise<Response>(() => {}));
    const renderer = await renderSection({
      managerServiceBase: 'http://manager.local:18317',
      managementKey: 'manager-key',
      authIndex: 'auth-1',
    });
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_title');
    expect(text).toContain('accounts.detail_overview_auto_pause_state_reading');
    expect(findHint(renderer, 'accounts.detail_overview_auto_pause_reading_hint')).not.toHaveLength(
      0
    );
    expect(rendersNoControls(renderer)).toBe(true);
    renderer.unmount();
  });

  it('reads a participating credential with an explicit unjudged time and no schedule', async () => {
    const { calls, fetchMock } = stubPolicyRead(() =>
      Promise.resolve(
        Response.json({
          subjectId: 'subject-1',
          enabled: true,
          state: 'armed',
          reason: 'policy_enabled',
          availability: { state: 'available', reason: 'current_source_gate_ready' },
          applied: false,
        })
      )
    );
    const renderer = await renderSection({
      managerServiceBase: 'http://manager.local:18317/',
      managementKey: 'manager-key',
      authIndex: 'auth-1',
    });
    await flushPromises();
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_active');
    expect(text).toContain('accounts.auto_pause_reason_policy_enabled');
    expect(text).toContain('accounts.detail_overview_auto_pause_observed_missing');
    expect(text).toContain('accounts.detail_overview_auto_pause_next_review_missing');
    expect(findHint(renderer, 'accounts.detail_overview_auto_pause_active_hint')).not.toHaveLength(0);
    expect(calls).toEqual(['http://manager.local:18317/usage-service/credential-policy?authIndex=auth-1']);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(rendersNoControls(renderer)).toBe(true);
    renderer.unmount();
  });

  it('reads an applied pause with its backend reason and real times', async () => {
    const observedAtMs = 1_700_000_000_000;
    const nextReviewAtMs = 1_700_000_600_000;
    stubPolicyRead(() =>
      Promise.resolve(
        Response.json({
          subjectId: 'subject-1',
          enabled: true,
          applied: true,
          availability: { state: 'available', reason: 'current_source_gate_ready' },
          operation: { reason: 'current_gate_confirmed', action: 'pause', currentApplied: true },
          observedAtMs,
          nextReviewAtMs,
        })
      )
    );
    const renderer = await renderSection({
      managerServiceBase: 'http://manager.local:18317',
      managementKey: 'manager-key',
      authIndex: 'auth-1',
    });
    await flushPromises();
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_paused');
    expect(text).toContain('accounts.auto_pause_reason_current_gate_confirmed');
    expect(text).toContain(formatTimestamp(observedAtMs, 'en'));
    expect(text).toContain(formatTimestamp(nextReviewAtMs, 'en'));
    expect(text).not.toContain('accounts.detail_overview_auto_pause_observed_missing');
    expect(text).not.toContain('accounts.detail_overview_auto_pause_next_review_missing');
    expect(findHint(renderer, 'accounts.detail_overview_auto_pause_paused_hint')).not.toHaveLength(0);
    renderer.unmount();
  });

  it('shows an unknown reason verbatim instead of leaving the row blank', async () => {
    stubPolicyRead(() =>
      Promise.resolve(
        Response.json({
          subjectId: 'subject-1',
          enabled: true,
          applied: true,
          availability: { state: 'available', reason: 'current_source_gate_ready' },
          operation: { reason: 'gate_future_reason', action: 'pause' },
        })
      )
    );
    const renderer = await renderSection({
      managerServiceBase: 'http://manager.local:18317',
      managementKey: 'manager-key',
      authIndex: 'auth-1',
    });
    await flushPromises();
    expect(treeText(renderer)).toContain('gate_future_reason');
    renderer.unmount();
  });

  it('says the state is unreadable when the capability is unavailable', async () => {
    stubPolicyRead(() =>
      Promise.resolve(
        Response.json({
          subjectId: 'subject-1',
          enabled: true,
          availability: { state: 'unavailable', reason: 'gate_core_unavailable' },
        })
      )
    );
    const renderer = await renderSection({
      managerServiceBase: 'http://manager.local:18317',
      managementKey: 'manager-key',
      authIndex: 'auth-1',
    });
    await flushPromises();
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_unavailable');
    expect(text).toContain('accounts.auto_pause_reason_gate_core_unavailable');
    expect(text).not.toContain('accounts.detail_overview_auto_pause_state_active');
    expect(text).not.toContain('accounts.detail_overview_auto_pause_state_paused');
    expect(
      findHint(renderer, 'accounts.detail_overview_auto_pause_unavailable_hint')
    ).not.toHaveLength(0);
    renderer.unmount();
  });

  it('turns a failed read into unreadable state without throwing or flashing empty', async () => {
    stubPolicyRead(() => Promise.reject(new Error('network down')));
    const renderer = await renderSection({
      managerServiceBase: 'http://manager.local:18317',
      managementKey: 'manager-key',
      authIndex: 'auth-1',
    });
    await flushPromises();
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_unavailable');
    expect(text).toContain('accounts.auto_pause_reason_panel_read_failed');
    expect(text).toContain('accounts.detail_overview_auto_pause_observed_missing');
    expect(text).toContain('accounts.detail_overview_auto_pause_next_review_missing');
    renderer.unmount();
  });

  it('reads nothing and reports a missing identity when the drawer has no auth index', async () => {
    const { fetchMock } = stubPolicyRead(() => Promise.resolve(Response.json({})));
    const renderer = await renderSection({
      managerServiceBase: 'http://manager.local:18317',
      managementKey: 'manager-key',
    });
    await flushPromises();
    expect(fetchMock).not.toHaveBeenCalled();
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_unavailable');
    expect(text).toContain('accounts.auto_pause_reason_panel_identity_unavailable');
    renderer.unmount();
  });

  it('keeps a manual credential stop and an automatic pause side by side', async () => {
    stubPolicyRead(() =>
      Promise.resolve(
        Response.json({
          subjectId: 'subject-1',
          enabled: true,
          applied: true,
          availability: { state: 'available', reason: 'current_source_gate_ready' },
          operation: { reason: 'current_gate_confirmed', action: 'pause' },
        })
      )
    );
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountOverviewTab
          detailView={makeDetailView()}
          getHealthStatusClass={() => ''}
          onSelectTab={() => {}}
          managerServiceBase="http://manager.local:18317"
          managementKey="manager-key"
          credentialAuthIndex="auth-1"
        />
      );
    });
    await flushPromises();
    const text = treeText(renderer);
    expect(text).toContain('accounts.credential_status_manual_disabled');
    expect(text).toContain('accounts.detail_overview_auto_pause_state_paused');
    expect(text).toContain('accounts.auto_pause_reason_current_gate_confirmed');
    renderer.unmount();
  });
});
