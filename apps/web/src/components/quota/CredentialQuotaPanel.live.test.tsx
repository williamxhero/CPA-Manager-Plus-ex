import { readFileSync } from 'node:fs';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { expect, it } from 'vitest';
import { CredentialQuotaPanel } from './CredentialQuotaPanel';

const fixturePath = process.env.CREDENTIAL_QUOTA_UI_FIXTURE;
it.skipIf(!fixturePath)(
  'live React -> Manager -> SQLite -> compiled actual Core selected OAuth -> TLS',
  async () => {
    const fixture = JSON.parse(readFileSync(fixturePath!, 'utf8'));
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <CredentialQuotaPanel
          base={fixture.base}
          managementKey={fixture.managementKey}
          authIndex={fixture.authIndex}
        />
      );
    });
    const waitText = async (text: string) => {
      for (let i = 0; i < 200 && !JSON.stringify(renderer.toJSON()).includes(text); i++) {
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 20));
        });
      }
      expect(JSON.stringify(renderer.toJSON())).toContain(text);
    };
    await waitText('25');
    expect(JSON.stringify(renderer.toJSON())).toContain('available');
    const click = async () => {
      await act(async () => {
        const button = renderer.root.findByType('button');
        expect(button.props.disabled).toBeFalsy();
        button.props.onClick();
      });
      for (let i = 0; i < 200 && renderer.root.findByType('button').props.disabled; i++) {
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 20));
        });
      }
      expect(renderer.root.findByType('button').props.disabled).toBeFalsy();
    };
    await click();
    await waitText('success');
    await fetch(fixture.control, { method: 'POST' });
    await click();
    await waitText('auth_error');
    await waitText('stale');
    expect(JSON.stringify(renderer.toJSON())).toContain('25');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('CANARY');
    await act(async () => {
      renderer.update(
        <CredentialQuotaPanel base={fixture.base} managementKey={fixture.managementKey} />
      );
    });
    expect(JSON.stringify(renderer.toJSON())).not.toContain('25');
    await act(async () => renderer.unmount());
  },
  15000
);
