import { apiClient, createScopedApiRequestConfig, type ApiClientRequestScope } from './client';
import {
  authFilesApi,
  applyAuthFileFieldsPatchToRecord,
  type AuthFileFieldsPatch,
  type AuthFileStatusTarget,
} from './authFiles';
import { sha256RawTextHex } from '@/utils/apiKeyHash';
import {
  readAuthFileStatusAuthIndex,
  readAuthFileStatusProvider,
  readAuthFileStatusRuntimeId,
} from '@/utils/authFileStatusMutation';

export const PLAN_CREDENTIAL_DEFAULT_URLS: Readonly<Record<string, string>> = {
  qwen: 'https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1',
  'opencode-go': 'https://opencode.ai/zen/go/v1',
};
const pluginProviders: Readonly<Record<string, string>> = {
  'qwen-cliproxyapi': 'qwen',
  'opencode-go-cliproxyapi': 'opencode-go',
};
type PlanFields = AuthFileFieldsPatch & { api_key?: string; label?: string; disabled?: boolean };
const normalizeURL = (value: string) => value.trim().replace(/\/+$/, '');
const requestConfig = (
  scope: ApiClientRequestScope | undefined,
  headers: Record<string, string> = {}
) => {
  const config = scope ? createScopedApiRequestConfig(scope) : {};
  return { ...config, headers: { ...config.headers, ...headers } };
};
const parseRecord = (text: string): Record<string, unknown> => {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    // JSON parser diagnostics can contain raw credential source text.
    throw new Error('Credential source must contain one JSON object');
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Credential source must contain one JSON object');
  }
  return value as Record<string, unknown>;
};
const requireOK = (value: unknown) => {
  if (!value || typeof value !== 'object') throw new Error('Credential write was not acknowledged');
  const response = value as Record<string, unknown>;
  if (
    !(response.status === 'ok' || response.ok === true || response.success === true) ||
    response.error ||
    response.ok === false ||
    response.success === false ||
    ['error', 'failed', 'partial'].includes(String(response.status)) ||
    response.uploaded === 0
  ) {
    // Do not relay host diagnostics: they can contain submitted credentials.
    throw new Error('Credential write failed');
  }
};
const findRuntime = async (name: string, scope?: ApiClientRequestScope) => {
  const files = await authFilesApi.lookup({ name }, scope);
  if (files.length !== 1) throw new Error('Credential runtime entry is missing or ambiguous');
  return files[0];
};
const verifyRuntime = async (
  name: string,
  provider: string,
  disabled: boolean,
  scope?: ApiClientRequestScope
) => {
  const file = await findRuntime(name, scope);
  if (
    readAuthFileStatusProvider(file) !== provider ||
    !readAuthFileStatusAuthIndex(file) ||
    file.disabled !== disabled ||
    (!disabled && (file.unavailable === true || !['active', 'ok'].includes(String(file.status))))
  ) {
    throw new Error('Credential runtime provider or enabled state did not match the saved file');
  }
  // Model registration is a supported, non-inference check. Never enable a disabled credential to probe it.
  if (!disabled && (await authFilesApi.getModelsForAuthFile(file.id || name, scope)).length === 0) {
    throw new Error('Credential runtime has no registered models');
  }
};
const canonicalJSON = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonicalJSON);
  if (value && typeof value === 'object')
    return Object.fromEntries(
      Object.entries(value)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, entry]) => [key, canonicalJSON(entry)])
    );
  return value;
};
const untouchedFieldsMatch = (
  before: Record<string, unknown>,
  current: Record<string, unknown>,
  fields: string[]
) => {
  const touched = new Set(fields.map((field) => field.split('.')[0]));
  return [...new Set([...Object.keys(before), ...Object.keys(current)])].every(
    (field) =>
      touched.has(field) ||
      JSON.stringify(canonicalJSON(before[field])) === JSON.stringify(canonicalJSON(current[field]))
  );
};
const touchedFieldsMatch = (
  before: Record<string, unknown>,
  current: Record<string, unknown>,
  patch: PlanFields
) => {
  const expected = applyAuthFileFieldsPatchToRecord(before, patch);
  return Object.keys(patch).every((field) => {
    const value =
      field === 'excluded_models' && Array.isArray(patch.excluded_models)
        ? patch.excluded_models
        : expected[field];
    const actual = current[field];
    if (value === undefined)
      return (
        actual == null || actual === '' || (patch[field as keyof PlanFields] === 0 && actual === 0)
      );
    return JSON.stringify(canonicalJSON(value)) === JSON.stringify(canonicalJSON(actual));
  });
};
const sameIdentity = (
  record: Record<string, unknown>,
  provider: string,
  key: string,
  baseURL: string
) =>
  record.type === provider &&
  record.api_key === key &&
  normalizeURL(String(record.base_url || PLAN_CREDENTIAL_DEFAULT_URLS[provider])) === baseURL;

