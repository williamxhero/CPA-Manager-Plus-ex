export const PLAN_PLUGIN_TITLES: Readonly<Record<string, string>> = {
  'cpa-credential-qwen': 'Qwen Token Plan',
  'cpa-credential-opencode': 'OpenCode Go',
};

export const isPlanCredentialPlugin = (pluginId: string): boolean =>
  Object.prototype.hasOwnProperty.call(PLAN_PLUGIN_TITLES, pluginId);
