import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { PluginCredentialForm } from '@/features/oauth/PluginCredentialForm';
import {
  getPluginCredentialFormFallback,
  readPluginCredentialForm,
  type PluginCredentialFormDefinition,
} from '@/features/oauth/pluginCredentialMetadata';
import { resolvePluginOAuthProviderId } from '@/features/oauth/oauthProviderHelpers';
import { getAuthFileIcon } from '@/features/authFiles/constants';
import { isPlanCredentialPlugin, PLAN_PLUGIN_TITLES } from '@/features/plugins/planPlugins';
import { PLUGIN_RESOURCES_REFRESH_EVENT } from '@/features/plugins/pluginResources';
import { createCodexInspectionConnectionFingerprint } from '@/features/monitoring/codexInspection';
import { recordAccountCredentialMutationMarker } from '@/features/accounts/model/accountCredentialMutationMarker';
import { oauthApi, pluginsApi } from '@/services/api';
import { publishAccountCredentialMutationRevision, useAuthStore, useThemeStore } from '@/stores';
import type { PluginListEntry } from '@/types';
import styles from '@/features/oauth/OAuthPage.module.scss';

interface PlanModule {
  plugin: PluginListEntry;
  definition: PluginCredentialFormDefinition;
  loadingMetadata: boolean;
}

export function PlanCredentialsPage() {
  const { t } = useTranslation();
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const supportsPlugin = useAuthStore((state) => state.supportsPlugin);
  const requestScope = useMemo(() => ({ apiBase, managementKey }), [apiBase, managementKey]);
  const connectionFingerprint = useMemo(
    () => createCodexInspectionConnectionFingerprint(apiBase, managementKey),
    [apiBase, managementKey]
  );
  const [modules, setModules] = useState<PlanModule[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [revision, setRevision] = useState(0);
  const available = connectionStatus === 'connected' && supportsPlugin;

  useEffect(() => {
    const refresh = () => setRevision((previous) => previous + 1);
    window.addEventListener(PLUGIN_RESOURCES_REFRESH_EVENT, refresh);
    return () => window.removeEventListener(PLUGIN_RESOURCES_REFRESH_EVENT, refresh);
  }, []);

  useEffect(() => {
    let cancelled = false;
    const isCurrent = () => {
      const auth = useAuthStore.getState();
      return !cancelled && auth.apiBase === apiBase && auth.managementKey === managementKey;
    };
    setModules([]);
    setError('');
    setLoading(available);
    if (!available) return;

    pluginsApi
      .list(requestScope)
      .then(async (response) => {
        if (!isCurrent()) return;
        const next = response.plugins
          .filter((plugin) => isPlanCredentialPlugin(plugin.id))
          .map(
            (plugin): PlanModule => ({
              plugin,
              definition: getPluginCredentialFormFallback(plugin.id)!,
              loadingMetadata: true,
            })
          );
        setModules(next);
        setLoading(false);
        await Promise.all(
          next.map(async (module) => {
            let definition = module.definition;
            // These allowlisted plugins are manual forms, not browser OAuth flows.
            try {
              const start = await oauthApi.startAuth(
                resolvePluginOAuthProviderId(module.plugin),
                requestScope
              );
              const metadata = readPluginCredentialForm(module.plugin.id, start.metadata);
              if (metadata) {
                const names = metadata.fields.map((field) => field.name);
                if (
                  names.length !== 3 ||
                  !['base_url', 'api_key', 'name'].every((name) => names.includes(name))
                ) {
                  throw new Error('Invalid plan credential form fields');
                }
                definition = metadata;
              }
            } catch {
              // Older hosts strip metadata or reject the probe; POST still reports real errors.
            }
            if (!isCurrent()) return;
            setModules((previous) =>
              previous.map((item) =>
                item.plugin.id === module.plugin.id
                  ? { ...item, definition, loadingMetadata: false }
                  : item
              )
            );
          })
        );
      })
      .catch((err: unknown) => {
        if (!isCurrent()) return;
        setError(err instanceof Error ? err.message : t('plan_credentials.failed'));
        setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [apiBase, available, managementKey, requestScope, revision, t]);

  const renderModuleTitle = (plugin: PluginListEntry) => {
    const title = PLAN_PLUGIN_TITLES[plugin.id];
    const icon = getAuthFileIcon(resolvePluginOAuthProviderId(plugin), resolvedTheme);
    if (!icon) return title;
    return (
      <span className={styles.cardTitle}>
        <img src={icon} alt="" aria-hidden="true" className={styles.cardTitleIcon} />
        {title}
      </span>
    );
  };

  return (
    <div className={styles.container}>
      <div className={styles.content}>
        <h1>{t('plan_credentials.title')}</h1>
        {loading && <div role="status">{t('plan_credentials.loading')}</div>}
        {error && (
          <div className="status-badge error" role="alert">
            {error}
          </div>
        )}
        {!loading && !error && !modules.length && <div>{t('plan_credentials.empty')}</div>}
        {modules.map(({ plugin, definition, loadingMetadata }) => (
          <section
            key={`${connectionFingerprint}:${plugin.id}`}
            aria-label={PLAN_PLUGIN_TITLES[plugin.id]}
          >
            <Card title={renderModuleTitle(plugin)}>
              <PluginCredentialForm
                pluginId={plugin.id}
                definition={definition}
                requestScope={requestScope}
                loadingMetadata={loadingMetadata}
                onCreated={() => {
                  const auth = useAuthStore.getState();
                  if (
                    !connectionFingerprint ||
                    auth.apiBase !== apiBase ||
                    auth.managementKey !== managementKey
                  )
                    return;
                  const provider = resolvePluginOAuthProviderId(plugin);
                  recordAccountCredentialMutationMarker({ connectionFingerprint, provider });
                  publishAccountCredentialMutationRevision({
                    connectionFingerprint,
                    provider,
                    kind: 'credential',
                  });
                }}
              />
            </Card>
          </section>
        ))}
      </div>
    </div>
  );
}
