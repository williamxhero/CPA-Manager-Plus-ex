/**
 * OAuth 与设备码登录相关 API
 */

import { apiClient, createScopedApiRequestConfig, type ApiClientRequestScope } from './client';
import { isPlanCredentialPlugin } from '@/features/plugins/planPlugins';
import { planCredentialsApi } from './planCredentials';

export type BuiltInOAuthProvider =
  | 'codex'
  | 'anthropic'
  | 'antigravity'
  | 'kimi'
  | 'xai'
  | 'devin'
  | 'meta';
export type OAuthProvider = BuiltInOAuthProvider | (string & {});

export interface OAuthStartResponse {
  url: string;
  state?: string;
  user_code?: string;
  flow?: string;
  expires_in?: number;
  metadata?: unknown;
}

// Only plugin-owned management paths may receive credentials and management authorization.
export const getPluginCredentialSubmitPath = (pluginId: string, value: unknown): string | null => {
  if (typeof value !== 'string') return null;
  const prefix = `/v0/management/plugins/${encodeURIComponent(pluginId)}/`;
  if (!value.startsWith(prefix) || /[?#\\\s]/.test(value)) return null;
  const url = new URL(value, 'https://management.invalid');
  if (url.pathname !== value) return null;
  return value.slice('/v0/management'.length);
};

export interface OAuthCallbackResponse {
  status: 'ok';
}

export interface OAuthCancelResponse {
  status: 'ok';
  cancelled: boolean;
}

/**
 * Polled completion state for a Web-driven OAuth session. Core reports the
 * targeted credential it saved and whether it was adopted as the credential
 * that started the session, so the panel never has to infer adoption from the
 * mere presence of a fresh OAuth result.
 */
export interface OAuthAuthStatusResponse {
  status: 'ok' | 'wait' | 'error';
  error?: string;
  auth_id?: string;
  saved?: boolean;
  adopted?: boolean;
  adoption_error?: string;
}

const WEBUI_SUPPORTED: string[] = ['codex', 'anthropic', 'antigravity', 'xai', 'devin'];

type CodexReauthSelector = { authIndex: string } | { name: string };

export const oauthApi = {
  startAuth: (provider: OAuthProvider, requestScope?: ApiClientRequestScope) => {
    const params: Record<string, string | boolean> = {};
    if (WEBUI_SUPPORTED.includes(provider)) {
      params.is_webui = true;
    }
    return apiClient.get<OAuthStartResponse>(`/${provider}-auth-url`, {
      ...(requestScope ? createScopedApiRequestConfig(requestScope) : {}),
      params: Object.keys(params).length ? params : undefined,
    });
  },

  /**
   * Start a targeted Codex reauthentication on the selected credential.
   *
   * This intentionally has no add-login fallback: when Core cannot target the
   * selected credential (stale, missing or unconfirmed) it must fail instead of
   * publishing a second credential through `/codex-auth-url`.
   */
  startCodexReauth: async (
    selector: CodexReauthSelector,
    requestScope?: ApiClientRequestScope
  ): Promise<OAuthStartResponse & { target_auth_id: string }> => {
    const params = new URLSearchParams({ provider: 'codex', is_webui: 'true' });
    const usesAuthIndex = 'authIndex' in selector;
    const value = (usesAuthIndex ? selector.authIndex : selector.name).trim();
    if (!value) throw new Error('Missing target credential');
    params.set(usesAuthIndex ? 'auth_index' : 'name', value);
    const response = await apiClient.getV8<OAuthStartResponse & { target_auth_id?: string }>(
      `/oauth/auth-url?${params.toString()}`,
      requestScope
    );
    const targetAuthId =
      typeof response?.target_auth_id === 'string' ? response.target_auth_id.trim() : '';
    if (!targetAuthId) throw new Error('Core did not confirm the target credential');
    return { ...response, target_auth_id: targetAuthId };
  },

  submitPluginCredential: async (
    pluginId: string,
    submitPath: string,
    fields: Record<string, string>,
    requestScope: ApiClientRequestScope
  ) => {
    const path = getPluginCredentialSubmitPath(pluginId, submitPath);
    if (!path) throw new Error('Invalid plugin credential submit path');
    if (isPlanCredentialPlugin(pluginId)) {
      return planCredentialsApi.create(pluginId, fields, requestScope);
    }
    const response = await apiClient.post<{
      ok?: boolean;
      id?: string;
      label?: string;
      status?: string;
      error?: string;
      success?: boolean;
    }>(path, fields, createScopedApiRequestConfig(requestScope));
    if (
      response?.error ||
      response?.status === 'error' ||
      response?.success === false ||
      response?.ok === false
    ) {
      throw new Error(response.error || '添加凭证失败');
    }
    return response;
  },

  getAuthStatus: (state: string, requestScope?: ApiClientRequestScope) =>
    apiClient.get<OAuthAuthStatusResponse>(`/get-auth-status`, {
      ...(requestScope ? createScopedApiRequestConfig(requestScope) : {}),
      params: { state },
    }),

  submitCallback: (
    provider: OAuthProvider,
    redirectUrl: string,
    requestScope?: ApiClientRequestScope
  ) => {
    return apiClient.post<OAuthCallbackResponse>(
      '/oauth-callback',
      {
        provider,
        redirect_url: redirectUrl,
      },
      requestScope ? createScopedApiRequestConfig(requestScope) : undefined
    );
  },

  cancelSession: (state: string, requestScope?: ApiClientRequestScope) =>
    apiClient.delete<OAuthCancelResponse>('/oauth-session', {
      ...(requestScope ? createScopedApiRequestConfig(requestScope) : {}),
      params: { state },
    }),
};
