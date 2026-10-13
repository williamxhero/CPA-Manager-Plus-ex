import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { InfoTooltip } from '@/components/ui/InfoTooltip';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import type { AccountProcessingPolicy } from '@/services/api/usageService';
import { CredentialAutoStartStopToggle } from './CredentialAutoStartStopToggle';

const { mocks } = vi.hoisted(() => ({
  mocks: {
    getAccountProcessingPolicy: vi.fn(),
    updateAccountProcessingPolicy: vi.fn(),
  },
}));

vi.mock('@/services/api/usageService', () => ({
  usageServiceApi: {
    getAccountProcessingPolicy: mocks.getAccountProcessingPolicy,
    updateAccountProcessingPolicy: mocks.updateAccountProcessingPolicy,
  },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: { message?: string }) =>
      options?.message ? `${key}:${options.message}` : key,
  }),
}));

const MANAGER_BASE = 'http://manager.local:18317';
const MANAGEMENT_KEY = 'manager-key';

const buildPolicy = (
  overrides: { enabled?: boolean; locked?: boolean } = {}
): AccountProcessingPolicy => {
  const enabled = overrides.enabled ?? false;
  const locked = overrides.locked ?? false;
  return {
    source: 'db',
    updatedAtMs: 1,
    codexQuotaCooldown: {
      enabled,
      configured: enabled,
      source: 'db',
      locked,
      envKey: 'USAGE_QUOTA_COOLDOWN_ENABLED',
      configFileKey: 'quotaCooldownEnabled',
    },
    authIssueQueue: {
      enabled: false,
      configured: false,
      source: 'startup',
      locked: false,
      envKey: 'USAGE_ACCOUNT_ACTIONS_ENABLED',
      configFileKey: 'accountActionsEnabled',
    },
    authIssueAutoDisable: {
      enabled: false,
      configured: false,
      source: 'startup',
      locked: false,
      envKey: 'USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE',
      configFileKey: 'accountActionsAutoDisable',
    },
  };
};

const readText = (value: unknown): string => {
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.map(readText).join('');
  if (value && typeof value === 'object' && 'children' in value) {
    return readText((value as { children?: unknown }).children);
  }
  if (value && typeof value === 'object' && 'props' in value) {
    return readText((value as ReactTestInstance).props.children);
  }
  return '';
};

const pageText = (renderer: ReactTestRenderer) => readText(renderer.toJSON());

const flush = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

const render = async (props: { managerServiceBase?: string; managementKey?: string } = {}) => {
  let renderer: ReactTestRenderer | null = null;
  await act(async () => {
    renderer = create(
      <CredentialAutoStartStopToggle
        managerServiceBase={props.managerServiceBase ?? MANAGER_BASE}
        managementKey={props.managementKey ?? MANAGEMENT_KEY}
      />
    );
  });
  await flush();
  return renderer!;
};

const findToggle = (renderer: ReactTestRenderer) => renderer.root.findAllByType(ToggleSwitch)[0];

afterEach(() => {
  mocks.getAccountProcessingPolicy.mockReset();
  mocks.updateAccountProcessingPolicy.mockReset();
});

describe('CredentialAutoStartStopToggle', () => {
  it('shows the persisted ON state and persists OFF through the shared policy API', async () => {
    mocks.getAccountProcessingPolicy.mockResolvedValue(buildPolicy({ enabled: true }));
    mocks.updateAccountProcessingPolicy.mockResolvedValue(buildPolicy({ enabled: false }));
    const renderer = await render();

    expect(mocks.getAccountProcessingPolicy).toHaveBeenCalledWith(MANAGER_BASE, MANAGEMENT_KEY);
    expect(findToggle(renderer).props.checked).toBe(true);
    expect(findToggle(renderer).props.disabled).toBe(false);
    expect(pageText(renderer)).toContain('accounts.auto_start_stop');
    expect(findToggle(renderer).props.labelPosition).toBe('left');
    expect(renderer.root.findByType(InfoTooltip).props.content).toBe(
      'accounts.auto_start_stop_on_hint'
    );

    await act(async () => {
      findToggle(renderer).props.onChange(false);
      await Promise.resolve();
    });
    await flush();

    expect(mocks.updateAccountProcessingPolicy).toHaveBeenCalledWith(MANAGER_BASE, MANAGEMENT_KEY, {
      codexQuotaCooldownEnabled: false,
    });
    expect(findToggle(renderer).props.checked).toBe(false);
    expect(renderer.root.findByType(InfoTooltip).props.content).toBe(
      'accounts.auto_start_stop_off_hint'
    );
  });

  it('reflects the persisted OFF state and honestly describes the OFF gating', async () => {
    mocks.getAccountProcessingPolicy.mockResolvedValue(buildPolicy({ enabled: false }));
    const renderer = await render();

    expect(findToggle(renderer).props.checked).toBe(false);
    expect(renderer.root.findByProps({ 'data-credential-auto-start-stop': 'ready' })).toBeTruthy();
    expect(renderer.root.findByType(InfoTooltip).props.content).toBe(
      'accounts.auto_start_stop_off_hint'
    );
  });

  it('keeps the persisted value and surfaces a save failure instead of a fake state', async () => {
    mocks.getAccountProcessingPolicy.mockResolvedValue(buildPolicy({ enabled: true }));
    mocks.updateAccountProcessingPolicy.mockRejectedValue(new Error('boom'));
    const renderer = await render();

    await act(async () => {
      findToggle(renderer).props.onChange(false);
      await Promise.resolve();
    });
    await flush();

    expect(findToggle(renderer).props.checked).toBe(true);
    const alert = renderer.root.findByProps({ role: 'alert' });
    expect(readText(alert)).toBe('accounts.auto_start_stop_save_failed:boom');
  });

  it('disables the switch and reports the environment lock', async () => {
    mocks.getAccountProcessingPolicy.mockResolvedValue(
      buildPolicy({ enabled: true, locked: true })
    );
    const renderer = await render();

    expect(findToggle(renderer).props.disabled).toBe(true);
    expect(renderer.root.findByType(InfoTooltip).props.content).toBe(
      'accounts.auto_start_stop_locked_hint'
    );
  });

  it('disables the switch and reports a load failure honestly', async () => {
    mocks.getAccountProcessingPolicy.mockRejectedValue(new Error('offline'));
    const renderer = await render();

    expect(findToggle(renderer).props.disabled).toBe(true);
    const errorNode = renderer.root.findByProps({
      'data-credential-auto-start-stop': 'error',
    });
    expect(errorNode.findByType(InfoTooltip).props.content).toBe(
      'accounts.auto_start_stop_load_failed:offline'
    );
  });

  it('is unavailable when no Manager service base is configured', async () => {
    const renderer = await render({ managerServiceBase: '', managementKey: '' });

    expect(mocks.getAccountProcessingPolicy).not.toHaveBeenCalled();
    expect(findToggle(renderer).props.disabled).toBe(true);
    expect(renderer.root.findByType(InfoTooltip).props.content).toBe(
      'accounts.auto_start_stop_unavailable'
    );
  });
});
