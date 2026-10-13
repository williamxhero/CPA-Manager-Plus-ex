export const PLAN_PLUGIN_TITLES: Readonly<Record<string, string>> = {
  'qwen-cliproxyapi': 'Qwen Token Plan',
  'opencode-go-cliproxyapi': 'OpenCode Go',
};

export const isPlanCredentialPlugin = (pluginId: string): boolean =>
  Object.prototype.hasOwnProperty.call(PLAN_PLUGIN_TITLES, pluginId);
