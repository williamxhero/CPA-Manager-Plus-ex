import { readFileSync } from 'node:fs';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it, vi } from 'vitest';
import type { CredentialPolicyStatus } from '@/services/api/credentialPolicy';
import type { CredentialQuotaStatus } from '@/services/api/credentialQuota';
import { CredentialAutoStartStopToggle } from './CredentialAutoStartStopToggle';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
const directory = process.env.POLICY_QUERY_SUBJECT_UI_FIXTURE;

// Opt-in existing bridge: real HTTP/SQLite/Core/TLS, never mocked fetch or accounts.
it.skipIf(!directory)(
  'real policy row refresh queries A by issued query subject and preserves A/B policy identities',
  async () => {
    const fixture: { base: string; sources: Record<string, { auth_index: string }> } = JSON.parse(
      readFileSync(`${directory}/browser-bridge.json`, 'utf8')
    );
    const managementKey = 'cpamp_test_key_0123456789abcdef';
    const read = async <T,>(path: string): Promise<T> => {
      const response = await fetch(fixture.base + path, {
        headers: { Authorization: `Bearer ${managementKey}` },
        cache: 'no-store',
        redirect: 'error',
      });
      expect(response.ok).toBe(true);
      return response.json();
    };
    const policyPath = `/usage-service/credential-policy?authIndex=${fixture.sources.A.auth_index}`;
    const quotaPath = `/usage-service/credential-quota?authIndex=${fixture.sources.A.auth_index}`;
    const before = await read<CredentialPolicyStatus>(policyPath);
    const quotaBefore = await read<CredentialQuotaStatus>(quotaPath);
    expect(before.subjectId).not.toBe(quotaBefore.subjectId);
    expect(before.querySubjectId).toBe(quotaBefore.subjectId);
    expect(before.enabled).toBe(false);
    let renderer!: ReactTestRenderer;
    const ready = async () => {
      for (let i = 0; i < 300; i++) {
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 20));
        });
        if (
          renderer.root.findAllByProps({ 'data-credential-auto-start-stop': 'ready' }).length === 2
        )
          return;
      }
      expect(renderer.root.findAllByProps({ 'data-credential-auto-start-stop': 'ready' })).toHaveLength(
        2
      );
    };
    try {
      await act(async () => {
        renderer = create(
          <>
            {['A', 'B'].map((name) => (
              <CredentialAutoStartStopToggle
                key={name}
                managerServiceBase={fixture.base}
                managementKey={managementKey}
                authIndex={fixture.sources[name].auth_index}
              />
            ))}
          </>
        );
      });
      await ready();
      await act(async () => {
        const refresh = renderer.root.findAllByType('button')[0];
        expect(refresh.props.disabled).toBe(false);
        refresh.props.onClick();
      });
      await ready();
      expect(renderer.root.findAllByProps({ role: 'alert' })).toHaveLength(0);
      const quota = await read<CredentialQuotaStatus>(quotaPath);
      expect(quota.version).toBe(quotaBefore.version + 1);
      expect(quota.stale).toBe(false);
      expect(quota.lastSuccess?.queryStatus).toBe('success');
      const after = await read<CredentialPolicyStatus>(policyPath);
      expect(after.subjectId).toBe(before.subjectId);
      expect(after.querySubjectId).toBe(quota.subjectId);
      expect(after.enabled).toBe(false);
      await act(async () => {
        const toggle = renderer.root.findAllByType('input')[0];
        expect(toggle.props.disabled).toBe(false);
        toggle.props.onChange({ target: { checked: true } });
      });
      await ready();
      expect(renderer.root.findAllByProps({ role: 'alert' })).toHaveLength(0);
      const saved = await read<CredentialPolicyStatus>(policyPath);
      expect(saved.subjectId).toBe(before.subjectId);
      expect(saved.querySubjectId).toBe(quota.subjectId);
      expect(saved.enabled).toBe(true);
      expect(saved.generation).toBe(before.generation + 1);
      expect(renderer.root.findAllByType('input').map((input) => input.props.checked)).toEqual([
        true,
        false,
      ]);
      const other = await read<CredentialQuotaStatus>(
        `/usage-service/credential-quota?authIndex=${fixture.sources.B.auth_index}`
      );
      expect(other.version).toBe(0);
      expect(other.lastSuccess).toBeUndefined();
    } finally {
      if (renderer) await act(async () => renderer.unmount());
    }
  },
  15000
);
