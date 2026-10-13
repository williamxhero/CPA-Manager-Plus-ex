import { useEffect, useRef, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { oauthApi } from '@/services/api/oauth';
import type { ApiClientRequestScope } from '@/services/api/client';
import { useAuthStore, useNotificationStore } from '@/stores';
import type { PluginCredentialFormDefinition } from './pluginCredentialMetadata';
import { isPlanCredentialPlugin } from '@/features/plugins/planPlugins';
import styles from './OAuthPage.module.scss';

interface PluginCredentialFormProps {
  pluginId: string;
  definition: PluginCredentialFormDefinition;
  requestScope: ApiClientRequestScope;
  onCreated: () => void;
  loadingMetadata?: boolean;
}

export function PluginCredentialForm({
  pluginId,
  definition,
  requestScope,
  onCreated,
  loadingMetadata = false,
}: PluginCredentialFormProps) {
  const { t } = useTranslation();
  const { showNotification } = useNotificationStore();
  const [values, setValues] = useState<Record<string, string>>({});
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [createdLabel, setCreatedLabel] = useState('');
  const mounted = useRef(false);
  const inFlight = useRef(false);
  const isPlan = isPlanCredentialPlugin(pluginId);
  const formFields = isPlan
    ? [...definition.fields].sort(
        (left, right) =>
          ['name', 'base_url', 'api_key'].indexOf(left.name) -
          ['name', 'base_url', 'api_key'].indexOf(right.name)
      )
    : definition.fields;

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (inFlight.current || loadingMetadata) return;
    const payload = Object.fromEntries(
      definition.fields.map((field) => [field.name, values[field.name] || ''])
    );
    setError('');
    setCreatedLabel('');
    const missing = definition.fields.find(
      (field) =>
        !(isPlan && field.name === 'base_url') &&
        (field.required || (isPlan && field.name === 'api_key')) &&
        !payload[field.name].trim()
    );
    if (missing) {
      setError(t('plan_credentials.required_field', { field: missing.label }));
      return;
    }
    if (isPlan && payload.base_url?.trim()) {
      try {
        const url = new URL(payload.base_url);
        if (!['http:', 'https:'].includes(url.protocol) || !url.hostname) throw new Error();
      } catch {
        setError(t('plan_credentials.invalid_base_url'));
        return;
      }
    }
    const isConnectionCurrent = () => {
      const auth = useAuthStore.getState();
      return (
        auth.apiBase === requestScope.apiBase && auth.managementKey === requestScope.managementKey
      );
    };
    const isCurrent = () => mounted.current && isConnectionCurrent();
    if (!isCurrent()) return;
    inFlight.current = true;
    setSubmitting(true);
    setError('');
    try {
      const response = await oauthApi.submitPluginCredential(
        pluginId,
        definition.submitPath,
        payload,
        requestScope
      );
      if (!isConnectionCurrent()) return;
      // Credential creation outlives this page; synchronize an already-open accounts list too.
      if (isPlan) onCreated();
      if (!mounted.current) return;
      setValues((previous) =>
        isPlan
          ? {}
          : Object.fromEntries(
              definition.fields.map((field) => [
                field.name,
                field.type === 'password' ? '' : previous[field.name] || '',
              ])
            )
      );
      setCreatedLabel(response?.label || '');
      if (!isPlan) onCreated();
      showNotification(t('plan_credentials.success', { defaultValue: '凭证添加成功' }), 'success');
    } catch (err: unknown) {
      if (!isCurrent()) return;
      let message = err instanceof Error ? err.message : t('plan_credentials.failed');
      if (err && typeof err === 'object' && 'status' in err && err.status === 409) {
        const duplicate = t('plan_credentials.duplicate');
        message = message === duplicate ? duplicate : `${duplicate}: ${message}`;
      }
      // A plugin error must not accidentally echo submitted secrets back into the panel/toast.
      const secrets = definition.fields
        .filter((field) => field.type === 'password')
        .map((field) => payload[field.name])
        .filter(Boolean)
        .sort((left, right) => right.length - left.length);
      for (const secret of secrets) {
        message = message.split(secret).join('[redacted]');
      }
      setError(message);
      showNotification(message, 'error');
    } finally {
      inFlight.current = false;
      if (isCurrent()) setSubmitting(false);
    }
  };

  return (
    <form
      className={`${styles.cardContent} ${isPlan ? styles.planCredentialRow : ''}`}
      onSubmit={submit}
      autoComplete="off"
      noValidate
    >
      {formFields.map((field) => (
        <Input
          key={field.name}
          name={field.name}
          label={field.label}
          placeholder={field.placeholder}
          type={field.type}
          required={isPlan && field.name === 'base_url' ? false : field.required}
          autoComplete={field.type === 'password' ? 'new-password' : 'off'}
          value={values[field.name] || ''}
          disabled={submitting || loadingMetadata}
          onChange={(event) =>
            setValues((previous) => ({ ...previous, [field.name]: event.target.value }))
          }
        />
      ))}
      <div className={styles.callbackActions}>
        <Button type="submit" loading={submitting || loadingMetadata}>
          {definition.submitLabel}
        </Button>
      </div>
      {createdLabel && (
        <div className="status-badge success" role="status">
          {t('plan_credentials.success')}: {createdLabel}
        </div>
      )}
      {error && (
        <div className="status-badge error" role="alert">
          {error}
        </div>
      )}
    </form>
  );
}
