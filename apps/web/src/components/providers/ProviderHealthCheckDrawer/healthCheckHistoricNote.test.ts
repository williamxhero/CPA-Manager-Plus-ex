import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('health check historic note (#84)', () => {
  it('ships bilingual copy that separates probes from current monetary evidence', () => {
    const zh = JSON.parse(
      readFileSync(resolve(__dirname, '../../../i18n/locales/zh-CN.json'), 'utf8')
    );
    const en = JSON.parse(
      readFileSync(resolve(__dirname, '../../../i18n/locales/en.json'), 'utf8')
    );
    expect(zh.ai_providers.health_check_historic_note).toMatch(/不是当前资金策略/);
    expect(en.ai_providers.health_check_historic_note).toMatch(/not current monetary policy/i);
    expect(zh.ai_providers.health_check_manual_config_toggle).toMatch(/非资金策略/);
    expect(en.ai_providers.health_check_manual_config_toggle).toMatch(/not monetary policy/i);
  });

  it('renders the historic note and config-only toggle aria in the drawer source', () => {
    const source = readFileSync(resolve(__dirname, './ProviderHealthCheckDrawer.tsx'), 'utf8');
    expect(source).toContain('health-check-historic-note');
    expect(source).toContain('health_check_historic_note');
    expect(source).toContain('health_check_manual_config_toggle');
  });
});
