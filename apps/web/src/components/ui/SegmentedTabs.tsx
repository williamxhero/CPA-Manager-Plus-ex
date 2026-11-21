import {
  useCallback,
  useMemo,
  useLayoutEffect,
  useRef,
  type ComponentType,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
} from 'react';
import styles from './SegmentedTabs.module.scss';

export type SegmentedTabItem<Id extends string = string> = {
  id: Id;
  label: ReactNode;
  disabled?: boolean;
  title?: string;
  to?: string;
};

export type SegmentedTabsLinkProps = {
  to: string;
  className: string;
  role: 'tab';
  id: string;
  'aria-selected': boolean;
  'aria-disabled'?: boolean;
  'aria-controls'?: string;
  tabIndex: number;
  title?: string;
  onClick: (event: MouseEvent<HTMLElement>) => void;
  onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
  children: ReactNode;
  ref?: (node: HTMLElement | null) => void;
};

type SegmentedTabsProps<Id extends string> = {
  items: ReadonlyArray<SegmentedTabItem<Id>>;
  activeTab: Id;
  ariaLabel: string;
  onChange?: (tab: Id) => void;
  idBase?: string;
  className?: string;
  fullWidth?: boolean;
  equalWidth?: boolean;
  responsiveFullWidth?: boolean;
  disabled?: boolean;
  panelId?: string;
  keepActiveVisible?: boolean;
  linkComponent?: ComponentType<SegmentedTabsLinkProps>;
};

export function SegmentedTabs<Id extends string>({
  items,
  activeTab,
  ariaLabel,
  onChange,
  idBase = 'segmented-tabs',
  className = '',
  fullWidth = false,
  equalWidth = false,
  responsiveFullWidth = true,
  disabled = false,
  panelId,
  keepActiveVisible = false,
  linkComponent: LinkComponent,
}: SegmentedTabsProps<Id>) {
  const railRef = useRef<HTMLDivElement>(null);
  const previousActiveTab = useRef(activeTab);
  const itemRefs = useRef<Map<Id, HTMLElement | null>>(new Map());
  const enabledItems = useMemo(
    () => items.filter((item) => !disabled && !item.disabled),
    [disabled, items]
  );

  const revealItem = useCallback((item: HTMLElement | null | undefined) => {
    const rail = railRef.current;
    if (!rail || !item) return;
    const { left, right } = rail.getBoundingClientRect();
    const tab = item.getBoundingClientRect();
    if (tab.left < left) rail.scrollLeft += tab.left - left;
    else if (tab.right > right) rail.scrollLeft += tab.right - right;
  }, []);

  useLayoutEffect(() => {
    const activeChanged = previousActiveTab.current !== activeTab;
    previousActiveTab.current = activeTab;
    const rail = railRef.current;
    const item = itemRefs.current.get(activeTab);
    if (!keepActiveVisible || !rail || !item) return;
    if (activeChanged && rail.contains(rail.ownerDocument.activeElement)) {
      item.focus({ preventScroll: true });
    }
    revealItem(item);
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => revealItem(item));
    observer.observe(rail);
    return () => observer.disconnect();
  }, [activeTab, items, keepActiveVisible, revealItem]);

  const focusItem = useCallback((itemId: Id) => {
    const item = itemRefs.current.get(itemId);
    if (keepActiveVisible) {
      item?.focus({ preventScroll: true });
      revealItem(item);
    } else {
      item?.focus();
    }
  }, [keepActiveVisible, revealItem]);

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLElement>, currentId: Id) => {
      if (enabledItems.length === 0) return;

      const currentIndex = enabledItems.findIndex((item) => item.id === currentId);
      if (currentIndex === -1) return;

      if (event.key === " " && LinkComponent && enabledItems[currentIndex].to) {
        event.preventDefault();
        event.currentTarget.click();
        return;
      }

      let nextIndex = currentIndex;
      switch (event.key) {
        case 'ArrowRight':
        case 'ArrowDown':
          nextIndex = (currentIndex + 1) % enabledItems.length;
          break;
        case 'ArrowLeft':
        case 'ArrowUp':
          nextIndex = (currentIndex - 1 + enabledItems.length) % enabledItems.length;
          break;
        case 'Home':
          nextIndex = 0;
          break;
        case 'End':
          nextIndex = enabledItems.length - 1;
          break;
        default:
          return;
      }

      event.preventDefault();
      const nextItem = enabledItems[nextIndex];
      focusItem(nextItem.id);
      if (!nextItem.to) {
        onChange?.(nextItem.id);
      }
    },
    [LinkComponent, enabledItems, focusItem, onChange]
  );

  const rootClassName = [
    styles.root,
    fullWidth ? styles.fullWidth : '',
    equalWidth ? styles.equalWidth : '',
    responsiveFullWidth ? styles.responsiveFullWidth : '',
    className,
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className={rootClassName} role="tablist" aria-label={ariaLabel} ref={railRef}>
      {items.map((item) => {
        const isActive = item.id === activeTab;
        const isDisabled = disabled || Boolean(item.disabled);
        const itemClassName = [
          styles.item,
          isActive ? styles.active : '',
          isDisabled ? styles.disabled : '',
        ]
          .filter(Boolean)
          .join(' ');
        const itemId = `${idBase}-${item.id}`;
        const setItemRef = (node: HTMLElement | null) => {
          itemRefs.current.set(item.id, node);
        };
        const handleClick = (event: MouseEvent<HTMLElement>) => {
          if (isDisabled) {
            event.preventDefault();
            return;
          }
          if (!isActive) {
            onChange?.(item.id);
          }
        };
        const commonProps = {
          className: itemClassName,
          role: 'tab' as const,
          id: itemId,
          'aria-selected': isActive,
          'aria-controls': panelId,
          tabIndex: isActive && !isDisabled ? 0 : -1,
          title: item.title,
          onClick: handleClick,
          onKeyDown: (event: KeyboardEvent<HTMLElement>) => handleKeyDown(event, item.id),
          children: item.label,
        };

        if (item.to && !isDisabled && LinkComponent) {
          return (
            <LinkComponent
              key={item.id}
              {...commonProps}
              ref={setItemRef}
              to={item.to}
            />
          );
        }

        return (
          <button
            key={item.id}
            {...commonProps}
            ref={setItemRef}
            type="button"
            disabled={isDisabled}
            aria-disabled={isDisabled || undefined}
          />
        );
      })}
    </div>
  );
}
