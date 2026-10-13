import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  publishAccountCredentialMutationRevision,
  useAccountCredentialMutationRevisionStore,
} from '@/stores/useAccountCredentialMutationRevisionStore';
import { useAccountCredentialMutationSubscription } from './useAccountCredentialMutationSubscription';

function SubscriptionHarness({
  connectionFingerprint,
  synchronize,
}: {
  connectionFingerprint: string;
  synchronize: () => void | Promise<unknown>;
}) {
  useAccountCredentialMutationSubscription(connectionFingerprint, synchronize);
  return null;
}

describe('useAccountCredentialMutationSubscription', () => {
  let renderer: ReactTestRenderer | undefined;

  beforeEach(() => {
    useAccountCredentialMutationRevisionStore.getState().clearForTests();
  });

  afterEach(async () => {
    await act(async () => renderer?.unmount());
    renderer = undefined;
    useAccountCredentialMutationRevisionStore.getState().clearForTests();
  });

  const mount = async (
    connectionFingerprint: string,
    synchronize: () => void | Promise<unknown>
  ) => {
    await act(async () => {
      renderer = create(
        <SubscriptionHarness
          connectionFingerprint={connectionFingerprint}
          synchronize={synchronize}
        />
      );
    });
  };

  it('synchronizes each new matching credential revision without replaying existing events', async () => {
    const synchronize = vi.fn(async () => false);
    const event = {
      connectionFingerprint: 'connection-a',
      provider: 'qwen',
      kind: 'credential' as const,
    };
    publishAccountCredentialMutationRevision(event);
    await mount('connection-a', synchronize);
    expect(synchronize).not.toHaveBeenCalled();

    await act(async () => publishAccountCredentialMutationRevision(event));
    expect(synchronize).toHaveBeenCalledTimes(1);
    expect(synchronize).toHaveBeenLastCalledWith();

    await act(async () => publishAccountCredentialMutationRevision(event));
    expect(synchronize).toHaveBeenCalledTimes(2);

    await act(async () => publishAccountCredentialMutationRevision({ ...event, provider: 'kimi' }));
    expect(synchronize).toHaveBeenCalledTimes(3);
  });

  it('ignores other scopes and non-credential events without replaying an unchanged revision', async () => {
    const synchronize = vi.fn(async () => false);
    await mount('connection-a', synchronize);

    await act(async () => {
      publishAccountCredentialMutationRevision({
        connectionFingerprint: 'connection-b',
        provider: 'qwen',
        kind: 'credential',
      });
      for (const kind of ['oauth', 'quota', 'reauth'] as const) {
        publishAccountCredentialMutationRevision({
          connectionFingerprint: 'connection-a',
          provider: 'qwen',
          kind,
        });
      }
    });
    expect(synchronize).not.toHaveBeenCalled();

    await act(async () =>
      publishAccountCredentialMutationRevision({
        connectionFingerprint: 'connection-a',
        provider: 'qwen',
        kind: 'credential',
      })
    );
    expect(synchronize).toHaveBeenCalledTimes(1);

    await act(async () =>
      publishAccountCredentialMutationRevision({
        connectionFingerprint: 'connection-a',
        provider: 'kimi',
        kind: 'oauth',
      })
    );
    expect(synchronize).toHaveBeenCalledTimes(1);
  });

  it('uses the latest callback and unsubscribes from the previous scope', async () => {
    const firstSynchronize = vi.fn();
    const latestSynchronize = vi.fn();
    await mount('connection-a', firstSynchronize);

    await act(async () => {
      renderer!.update(
        <SubscriptionHarness connectionFingerprint="connection-a" synchronize={latestSynchronize} />
      );
    });
    await act(async () =>
      publishAccountCredentialMutationRevision({
        connectionFingerprint: 'connection-a',
        provider: 'qwen',
        kind: 'credential',
      })
    );
    expect(firstSynchronize).not.toHaveBeenCalled();
    expect(latestSynchronize).toHaveBeenCalledTimes(1);

    await act(async () => {
      renderer!.update(
        <SubscriptionHarness connectionFingerprint="connection-b" synchronize={latestSynchronize} />
      );
    });
    await act(async () => {
      publishAccountCredentialMutationRevision({
        connectionFingerprint: 'connection-a',
        provider: 'qwen',
        kind: 'credential',
      });
      publishAccountCredentialMutationRevision({
        connectionFingerprint: 'connection-b',
        provider: 'qwen',
        kind: 'credential',
      });
    });
    expect(latestSynchronize).toHaveBeenCalledTimes(2);
  });

  it('does not synchronize after unmount', async () => {
    const synchronize = vi.fn();
    await mount('connection-a', synchronize);
    await act(async () => renderer!.unmount());
    renderer = undefined;

    await act(async () =>
      publishAccountCredentialMutationRevision({
        connectionFingerprint: 'connection-a',
        provider: 'qwen',
        kind: 'credential',
      })
    );
    expect(synchronize).not.toHaveBeenCalled();
  });

  it('does not subscribe without a connection fingerprint', async () => {
    const synchronize = vi.fn();
    await mount('', synchronize);

    await act(async () =>
      publishAccountCredentialMutationRevision({
        connectionFingerprint: 'connection-a',
        provider: 'qwen',
        kind: 'credential',
      })
    );
    expect(synchronize).not.toHaveBeenCalled();
  });
});
