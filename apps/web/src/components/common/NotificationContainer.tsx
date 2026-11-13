import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { useNotificationStore } from '@/stores';
import { IconCopy, IconX } from '@/components/ui/icons';
import type { Notification } from '@/types';

interface AnimatedNotification extends Notification {
  isExiting?: boolean;
}

const ANIMATION_DURATION = 300; // ms

export function NotificationContainer() {
  const { t } = useTranslation();
  const {
    notifications,
    removeNotification,
    pauseNotificationDismiss,
    resumeNotificationDismiss,
  } = useNotificationStore();
  const [animatedNotifications, setAnimatedNotifications] = useState<AnimatedNotification[]>([]);
  const prevNotificationsRef = useRef<Notification[]>([]);
  const [copiedId, setCopiedId] = useState<string | null>(null);

  useEffect(() => {
    const prevNotifications = prevNotificationsRef.current;
    const prevIds = new Set(prevNotifications.map((n) => n.id));
    const currentIds = new Set(notifications.map((n) => n.id));

    const newNotifications = notifications.filter((n) => !prevIds.has(n.id));

    const removedIds = new Set(
      prevNotifications.filter((n) => !currentIds.has(n.id)).map((n) => n.id)
    );

    setAnimatedNotifications((prev) => {
      let updated = prev.map((n) => (removedIds.has(n.id) ? { ...n, isExiting: true } : n));

      newNotifications.forEach((n) => {
        if (!updated.find((animatedNotification) => animatedNotification.id === n.id)) {
          updated.push({ ...n, isExiting: false });
        }
      });

      updated = updated.filter((n) => currentIds.has(n.id) || n.isExiting);

      return updated;
    });

    if (removedIds.size > 0) {
      setTimeout(() => {
        setAnimatedNotifications((prev) => prev.filter((n) => !removedIds.has(n.id)));
      }, ANIMATION_DURATION);
    }

    prevNotificationsRef.current = notifications;
  }, [notifications]);

  const handleClose = (id: string) => {
    setAnimatedNotifications((prev) =>
      prev.map((n) => (n.id === id ? { ...n, isExiting: true } : n))
    );

    setTimeout(() => {
      removeNotification(id);
    }, ANIMATION_DURATION);
  };

  const handleCopy = async (notification: AnimatedNotification) => {
    try {
      await navigator.clipboard.writeText(notification.message);
      setCopiedId(notification.id);
      window.setTimeout(() => {
        setCopiedId((current) => (current === notification.id ? null : current));
      }, 1500);
    } catch {
      // Clipboard may be unavailable in restricted contexts; keep the toast itself.
    }
  };

  if (!animatedNotifications.length) return null;

  const content = (
    <div className="notification-container">
      {animatedNotifications.map((notification) => (
        <div
          key={notification.id}
          className={`notification ${notification.type} ${notification.isExiting ? 'exiting' : 'entering'}`}
          onMouseEnter={() => pauseNotificationDismiss(notification.id)}
          onMouseLeave={() => resumeNotificationDismiss(notification.id)}
        >
          <div className="message">{notification.message}</div>
          <div className="notification-actions">
            <button
              type="button"
              className="action-btn"
              onClick={() => void handleCopy(notification)}
              aria-label={t('common.copy', { defaultValue: '复制' })}
              title={
                copiedId === notification.id
                  ? t('notification.link_copied', { defaultValue: '已复制' })
                  : t('common.copy', { defaultValue: '复制' })
              }
            >
              <IconCopy size={16} />
            </button>
            <button
              type="button"
              className="close-btn action-btn"
              onClick={() => handleClose(notification.id)}
              aria-label={t('common.close')}
            >
              <IconX size={16} />
            </button>
          </div>
        </div>
      ))}
    </div>
  );

  if (typeof document === 'undefined') return content;

  return createPortal(content, document.body);
}