export const planCredentialsApi = {
  update: async (
    target: AuthFileStatusTarget,
    fields: PlanFields,
    scope?: ApiClientRequestScope
  ) => {
    const oldText = await authFilesApi.downloadText(target.name, scope);
    const before = parseRecord(oldText);
    const provider = String(before.type);
    if (
      !PLAN_CREDENTIAL_DEFAULT_URLS[provider] ||
      (target.provider && target.provider !== provider) ||
      typeof before.api_key !== 'string' ||
      !before.api_key.trim()
    ) {
      throw new Error('Credential source identity changed; reload before saving');
    }
    // Configuration edits never override an operator's disabled state.
    const disabled = before.disabled === true;
    const patch = { ...fields, disabled };
    // Core retains generic PATCH nulls. Keep the legacy exclusion alias equivalent
    // rather than letting its presence shadow the canonical list at runtime.
    if (patch.excluded_models === null && patch['excluded-models'] !== undefined) {
      patch.excluded_models = patch['excluded-models'] ?? [];
    }
    if (patch.api_key !== undefined) {
      patch.api_key = patch.api_key.trim();
      if (!patch.api_key) delete patch.api_key;
    }
    if (patch.label !== undefined) patch.label = patch.label.trim();
    if (patch.base_url !== undefined)
      patch.base_url = normalizeURL(
        patch.base_url.trim() || PLAN_CREDENTIAL_DEFAULT_URLS[provider]
      );
    const expectedKey = patch.api_key ?? before.api_key;
    const expectedURL = normalizeURL(
      String(patch.base_url || before.base_url || PLAN_CREDENTIAL_DEFAULT_URLS[provider])
    );
    if (/[\r\n\0]/.test(expectedKey)) throw new Error('Invalid API Key');
    const parsedURL = new URL(expectedURL);
    if (
      !['http:', 'https:'].includes(parsedURL.protocol) ||
      parsedURL.username ||
      parsedURL.password ||
      parsedURL.search ||
      parsedURL.hash ||
      expectedURL.includes(expectedKey)
    )
      throw new Error('Invalid credential Base URL');
    const config = requestConfig(scope, {
      'X-CPAMP-Auth-File-Mutation-Identity': encodeURIComponent(JSON.stringify([target])),
      'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(oldText),
    });
    // A rejected PATCH is not a success; a transport error can still mean it persisted.
    let savedText: string | undefined;
    try {
      requireOK(
        await apiClient.patch(
          '/auth-files/fields',
          { name: target.runtimeId || target.name, ...patch },
          config
        )
      );
      savedText = await authFilesApi.downloadText(target.name, scope);
      const saved = parseRecord(savedText);
      if (
        !sameIdentity(saved, provider, expectedKey, expectedURL) ||
        !touchedFieldsMatch(before, saved, patch) ||
        !untouchedFieldsMatch(before, saved, Object.keys(patch))
      ) {
        throw new Error('Saved credential fields did not match');
      }
      await verifyRuntime(target.name, provider, disabled, scope);
    } catch {
      try {
        const currentText = await authFilesApi.downloadText(target.name, scope);
        if (currentText !== oldText) {
          const current = parseRecord(currentText);
          if (
            !sameIdentity(current, provider, expectedKey, expectedURL) ||
            current.disabled !== disabled ||
            (patch.label !== undefined && current.label !== patch.label) ||
            !untouchedFieldsMatch(before, current, Object.keys(patch)) ||
            !touchedFieldsMatch(before, current, patch) ||
            (savedText !== undefined && currentText !== savedText)
          )
            throw new Error('Credential changed concurrently');
          // Restore the complete snapshot even when failed activation has removed the runtime row.
          requireOK(
            await apiClient.post(
              `/auth-files?name=${encodeURIComponent(target.name)}`,
              oldText,
              requestConfig(scope, {
                'Content-Type': 'application/json',
                'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(currentText),
              })
            )
          );
          const restoreFields = Object.fromEntries(
            ['api_key', 'base_url', 'label', 'disabled']
              .filter((field) => Object.prototype.hasOwnProperty.call(before, field))
              .map((field) => [field, before[field]])
          );
          const restoreText = await authFilesApi.downloadText(target.name, scope);
          if (!untouchedFieldsMatch(before, parseRecord(restoreText), []))
            throw new Error('Credential changed during rollback');
          requireOK(
            await apiClient.patch(
              '/auth-files/fields',
              { name: target.name, ...restoreFields },
              requestConfig(scope, {
                'X-CPAMP-Auth-File-Mutation-Identity': encodeURIComponent(
                  JSON.stringify([{ name: target.name, provider }])
                ),
                'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(restoreText),
              })
            )
          );
          const restored = parseRecord(await authFilesApi.downloadText(target.name, scope));
          if (!untouchedFieldsMatch(before, restored, []))
            throw new Error('Rollback fields did not match');
          await verifyRuntime(target.name, provider, disabled, scope);
        }
      } catch {
        throw new Error(
          'Credential save/runtime validation failed; rollback could not be confirmed. Reload before retrying.'
        );
      }
      throw new Error(
        'Credential save/runtime validation failed; previous credential was rolled back'
      );
    }
  },
  create: async (
    pluginId: string,
    fields: Record<string, string>,
    scope: ApiClientRequestScope
  ) => {
    const provider = pluginProviders[pluginId];
    if (!provider) throw new Error('Unsupported plan credential provider');
    const key = fields.api_key?.trim() || '';
    const baseURL = normalizeURL(fields.base_url?.trim() || PLAN_CREDENTIAL_DEFAULT_URLS[provider]);
    if (!key || /[\r\n\0]/.test(key))
      throw new Error('API Key is required and must not contain control characters');
    const url = new URL(baseURL);
    if (
      !['https:', 'http:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      baseURL.includes(key)
    ) {
      throw new Error('Invalid credential Base URL');
    }
    const id = `${provider}-key-${sha256RawTextHex(`${key}\0${baseURL}`)}`;
    const name = `${id}.json`;
    const label = (
      fields.name?.trim() ||
      (key.length > 8 ? `${key.slice(0, 4)}...${key.slice(-4)}` : '[redacted]')
    )
      .split(key)
      .join('[redacted]');
    const record = { type: provider, id, api_key: key, base_url: baseURL, label, disabled: false };
    const inventory = await authFilesApi.list(scope);
    for (const file of inventory.files) {
      if (file.name === name)
        throw new Error(
          'Credential file name is already occupied; existing content was not overwritten'
        );
      if (readAuthFileStatusProvider(file) !== provider) continue;
      const existing = await authFilesApi.downloadJsonObject(file.name, scope);
      if (sameIdentity(existing, provider, key, baseURL))
        throw new Error('Credential already exists');
    }
    // Inventory can miss a file not registered in the runtime. Also check the physical target.
    let absent = false;
    try {
      await authFilesApi.downloadText(name, scope);
    } catch (error) {
      absent = Boolean(
        error && typeof error === 'object' && 'status' in error && error.status === 404
      );
      if (!absent) throw new Error('Cannot safely inspect credential file occupancy');
    }
    if (!absent)
      throw new Error(
        'Credential file name is already occupied; existing content was not overwritten'
      );
    let created = false;
    try {
      const result = await apiClient.post(
        `/auth-files?name=${encodeURIComponent(name)}`,
        record,
        requestConfig(scope, { 'X-CPAMP-Auth-File-Create-Only': 'true' })
      );
      requireOK(result);
      created = true;
      const publishedText = await authFilesApi.downloadText(name, scope);
      const published = parseRecord(publishedText);
      if (
        !sameIdentity(published, provider, key, baseURL) ||
        published.disabled !== false ||
        published.label !== label
      ) {
        throw new Error('Credential changed before runtime publication');
      }
      // A freshly created credential has no account evidence, so the Manager requires an
      // explicit credential locator in the mutation identity or it fails closed with 409.
      const registered = (await authFilesApi.list(scope)).files.find((file) => file.name === name);
      const runtimeId = registered ? readAuthFileStatusRuntimeId(registered) : '';
      const authIndex = registered ? readAuthFileStatusAuthIndex(registered) : '';
      if (!runtimeId && !authIndex) {
        throw new Error('Credential was not registered in the runtime; reload before retrying');
      }
      requireOK(
        await apiClient.patch(
          '/auth-files/fields',
          { name, api_key: key, base_url: baseURL, label, disabled: false },
          requestConfig(scope, {
            'X-CPAMP-Auth-File-Mutation-Identity': encodeURIComponent(
              JSON.stringify([
                {
                  name,
                  provider,
                  ...(runtimeId ? { runtimeId } : {}),
                  ...(authIndex ? { authIndex } : {}),
                },
              ])
            ),
            'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(publishedText),
          })
        )
      );
      const saved = parseRecord(await authFilesApi.downloadText(name, scope));
      if (!untouchedFieldsMatch(record, saved, []))
        throw new Error('Saved credential fields did not match');
      await verifyRuntime(name, provider, false, scope);
      return { ok: true, id, label };
    } catch (error) {
      const status =
        error && typeof error === 'object' && 'status' in error ? Number(error.status) : 0;
      if (!created && status >= 400 && status < 500) {
        throw Object.assign(
          new Error(
            status === 409
              ? 'Credential file changed or is occupied; no overwrite was performed'
              : 'Credential save was rejected'
          ),
          { status }
        );
      }
      // A deterministic key/URL filename cannot prove ownership after a lost POST reply.
      if (!created)
        throw new Error(
          'Credential creation was not acknowledged; rollback could not be confirmed. Reload before retrying.'
        );
      try {
        const savedText = await authFilesApi.downloadText(name, scope);
        const saved = parseRecord(savedText);
        if (
          !sameIdentity(saved, provider, key, baseURL) ||
          saved.id !== id ||
          saved.disabled !== false ||
          saved.label !== label ||
          !untouchedFieldsMatch(record, saved, [])
        ) {
          throw new Error('Credential changed during save');
        }
        // A failed registration may have no runtime locator. The exact physical file plus content digest fences deletion.
        requireOK(
          await apiClient.delete(
            `/auth-files?name=${encodeURIComponent(name)}`,
            requestConfig(scope, {
              'X-CPAMP-Auth-File-Physical-Name': name,
              'X-CPAMP-Auth-File-Expected-Content-SHA256': sha256RawTextHex(savedText),
            })
          )
        );
      } catch {
        throw new Error(
          'Credential save/runtime validation failed; rollback could not be confirmed. Reload before retrying.'
        );
      }
      throw new Error('Credential save/runtime validation failed; new credential was rolled back');
    }
  },
};
