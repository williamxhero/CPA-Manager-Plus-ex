import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CodexReauthDialog } from './CodexReauthDialog';
import { CodexReauthReconciliationError } from './codexReauthModel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { mocks } = vi.hoisted(() => ({
  mocks: {
    startCodexReauth: vi.fn(),
    cancelSession: vi.fn(),
    getAuthStatus: vi.fn(),
    submitCallback: vi.fn(),
    showNotification: vi.fn(),
    translate: vi.fn((key: string) => key),
    intervalCallback: null as null | (() => void | Promise<void>),
  },
}));

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => undefined },
  useTranslation: () => ({ t: mocks.translate }),
}));

vi.mock('@/services/api', () => ({
  oauthApi: {
    startCodexReauth: mocks.startCodexReauth,
    cancelSession: mocks.cancelSession,
    getAuthStatus: mocks.getAuthStatus,
    submitCallback: mocks.submitCallback,
  },
}));

vi.mock('@/stores', () => ({
  useNotificationStore: (
    selector: (state: { showNotification: typeof mocks.showNotification }) => unknown
  ) => selector({ showNotification: mocks.showNotification }),
}));

vi.mock('@/components/ui/Modal', () => ({
  Modal: ({
    open,
    children,
    footer,
  }: {
    open: boolean;
    children?: ReactNode;
    footer?: ReactNode;
  }) =>
    open ? (
      <section>
        {children}
        <footer>{footer}</footer>
      </section>
    ) : null,
}));

vi.mock('@/components/ui/Button', () => ({
  Button: ({
    children,
    loading: _loading,
    ...props
  }: ButtonHTMLAttributes<HTMLButtonElement> & {
    loading?: boolean;
  }) => <button {...props}>{children}</button>,
}));

vi.mock('@/components/ui/icons', () => ({
  IconCheck: () => null,
  IconCopy: () => null,
  IconExternalLink: () => null,
  IconRefreshCw: () => null,
}));

vi.mock('@/utils/clipboard', () => ({
  copyToClipboard: vi.fn(async () => true),
}));

const TARGET = {
  account: 'codex@example.com',
  fileName: 'codex.json',
  runtimeId: 'runtime-auth-1',
  accountId: 'acct-1',
  accountSnapshot: 'alice@example.com',
  provider: 'codex',
  authIndex: 'auth-1',
};
const AUTH_LINK = {
  url: 'https://auth.example/codex',
  state: 'state-1',
  target_auth_id: TARGET.runtimeId,
};
// Core only reports adoption explicitly; completion alone is not evidence.
const OK_STATUS = {
  status: 'ok' as const,
  auth_id: TARGET.runtimeId,
  saved: true,
  adopted: true,
};
const REQUEST_SCOPE = {
  apiBase: 'http://cpa-a.local:8317',
  managementKey: 'cpa-key-a',
};
const NEXT_REQUEST_SCOPE = {
  apiBase: 'http://cpa-b.local:8317',
  managementKey: 'cpa-key-b',
};

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((nextResolve) => {
    resolve = nextResolve;
  });
  return { promise, resolve };
};

const flush = async () => {
  await new Promise((resolve) => globalThis.setTimeout(resolve, 0));
  await Promise.resolve();
};

const textContent = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : textContent(child))).join('');

const findButton = (renderer: ReactTestRenderer, text: string) => {
  const button = renderer.root
    .findAllByType('button')
    .find((candidate) => textContent(candidate).includes(text));
  if (!button) throw new Error(`Button not found: ${text}`);
  return button;
};

const flushEffects = async () => {
  await act(async () => {
    await flush();
  });
};

