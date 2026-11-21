import { useState } from 'react';
import type { AccountsView } from '@/features/accounts/model/accountsPagePresentation';
import {
  readAccountsWorkspaceUrlState,
  writeAccountsWorkspaceUrlSearch,
} from '@/features/accounts/model/accountsWorkspaceUrlState';
import { DEFAULT_ACCOUNTS_WORKSPACE_UI_STATE } from '@/features/accounts/model/accountsWorkspaceUiState';

export const accessNavigation = {
  id: 'accounts-tab',
  accounts: '/accounts',
  credentials: '/plan-credentials',
  login: '/oauth',
} as const;

export type AccessTab = AccountsView | 'credentials' | 'login';

export function getAccessTab(pathname: string, search: string): AccessTab | undefined {
  switch (pathname.toLowerCase().replace(/\/+$/, '')) {
    case accessNavigation.accounts:
      return readAccountsWorkspaceUrlState(search, DEFAULT_ACCOUNTS_WORKSPACE_UI_STATE).view;
    case accessNavigation.credentials:
      return 'credentials';
    case accessNavigation.login:
      return 'login';
    default:
      return undefined;
  }
}

export function accountsTabTarget(view: AccountsView, search: string) {
  const defaults = DEFAULT_ACCOUNTS_WORKSPACE_UI_STATE;
  const previous = readAccountsWorkspaceUrlState(search, defaults);
  const next = writeAccountsWorkspaceUrlSearch(search, { ...previous, view }, defaults);
  return accessNavigation.accounts + next;
}

export function useAccessAccountSearch(pathname: string, search: string, scope: string) {
  const isAccounts = pathname.toLowerCase().replace(/\/+$/, '') === accessNavigation.accounts;
  const currentSearch = isAccounts ? search : undefined;
  const [last, setLast] = useState(() => ({
    scope,
    currentSearch,
    remembered: currentSearch ?? '',
  }));

  // Only a new accounts address can seed the new connection's memory.
  if (last.scope !== scope || last.currentSearch !== currentSearch) {
    const newAddress = last.currentSearch !== currentSearch;
    setLast({
      scope,
      currentSearch,
      remembered:
        newAddress && currentSearch !== undefined
          ? currentSearch
          : last.scope === scope
            ? last.remembered
            : '',
    });
  }

  return currentSearch ?? (last.scope === scope ? last.remembered : '');
}
