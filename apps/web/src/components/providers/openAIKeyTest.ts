import { apiCallApi, getApiCallErrorDetails } from '@/services/api';
import { buildHeaderObject, hasHeader, type HeaderEntry } from '@/utils/headers';
import { buildOpenAIChatCompletionsEndpoint, buildOpenAIModelsEndpoint } from './utils';

export const OPENAI_KEY_TEST_TIMEOUT_MS = 30_000;

export type OpenAIKeyTestMode =
  | { kind: 'models'; auth: 'legacy' | 'keyed' | 'keyless' }
  | { kind: 'chat'; model: string; auth: 'keyed' | 'keyless' };

export interface OpenAIKeyTestInput {
  baseUrl: string;
  keyEntry: {
    apiKey: string;
    proxyUrl?: string;
    headers?: Record<string, string | undefined | null>;
  };
  headers: HeaderEntry[];
  authIndex?: string;
  mode?: OpenAIKeyTestMode;
}

/** Verify OpenAI-compatible connectivity; the default models probe is quota-free. */
export async function testOpenAIKey(input: OpenAIKeyTestInput): Promise<void> {
  const mode = input.mode ?? { kind: 'models', auth: 'legacy' as const };
  const endpoint =
    mode.kind === 'chat'
      ? buildOpenAIChatCompletionsEndpoint(input.baseUrl)
      : buildOpenAIModelsEndpoint(input.baseUrl);
  if (!endpoint) {
    throw new Error('OpenAI-compatible base URL is required');
  }
  if (mode.kind === 'chat' && !mode.model.trim()) {
    throw new Error('A model is required for chat testing');
  }
  if (mode.auth === 'keyed' && !input.keyEntry.apiKey.trim()) {
    throw new Error('An API key is required for keyed testing');
  }

  const headerObject = {
    ...buildHeaderObject(input.keyEntry.headers),
    ...buildHeaderObject(input.headers),
  };
  const headers: Record<string, string> = {
    ...(mode.kind === 'chat' ? { 'Content-Type': 'application/json' } : {}),
    ...headerObject,
  };
  if (!hasHeader(headers, 'authorization')) {
    if (mode.auth === 'legacy' && (input.authIndex || input.keyEntry.apiKey.trim())) {
      headers.Authorization = input.authIndex
        ? 'Bearer $TOKEN$'
        : `Bearer ${input.keyEntry.apiKey.trim()}`;
    } else if (mode.auth === 'keyed') {
      headers.Authorization = input.authIndex
        ? 'Bearer $TOKEN$'
        : `Bearer ${input.keyEntry.apiKey.trim()}`;
    }
  }

  const request = {
    authIndex: input.authIndex,
    proxyUrl: input.keyEntry.proxyUrl,
    method: mode.kind === 'chat' ? 'POST' : 'GET',
    url: endpoint,
    header: Object.keys(headers).length ? headers : undefined,
    ...(mode.kind === 'chat'
      ? {
          data: JSON.stringify({
            model: mode.model.trim(),
            messages: [{ role: 'user', content: 'Hi' }],
            stream: false,
            max_tokens: 5,
          }),
        }
      : {}),
  };

  const result = await apiCallApi.request(request, { timeout: OPENAI_KEY_TEST_TIMEOUT_MS });
  if (result.statusCode < 200 || result.statusCode >= 300) {
    throw new Error(getApiCallErrorDetails(result));
  }
}
