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

const WEBUI_SUPPORTED: string[] = ['codex', 'anthropic', 'antigravity', 'xai', 'devin'];

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
    apiClient.get<{ status: 'ok' | 'wait' | 'error'; error?: string }>(`/get-auth-status`, {
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
