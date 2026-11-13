/**
 * 通知状态管理
 * 替代原项目中的 showNotification 方法
 */

import { create } from 'zustand';
import type { ReactNode } from 'react';
import type { Notification, NotificationType } from '@/types';
import { generateId } from '@/utils/helpers';
import { NOTIFICATION_DURATION_MS } from '@/utils/constants';

interface ConfirmationStepOptions {
  title?: string;
  message: ReactNode;
  confirmText?: string;
  cancelText?: string;
  variant?: 'danger' | 'primary' | 'secondary';
  width?: number | string;
}

interface ConfirmationOptions extends ConfirmationStepOptions {
  onConfirm: () => void | Promise<void>;
  onCancel?: () => void;
  secondConfirmation?: ConfirmationStepOptions;
}

interface NotificationDismissState {
  remainingMs: number;
  startedAtMs: number;
}

const dismissTimers = new Map<string, ReturnType<typeof setTimeout>>();
const dismissStates = new Map<string, NotificationDismissState>();

const clearDismissTimer = (id: string) => {
  const timer = dismissTimers.get(id);
  if (timer) {
    clearTimeout(timer);
    dismissTimers.delete(id);
  }
};

interface NotificationState {
  notifications: Notification[];
  confirmation: {
    isOpen: boolean;
    isLoading: boolean;
    step: 1 | 2;
    options: ConfirmationOptions | null;
  };
  showNotification: (message: string, type?: NotificationType, duration?: number) => void;
  removeNotification: (id: string) => void;
  pauseNotificationDismiss: (id: string) => void;
  resumeNotificationDismiss: (id: string) => void;
  clearAll: () => void;
  showConfirmation: (options: ConfirmationOptions) => void;
  hideConfirmation: () => void;
  setConfirmationLoading: (loading: boolean) => void;
  advanceConfirmation: () => void;
}

export const useNotificationStore = create<NotificationState>((set, get) => {
  const scheduleDismiss = (id: string, remainingMs: number) => {
    clearDismissTimer(id);
    if (remainingMs <= 0) {
      dismissStates.delete(id);
      get().removeNotification(id);
      return;
    }
    dismissStates.set(id, { remainingMs, startedAtMs: Date.now() });
    dismissTimers.set(
      id,
      setTimeout(() => {
        dismissTimers.delete(id);
        dismissStates.delete(id);
        set((state) => ({
          notifications: state.notifications.filter((n) => n.id !== id),
        }));
      }, remainingMs)
    );
  };

  return {
    notifications: [],
    confirmation: {
      isOpen: false,
      isLoading: false,
      step: 1,
      options: null,
    },

    showNotification: (message, type = 'info', duration = NOTIFICATION_DURATION_MS) => {
      const id = generateId();
      const notification: Notification = {
        id,
        message,
        type,
        duration,
      };

      set((state) => ({
        notifications: [...state.notifications, notification],
      }));

      if (duration > 0) {
        scheduleDismiss(id, duration);
      }
    },

    removeNotification: (id) => {
      clearDismissTimer(id);
      dismissStates.delete(id);
      set((state) => ({
        notifications: state.notifications.filter((n) => n.id !== id),
      }));
    },

    pauseNotificationDismiss: (id) => {
      const timer = dismissTimers.get(id);
      const state = dismissStates.get(id);
      if (!timer || !state) return;
      clearDismissTimer(id);
      const elapsed = Date.now() - state.startedAtMs;
      dismissStates.set(id, {
        remainingMs: Math.max(0, state.remainingMs - elapsed),
        startedAtMs: 0,
      });
    },

    resumeNotificationDismiss: (id) => {
      const state = dismissStates.get(id);
      if (!state) return;
      if (dismissTimers.has(id)) return;
      scheduleDismiss(id, state.remainingMs > 0 ? state.remainingMs : 0);
    },

    clearAll: () => {
      for (const id of [...dismissTimers.keys()]) {
        clearDismissTimer(id);
      }
      dismissStates.clear();
      set({ notifications: [] });
    },

    showConfirmation: (options) => {
      const active = get().confirmation;
      if (active.isOpen && active.options) {
        if (active.isLoading) {
          options.onCancel?.();
          return;
        }
        active.options.onCancel?.();
      }
      set({
        confirmation: {
          isOpen: true,
          isLoading: false,
          step: 1,
          options,
        },
      });
    },

    hideConfirmation: () => {
      set((state) => ({
        confirmation: {
          ...state.confirmation,
          isOpen: false,
          isLoading: false,
          step: 1,
          options: null, // Cleanup
        },
      }));
    },

    setConfirmationLoading: (loading) => {
      set((state) => ({
        confirmation: {
          ...state.confirmation,
          isLoading: loading,
        },
      }));
    },

    advanceConfirmation: () => {
      set((state) => ({
        confirmation: {
          ...state.confirmation,
          isLoading: false,
          step: 2,
        },
      }));
    },
  };
});
