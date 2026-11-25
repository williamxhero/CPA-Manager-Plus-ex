import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { formatTimestamp } from '@/features/accounts/model/accountsPagePresentation';
import type { AccountDetailViewModel } from '@/features/accounts/model/accountDetailViewModel';
import type { AccountAutomaticPauseRead } from '@/features/accounts/model/accountAutomaticPause';
import type { CredentialPolicyStatus } from '@/services/api/credentialPolicy';
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

const renderSection = async (read: AccountAutomaticPauseRead) => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(<AccountAutomaticPauseSection read={read} />);
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

const makeStatus = (overrides: Partial<CredentialPolicyStatus> = {}): CredentialPolicyStatus =>
  ({
    subjectId: 'subject-1',
    bindingCurrent: true,
    enabled: true,
    generation: 1,
    owner: 'manager',
    state: 'armed',
    reason: 'policy_enabled',
    availability: { state: 'available', reason: 'current_source_gate_ready' },
    queryAvailability: { state: 'available', reason: 'verified_selected_key_evidence' },
    applied: false,
    ...overrides,
  }) as CredentialPolicyStatus;

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
  const fetchMock = vi.fn();

  beforeEach(() => {
    fetchMock.mockReset();
    vi.stubGlobal('fetch', fetchMock);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('shows a reading state before the drawer has an answer, with no controls', async () => {
    const renderer = await renderSection({ kind: 'reading' });
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_title');
    expect(text).toContain('accounts.detail_overview_auto_pause_state_reading');
    expect(findHint(renderer, 'accounts.detail_overview_auto_pause_reading_hint')).not.toHaveLength(
      0
    );
    expect(rendersNoControls(renderer)).toBe(true);
    renderer.unmount();
  });

  it('never reads on its own: the drawer owns the only policy request', async () => {
    const renderer = await renderSection({ kind: 'ready', status: makeStatus() });
    expect(fetchMock).not.toHaveBeenCalled();
    renderer.unmount();
  });

  it('presents a participating credential with an explicit unjudged time and no schedule', async () => {
    const renderer = await renderSection({ kind: 'ready', status: makeStatus() });
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_active');
    expect(text).toContain('accounts.auto_pause_reason_policy_enabled');
    expect(text).toContain('accounts.detail_overview_auto_pause_observed_missing');
    expect(text).toContain('accounts.detail_overview_auto_pause_next_review_missing');
    expect(findHint(renderer, 'accounts.detail_overview_auto_pause_active_hint')).not.toHaveLength(0);
    expect(rendersNoControls(renderer)).toBe(true);
    renderer.unmount();
  });

  it('presents an applied pause with its backend reason and real times', async () => {
    const observedAtMs = 1_700_000_000_000;
    const nextReviewAtMs = 1_700_000_600_000;
    const renderer = await renderSection({
      kind: 'ready',
      status: makeStatus({
        applied: true,
        operation: {
          reason: 'current_gate_confirmed',
          action: 'pause',
          currentApplied: true,
        } as never,
        observedAtMs,
        nextReviewAtMs,
      }),
    });
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
    const renderer = await renderSection({
      kind: 'ready',
      status: makeStatus({
        applied: true,
        operation: { reason: 'gate_future_reason', action: 'pause' } as never,
      }),
    });
    expect(treeText(renderer)).toContain('gate_future_reason');
    renderer.unmount();
  });

  it('never presents a confirmed clear as an applied pause', async () => {
    const renderer = await renderSection({
      kind: 'ready',
      status: makeStatus({
        applied: true,
        operation: {
          reason: 'current_gate_confirmed',
          action: 'clear',
          currentApplied: true,
        } as never,
      }),
    });
    const text = treeText(renderer);
    expect(text).not.toContain('accounts.detail_overview_auto_pause_state_paused');
    expect(findHint(renderer, 'accounts.detail_overview_auto_pause_paused_hint')).toHaveLength(0);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_active');
    renderer.unmount();
  });

  it('names an always-on credential that has no separate policy record', async () => {
    const renderer = await renderSection({
      kind: 'ready',
      status: makeStatus({
        reason: '',
        legacyMigration: { state: 'default_off', reason: 'no_proven_per_subject_legacy_intent' },
      }),
    });
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_no_record');
    expect(text).toContain('accounts.auto_pause_reason_no_policy_record');
    expect(findHint(renderer, 'accounts.detail_overview_auto_pause_no_record_hint')).not.toHaveLength(
      0
    );
    expect(text).not.toContain('accounts.detail_overview_auto_pause_state_unavailable');
    expect(text).not.toContain('accounts.detail_overview_auto_pause_state_active');
    expect(renderer.root.findByType('section').props['data-overview-auto-pause']).toBe('no_record');
    renderer.unmount();
  });

  it('still reports a real pause for a credential without a policy record', async () => {
    const renderer = await renderSection({
      kind: 'ready',
      status: makeStatus({
        applied: true,
        legacyMigration: { state: 'default_off', reason: 'no_proven_per_subject_legacy_intent' },
        operation: {
          reason: 'current_gate_confirmed',
          action: 'pause',
          currentApplied: true,
        } as never,
      }),
    });
    expect(treeText(renderer)).toContain('accounts.detail_overview_auto_pause_state_paused');
    renderer.unmount();
  });

  it('says the state is unreadable when the capability is unavailable', async () => {
    const renderer = await renderSection({
      kind: 'ready',
      status: makeStatus({ availability: { state: 'unavailable', reason: 'gate_core_unavailable' } }),
    });
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
    const renderer = await renderSection({ kind: 'failed' });
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_unavailable');
    expect(text).toContain('accounts.auto_pause_reason_panel_read_failed');
    expect(text).toContain('accounts.detail_overview_auto_pause_observed_missing');
    expect(text).toContain('accounts.detail_overview_auto_pause_next_review_missing');
    renderer.unmount();
  });

  it('reports a missing identity when the drawer has no auth index', async () => {
    const renderer = await renderSection({ kind: 'missing' });
    expect(fetchMock).not.toHaveBeenCalled();
    const text = treeText(renderer);
    expect(text).toContain('accounts.detail_overview_auto_pause_state_unavailable');
    expect(text).toContain('accounts.auto_pause_reason_panel_identity_unavailable');
    renderer.unmount();
  });

  it('keeps a manual credential stop and an automatic pause side by side', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <AccountOverviewTab
          detailView={makeDetailView()}
          getHealthStatusClass={() => ''}
          onSelectTab={() => {}}
          automaticPauseRead={{
            kind: 'ready',
            status: makeStatus({
              applied: true,
              operation: {
                reason: 'current_gate_confirmed',
                action: 'pause',
              } as never,
            }),
          }}
        />
      );
    });
    const text = treeText(renderer);
    expect(text).toContain('accounts.credential_status_manual_disabled');
    expect(text).toContain('accounts.detail_overview_auto_pause_state_paused');
    expect(text).toContain('accounts.auto_pause_reason_current_gate_confirmed');
    expect(fetchMock).not.toHaveBeenCalled();
    renderer.unmount();
  });
});