describe('CodexReauthDialog connection lifecycle', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.intervalCallback = null;
    vi.stubGlobal('window', {
      setTimeout: (callback: () => void, delay?: number) =>
        globalThis.setTimeout(callback, delay) as unknown as number,
      clearTimeout: (timer: number) => globalThis.clearTimeout(timer),
      setInterval: vi.fn((callback: () => void | Promise<void>) => {
        mocks.intervalCallback = callback;
        return 1;
      }),
      clearInterval: vi.fn(),
      open: vi.fn(),
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('restarts the OAuth link when the targeted Workspace member changes', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
        />
      );
    });
    await flushEffects();
    expect(mocks.startCodexReauth).toHaveBeenCalledTimes(1);

    await act(async () => {
      renderer.update(
        <CodexReauthDialog
          open
          target={{ ...TARGET, accountSnapshot: 'bob@example.com' }}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
        />
      );
    });
    await flushEffects();

    expect(mocks.startCodexReauth).toHaveBeenCalledTimes(2);
    act(() => renderer.unmount());
  });

  it('ignores a late auth-link response after the CPA scope changes', async () => {
    const oldRequest = deferred<typeof AUTH_LINK>();
    const newRequest = deferred<typeof AUTH_LINK>();
    mocks.startCodexReauth.mockImplementation(
      (_selector: { authIndex: string }, requestScope: typeof REQUEST_SCOPE) =>
        requestScope.apiBase === REQUEST_SCOPE.apiBase ? oldRequest.promise : newRequest.promise
    );

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog open target={TARGET} requestScope={REQUEST_SCOPE} onClose={vi.fn()} />
      );
    });
    await flushEffects();
    expect(mocks.startCodexReauth).toHaveBeenCalledWith({ authIndex: TARGET.authIndex }, REQUEST_SCOPE);

    await act(async () => {
      renderer.update(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={NEXT_REQUEST_SCOPE}
          onClose={vi.fn()}
        />
      );
    });
    await flushEffects();
    expect(mocks.startCodexReauth).toHaveBeenCalledWith(
      { authIndex: TARGET.authIndex },
      NEXT_REQUEST_SCOPE
    );

    await act(async () => {
      oldRequest.resolve({
        url: 'https://auth.example/old',
        state: 'old-state',
        target_auth_id: TARGET.runtimeId,
      });
      await Promise.resolve();
    });
    expect(mocks.intervalCallback).toBeNull();
    expect(mocks.cancelSession).toHaveBeenCalledWith('old-state', REQUEST_SCOPE);

    await act(async () => {
      newRequest.resolve({
        url: 'https://auth.example/new',
        state: 'new-state',
        target_auth_id: TARGET.runtimeId,
      });
      await Promise.resolve();
    });
    expect(textContent(renderer.root)).toContain('https://auth.example/new');
    expect(textContent(renderer.root)).not.toContain('https://auth.example/old');
    expect(mocks.intervalCallback).not.toBeNull();

    act(() => renderer.unmount());
  });

  it('ignores a late polling success after the dialog closes', async () => {
    const pollRequest = deferred<typeof OK_STATUS>();
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.getAuthStatus.mockReturnValue(pollRequest.promise);
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();
    expect(mocks.intervalCallback).not.toBeNull();

    let pollingPromise!: Promise<void>;
    act(() => {
      pollingPromise = Promise.resolve(mocks.intervalCallback?.());
    });
    await flushEffects();
    expect(mocks.getAuthStatus).toHaveBeenCalledWith('state-1', REQUEST_SCOPE);

    await act(async () => {
      renderer.update(
        <CodexReauthDialog
          open={false}
          target={null}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await act(async () => {
      pollRequest.resolve(OK_STATUS);
      await pollingPromise;
    });

    expect(onSuccess).not.toHaveBeenCalled();
    expect(mocks.showNotification).not.toHaveBeenCalledWith('codex_reauth.success', 'success');

    act(() => renderer.unmount());
  });

  it('ignores a late callback success after the CPA scope changes', async () => {
    const callbackRequest = deferred<{ status: 'ok' }>();
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockReturnValue(callbackRequest.promise);
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    const input = renderer.root.findByType('input');
    act(() => input.props.onChange({ target: { value: 'http://localhost/callback?code=1' } }));
    await act(async () => {
      findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
      await Promise.resolve();
    });
    expect(mocks.submitCallback).toHaveBeenCalledWith(
      'codex',
      'http://localhost/callback?code=1',
      REQUEST_SCOPE
    );

    await act(async () => {
      renderer.update(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={NEXT_REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await act(async () => {
      callbackRequest.resolve({ status: 'ok' });
      await Promise.resolve();
    });

    expect(onSuccess).not.toHaveBeenCalled();
    expect(mocks.showNotification).not.toHaveBeenCalledWith('codex_reauth.success', 'success');

    act(() => renderer.unmount());
  });

  it('keeps polling the original state when an accepted callback is still pending', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockResolvedValue({ status: 'ok' });
    mocks.getAuthStatus.mockResolvedValue({ status: 'wait' });
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    act(() =>
      renderer.root
        .findByType('input')
        .props.onChange({ target: { value: 'http://localhost/callback?code=1' } })
    );
    await act(async () => {
      await findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
    });

    expect(onSuccess).not.toHaveBeenCalled();
    expect(mocks.getAuthStatus).toHaveBeenCalledWith('state-1', REQUEST_SCOPE);
    expect(mocks.intervalCallback).not.toBeNull();
    expect(textContent(renderer.root)).toContain('codex_reauth.callback_accepted');

    act(() => renderer.unmount());
  });

  it('completes exactly once when the immediate callback probe confirms OAuth', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockResolvedValue({ status: 'ok' });
    mocks.getAuthStatus.mockResolvedValue(OK_STATUS);
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();
    act(() =>
      renderer.root
        .findByType('input')
        .props.onChange({ target: { value: 'http://localhost/callback?code=1' } })
    );
    await act(async () => {
      await findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
    });

    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(textContent(renderer.root)).toContain('codex_reauth.success');

    act(() => renderer.unmount());
  });

  it('ignores a late callback response after polling already completed OAuth', async () => {
    const callbackRequest = deferred<{ status: 'ok' }>();
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockReturnValue(callbackRequest.promise);
    mocks.getAuthStatus.mockResolvedValue(OK_STATUS);
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();
    act(() =>
      renderer.root
        .findByType('input')
        .props.onChange({ target: { value: 'http://localhost/callback?code=1' } })
    );
    let callbackPromise!: Promise<void>;
    act(() => {
      callbackPromise = Promise.resolve(
        findButton(renderer, 'codex_reauth.submit_callback').props.onClick()
      );
    });
    await flushEffects();
    await act(async () => {
      await mocks.intervalCallback?.();
    });
    await act(async () => {
      callbackRequest.resolve({ status: 'ok' });
      await callbackPromise;
    });

    expect(onSuccess).toHaveBeenCalledTimes(1);

    act(() => renderer.unmount());
  });

  it('confirms a 409 callback through the original OAuth state', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockRejectedValue(
      Object.assign(new Error('oauth flow is already completed'), { status: 409 })
    );
    mocks.getAuthStatus.mockResolvedValue(OK_STATUS);
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();
    act(() =>
      renderer.root
        .findByType('input')
        .props.onChange({ target: { value: 'http://localhost/callback?code=1' } })
    );
    await act(async () => {
      await findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
    });

    expect(mocks.getAuthStatus).toHaveBeenCalledWith('state-1', REQUEST_SCOPE);
    expect(onSuccess).toHaveBeenCalledTimes(1);

    act(() => renderer.unmount());
  });

  it('keeps waiting when a 409 callback still has pending OAuth status', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockRejectedValue(
      Object.assign(new Error('oauth flow is already completed'), { status: 409 })
    );
    mocks.getAuthStatus.mockResolvedValue({ status: 'wait' });
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();
    act(() =>
      renderer.root
        .findByType('input')
        .props.onChange({ target: { value: 'http://localhost/callback?code=1' } })
    );
    await act(async () => {
      await findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
    });

    expect(onSuccess).not.toHaveBeenCalled();
    expect(mocks.intervalCallback).not.toBeNull();
    expect(textContent(renderer.root)).toContain('codex_reauth.callback_accepted');

    act(() => renderer.unmount());
  });

  it('waits for Accounts synchronization before announcing re-login success', async () => {
    const synchronization = deferred<void>();
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockResolvedValue({ status: 'ok' });
    mocks.getAuthStatus.mockResolvedValue(OK_STATUS);
    const onSuccess = vi.fn(() => synchronization.promise);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    const input = renderer.root.findByType('input');
    act(() => input.props.onChange({ target: { value: 'http://localhost/callback?code=1' } }));
    await act(async () => {
      findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
      await Promise.resolve();
    });

    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(textContent(renderer.root)).toContain('codex_reauth.synchronizing');
    expect(mocks.showNotification).not.toHaveBeenCalledWith('codex_reauth.success', 'success');

    await act(async () => {
      synchronization.resolve();
      await flush();
    });

    expect(mocks.showNotification).toHaveBeenCalledWith('codex_reauth.success', 'success');
    expect(textContent(renderer.root)).toContain('codex_reauth.success');

    act(() => renderer.unmount());
  });

  it('reports re-login success with a synchronization warning when Accounts reload fails', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockResolvedValue({ status: 'ok' });
    mocks.getAuthStatus.mockResolvedValue(OK_STATUS);
    const onSuccess = vi.fn().mockRejectedValue(new Error('temporary Accounts reload failure'));

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    const input = renderer.root.findByType('input');
    act(() => input.props.onChange({ target: { value: 'http://localhost/callback?code=1' } }));
    await act(async () => {
      findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(mocks.showNotification).toHaveBeenCalledWith(
      expect.stringContaining('temporary Accounts reload failure'),
      'warning'
    );
    expect(mocks.showNotification).not.toHaveBeenCalledWith(
      expect.stringContaining('codex_reauth.error'),
      'error'
    );
    expect(textContent(renderer.root)).toContain('temporary Accounts reload failure');

    act(() => renderer.unmount());
  });

  it('keeps identity reconciliation failures in the error state', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockResolvedValue({ status: 'ok' });
    mocks.getAuthStatus.mockResolvedValue(OK_STATUS);
    const onSuccess = vi.fn().mockRejectedValue(
      new CodexReauthReconciliationError('identity_changed', 'identity changed')
    );

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    const input = renderer.root.findByType('input');
    act(() => input.props.onChange({ target: { value: 'http://localhost/callback?code=1' } }));
    await act(async () => {
      findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(textContent(renderer.root)).toContain('identity changed');
    expect(mocks.showNotification).toHaveBeenCalledWith('identity changed', 'error');
    expect(mocks.showNotification).not.toHaveBeenCalledWith(
      expect.stringContaining('codex_reauth.success'),
      'warning'
    );

    act(() => renderer.unmount());
  });

  it('does not restart OAuth while Accounts is still synchronizing after a successful callback', async () => {
    const synchronization = deferred<void>();
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.submitCallback.mockResolvedValue({ status: 'ok' });
    mocks.getAuthStatus.mockResolvedValue(OK_STATUS);
    const onSuccess = vi.fn(() => synchronization.promise);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();
    const startCount = mocks.startCodexReauth.mock.calls.length;

    const input = renderer.root.findByType('input');
    act(() => input.props.onChange({ target: { value: 'http://localhost/callback?code=1' } }));
    await act(async () => {
      findButton(renderer, 'codex_reauth.submit_callback').props.onClick();
      await Promise.resolve();
    });

    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(textContent(renderer.root)).toContain('codex_reauth.synchronizing');
    expect(mocks.showNotification).not.toHaveBeenCalledWith('codex_reauth.success', 'success');

    await act(async () => {
      renderer.update(
        <CodexReauthDialog
          open
          target={{ ...TARGET, authIndex: 'auth-2' }}
          requestScope={{ ...REQUEST_SCOPE }}
          onClose={vi.fn()}
          onSuccess={vi.fn()}
        />
      );
    });
    await flushEffects();

    expect(mocks.startCodexReauth).toHaveBeenCalledTimes(startCount);
    expect(textContent(renderer.root)).toContain('codex_reauth.synchronizing');

    await act(async () => {
      synchronization.resolve();
      await flush();
    });

    expect(mocks.startCodexReauth).toHaveBeenCalledTimes(startCount);
    expect(mocks.showNotification).toHaveBeenCalledWith('codex_reauth.success', 'success');
    expect(textContent(renderer.root)).toContain('codex_reauth.success');

    act(() => renderer.unmount());
  });

  it('does not restart OAuth while Accounts is still synchronizing after polling success', async () => {
    const synchronization = deferred<void>();
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.getAuthStatus.mockResolvedValue(OK_STATUS);
    const onSuccess = vi.fn(() => synchronization.promise);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();
    const startCount = mocks.startCodexReauth.mock.calls.length;
    expect(mocks.intervalCallback).not.toBeNull();

    let pollingPromise!: Promise<void>;
    act(() => {
      pollingPromise = Promise.resolve(mocks.intervalCallback?.());
    });
    await flushEffects();

    expect(onSuccess).toHaveBeenCalledTimes(1);
    expect(textContent(renderer.root)).toContain('codex_reauth.synchronizing');
    expect(mocks.showNotification).not.toHaveBeenCalledWith('codex_reauth.success', 'success');

    await act(async () => {
      renderer.update(
        <CodexReauthDialog
          open
          target={{ ...TARGET, authIndex: 'auth-2' }}
          requestScope={{ ...REQUEST_SCOPE }}
          onClose={vi.fn()}
          onSuccess={vi.fn()}
        />
      );
    });
    await flushEffects();

    expect(mocks.startCodexReauth).toHaveBeenCalledTimes(startCount);
    expect(textContent(renderer.root)).toContain('codex_reauth.synchronizing');

    await act(async () => {
      synchronization.resolve();
      await pollingPromise;
      await flush();
    });

    expect(mocks.startCodexReauth).toHaveBeenCalledTimes(startCount);
    expect(mocks.showNotification).toHaveBeenCalledWith('codex_reauth.success', 'success');
    expect(textContent(renderer.root)).toContain('codex_reauth.success');

    act(() => renderer.unmount());
  });

  it('drops a session Core authorized for a different credential', async () => {
    mocks.startCodexReauth.mockResolvedValue({
      ...AUTH_LINK,
      target_auth_id: 'runtime-other',
    });
    mocks.cancelSession.mockResolvedValue({ status: 'ok', cancelled: true });
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    expect(mocks.cancelSession).toHaveBeenCalledWith(AUTH_LINK.state, REQUEST_SCOPE);
    expect(mocks.intervalCallback).toBeNull();
    expect(textContent(renderer.root)).toContain('codex_reauth.target_mismatch');
    expect(textContent(renderer.root)).not.toContain(AUTH_LINK.url);
    expect(onSuccess).not.toHaveBeenCalled();

    act(() => renderer.unmount());
  });

  it('keeps polling while Core reports the credential as saved but not adopted', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    const onSuccess = vi.fn();
    mocks.getAuthStatus.mockResolvedValue({ ...OK_STATUS, adopted: false });

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    await act(async () => {
      await mocks.intervalCallback?.();
    });
    expect(onSuccess).not.toHaveBeenCalled();
    expect(mocks.showNotification).not.toHaveBeenCalledWith('codex_reauth.success', 'success');

    // The pending window is bounded: an adoption that never arrives fails closed.
    mocks.getAuthStatus.mockResolvedValue({ ...OK_STATUS, adopted: false });
    for (let attempt = 0; attempt < 20; attempt += 1) {
      await act(async () => {
        await mocks.intervalCallback?.();
      });
    }
    expect(onSuccess).not.toHaveBeenCalled();
    expect(textContent(renderer.root)).toContain('codex_reauth.identity_unconfirmed');

    act(() => renderer.unmount());
  });

  it('fails closed when the completion reports another credential', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.getAuthStatus.mockResolvedValue({ ...OK_STATUS, auth_id: 'runtime-other' });
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    await act(async () => {
      await mocks.intervalCallback?.();
    });

    expect(onSuccess).not.toHaveBeenCalled();
    expect(textContent(renderer.root)).toContain('codex_reauth.target_mismatch');
    expect(mocks.showNotification).toHaveBeenCalledWith('codex_reauth.target_mismatch', 'error');

    act(() => renderer.unmount());
  });

  it('treats a completion without adoption evidence as unconfirmed', async () => {
    mocks.startCodexReauth.mockResolvedValue(AUTH_LINK);
    mocks.getAuthStatus.mockResolvedValue({ status: 'ok', auth_id: TARGET.runtimeId });
    const onSuccess = vi.fn();

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CodexReauthDialog
          open
          target={TARGET}
          requestScope={REQUEST_SCOPE}
          onClose={vi.fn()}
          onSuccess={onSuccess}
        />
      );
    });
    await flushEffects();

    await act(async () => {
      await mocks.intervalCallback?.();
    });

    expect(onSuccess).not.toHaveBeenCalled();
    expect(textContent(renderer.root)).toContain('codex_reauth.identity_unconfirmed');

    act(() => renderer.unmount());
  });
});
