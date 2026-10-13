import type { PluginListEntry } from '@/types';
import { isPlanCredentialPlugin } from '@/features/plugins/planPlugins';

export interface OAuthPollingScope {
  connectionFingerprint: string | null;
  accountReauthSessionId: string | null;
  search: string;
}

export interface OAuthProviderAttempt {
  scope: OAuthPollingScope;
  version: number;
}

export const isOAuthPollingScopeCurrent = (
  started: OAuthPollingScope,
  current: OAuthPollingScope
): boolean =>
  started.connectionFingerprint === current.connectionFingerprint &&
  started.accountReauthSessionId === current.accountReauthSessionId;

export const isOAuthProviderAttemptCurrent = (
  attempt: OAuthProviderAttempt,
  currentScope: OAuthPollingScope,
  currentVersion: number | undefined
): boolean =>
  attempt.version === currentVersion && isOAuthPollingScopeCurrent(attempt.scope, currentScope);

export const resolvePluginOAuthProviderId = (
  plugin: Pick<PluginListEntry, 'id' | 'oauthProvider'>
): string => plugin.oauthProvider ?? plugin.id;

export const shouldShowPluginOAuthProvider = (
  plugin: Pick<PluginListEntry, 'id' | 'oauthProvider' | 'supportsOAuth'>,
  builtInProviderIds: ReadonlySet<string>
): boolean =>
  plugin.supportsOAuth &&
  !isPlanCredentialPlugin(plugin.id) &&
  !builtInProviderIds.has(resolvePluginOAuthProviderId(plugin));
