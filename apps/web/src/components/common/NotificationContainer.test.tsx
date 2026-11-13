import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Notification } from '@/types';

const { mocks } = vi.hoisted(() => ({
  mocks: {
    notifications: [] as Notification[],
    removeNotification: vi.fn(),
    pauseNotificationDismiss: vi.fn(),
    resumeNotificationDismiss: vi.fn(),
    portalTarget: null as unknown,
  },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}));

vi.mock('react-dom', async () => {
  const actual = await vi.importActual<typeof import('react-dom')>('react-dom');
  return {
    ...actual,
    createPortal: (children: unknown, target: unknown) => {
      mocks.portalTarget = target;
      return children;
    },
  };
});

vi.mock('@/stores', () => ({
  useNotificationStore: (
    selector?: (state: {
      notifications: Notification[];
      removeNotification: typeof mocks.removeNotification;
      pauseNotificationDismiss: typeof mocks.pauseNotificationDismiss;
      resumeNotificationDismiss: typeof mocks.resumeNotificationDismiss;
    }) => unknown
  ) => {
    const state = {
      notifications: mocks.notifications,
      removeNotification: mocks.removeNotification,
      pauseNotificationDismiss: mocks.pauseNotificationDismiss,
      resumeNotificationDismiss: mocks.resumeNotificationDismiss,
    };
    return selector ? selector(state) : state;
  },
}));

vi.mock('@/components/ui/icons', () => ({
  IconX: () => null,
  IconCopy: () => null,
}));

import { NotificationContainer } from './NotificationContainer';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe('NotificationContainer', () => {
  let renderer: ReactTestRenderer | undefined;
  let body: Record<string, unknown>;
  let restoreDocument: (() => void) | undefined;

  beforeEach(() => {
    vi.useFakeTimers();
    mocks.notifications = [];
    mocks.removeNotification.mockReset();
    mocks.pauseNotificationDismiss.mockReset();
    mocks.resumeNotificationDismiss.mockReset();
    mocks.portalTarget = null;

    const previousDocument = Object.getOwnPropertyDescriptor(globalThis, 'document');
    body = {};
    Object.defineProperty(globalThis, 'document', {
      configurable: true,
      value: { body },
    });
    restoreDocument = () => {
      if (previousDocument) {
        Object.defineProperty(globalThis, 'document', previousDocument);
      } else {
        Reflect.deleteProperty(globalThis, 'document');
      }
    };
  });

  afterEach(() => {
    act(() => renderer?.unmount());
    renderer = undefined;
    restoreDocument?.();
    restoreDocument = undefined;
    vi.useRealTimers();
  });

  it('portals notifications to document.body without changing their content or type', async () => {
    mocks.notifications = [
      {
        id: 'notification-1',
        message: '401 Unauthorized',
        type: 'error',
      },
    ];

    await act(async () => {
      renderer = create(<NotificationContainer />);
      await Promise.resolve();
    });

    expect(mocks.portalTarget).toBe(body);
    const notification = renderer!.root.find(
      (node) =>
        typeof node.props.className === 'string' &&
        node.props.className.includes('notification error')
    );
    expect(notification.props.children[0].props.children).toBe('401 Unauthorized');
  });

  it('keeps close behavior wired to removeNotification after the exit animation', async () => {
    mocks.notifications = [
      {
        id: 'notification-1',
        message: 'Saved',
        type: 'success',
      },
    ];

    await act(async () => {
      renderer = create(<NotificationContainer />);
      await Promise.resolve();
    });

    await act(async () => {
      const closeButton = renderer!.root.find(
        (node) =>
          typeof node.props.className === 'string' &&
          node.props.className.includes('close-btn')
      );
      closeButton.props.onClick();
      vi.advanceTimersByTime(300);
    });

    expect(mocks.removeNotification).toHaveBeenCalledWith('notification-1');
  });

  it('pauses auto-dismiss while hovered and resumes after leave', async () => {
    mocks.notifications = [
      {
        id: 'notification-1',
        message: 'Saved',
        type: 'success',
      },
    ];

    await act(async () => {
      renderer = create(<NotificationContainer />);
      await Promise.resolve();
    });

    const notification = renderer!.root.find(
      (node) =>
        typeof node.props.className === 'string' &&
        node.props.className.includes('notification success')
    );
    await act(async () => {
      notification.props.onMouseEnter();
      notification.props.onMouseLeave();
    });
    expect(mocks.pauseNotificationDismiss).toHaveBeenCalledWith('notification-1');
    expect(mocks.resumeNotificationDismiss).toHaveBeenCalledWith('notification-1');
  });

  it('exposes a copy action to the left of close', async () => {
    mocks.notifications = [
      {
        id: 'notification-1',
        message: 'copy me',
        type: 'error',
      },
    ];
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(globalThis.navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });

    await act(async () => {
      renderer = create(<NotificationContainer />);
      await Promise.resolve();
    });

    const actions = renderer!.root.find(
      (node) =>
        typeof node.props.className === 'string' &&
        node.props.className.includes('notification-actions')
    );
    const buttons = actions.findAllByType('button');
    expect(buttons).toHaveLength(2);
    expect(buttons[0].props['aria-label']).toBe('common.copy');
    expect(buttons[1].props.className).toContain('close-btn');

    await act(async () => {
      await buttons[0].props.onClick();
    });
    expect(writeText).toHaveBeenCalledWith('copy me');
  });
});
