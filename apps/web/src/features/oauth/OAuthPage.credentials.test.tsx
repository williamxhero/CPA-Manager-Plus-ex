import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Input } from '@/components/ui/Input';
import type { PluginListEntry } from '@/types';
import { OAuthPage } from './OAuthPage';
import { PlanCredentialsPage } from '@/features/planCredentials/PlanCredentialsPage';
import {
  getPluginCredentialFormFallback,
  readPluginCredentialForm,
} from './pluginCredentialMetadata';

const { mocks } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  return {
    mocks: {
      apiBase: 'http://cpa.test:8317',
      managementKey: 'test-management-key',
      plugins: [] as PluginListEntry[],
      startAuth: vi.fn(),
      submit: vi.fn(),
      listFiles: vi.fn(),
      notify: vi.fn(),
      publish: vi.fn(),
      marker: vi.fn(),
    },
  };
});
vi.mock('react-i18next', () => {
  const t = (key: string) => (key === 'plan_credentials.success' ? '凭证添加成功' : key);
  return {
    initReactI18next: { type: '3rdParty', init: () => undefined },
    useTranslation: () => ({ t }),
  };
});
vi.mock('react-router-dom', () => ({
  useLocation: () => ({ search: '', hash: '' }),
  useNavigate: () => vi.fn(),
}));
vi.mock('@/stores', () => {
  const getState = () => ({
    apiBase: mocks.apiBase,
    managementKey: mocks.managementKey,
    supportsPlugin: true,
    connectionStatus: 'connected',
  });
  return {
    useAuthStore: Object.assign(
      (select: (state: ReturnType<typeof getState>) => unknown) => select(getState()),
      { getState }
    ),
    useThemeStore: (select: (state: { resolvedTheme: 'light' }) => unknown) =>
      select({ resolvedTheme: 'light' }),
    useNotificationStore: () => ({ showNotification: mocks.notify }),
    publishAccountCredentialMutationRevision: mocks.publish,
  };
});
vi.mock('@/services/api', () => ({
  oauthApi: { startAuth: mocks.startAuth },
  authFilesApi: { list: mocks.listFiles },
  pluginsApi: { list: async () => ({ plugins: mocks.plugins }) },
}));
vi.mock('@/services/api/oauth', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/services/api/oauth')>()),
  oauthApi: { submitPluginCredential: mocks.submit },
}));
vi.mock('@/features/accounts/model/accountCredentialMutationMarker', () => ({
  createAccountCredentialMutationBaseline: vi.fn(),
  recordAccountCredentialMutationMarker: mocks.marker,
}));

const metadata = {
  auth_kind: 'manual_api_key',
  submit_path: '/v0/management/plugins/manual-plugin/credentials',
  submit_label: '保存凭证',
  fields: [
    { name: 'base_url', label: 'Base URL', placeholder: 'https://api.example/v1', required: true },
    { name: 'api_key', label: 'API Key', required: true },
    { name: 'name', label: '凭证名称', required: false },
    { name: 'token', label: 'Extra secret', type: 'password', required: false },
  ],
};
const plugin = (id = 'manual-plugin', oauthProvider = 'manual'): PluginListEntry => ({
  id,
  oauthProvider,
  supportsOAuth: true,
  enabled: true,
  effectiveEnabled: true,
  configured: true,
  registered: true,
  path: '',
  configFields: [],
  menus: [],
  logo: '/v0/management/plugins/manual-plugin/logo.svg',
  metadata: {
    name: 'Plugin-provided long title',
    logo: '',
    version: '',
    author: '',
    githubRepository: '',
    configFields: [],
  },
});
const text = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : text(child))).join('');
const findButton = (renderer: ReactTestRenderer, label: string) => {
  const button = renderer.root.findAllByType('button').find((node) => text(node) === label);
  if (!button) throw new Error(`Button not found: ${label}`);
  return button;
};
let renderer: ReactTestRenderer;
const render = async (page = <OAuthPage />) => {
  await act(async () => {
    renderer = create(page);
  });
};
const startManual = async () => {
  mocks.plugins = [plugin()];
  mocks.startAuth.mockResolvedValue({ url: 'https://plugin.example/login', metadata });
  await render();
  await act(async () => {
    await findButton(renderer, 'auth_login.plugin_oauth_button').props.onClick();
  });
};
const fill = async (name: string, value: string) => {
  await act(async () => {
    renderer.root
      .findAllByType(Input)
      .find((node) => node.props.name === name)!
      .props.onChange({ target: { value } });
  });
};
const submit = async () => {
  await act(async () => {
    await renderer.root.findByType('form').props.onSubmit({ preventDefault: vi.fn() });
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.apiBase = 'http://cpa.test:8317';
  mocks.managementKey = 'test-management-key';
  mocks.plugins = [];
  mocks.listFiles.mockResolvedValue({ files: [] });
  mocks.submit.mockResolvedValue(undefined);
  vi.stubGlobal('window', {
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    setInterval: vi.fn(() => 1),
    clearInterval: vi.fn(),
    setTimeout: vi.fn(() => 2),
    clearTimeout: vi.fn(),
  });
});
afterEach(async () => {
  if (renderer) await act(async () => renderer.unmount());
  vi.unstubAllGlobals();
});

describe('plugin manual credentials', () => {
  it('renders metadata fields, secret inputs, plugin title, and a root-relative logo', async () => {
    await startManual();
    expect(
      renderer.root
        .findAllByType(Input)
        .filter((node) => node.props.name)
        .map((node) => ({
          name: node.props.name,
          type: node.props.type,
          required: node.props.required,
        }))
    ).toEqual([
      { name: 'base_url', type: 'text', required: true },
      { name: 'api_key', type: 'password', required: true },
      { name: 'name', type: 'text', required: false },
      { name: 'token', type: 'password', required: false },
    ]);
    expect(findButton(renderer, '保存凭证')).toBeDefined();
    expect(text(renderer.root)).toContain('Plugin-provided long title');
    expect(
      renderer.root
        .findAllByType('img')
        .some(
          (node) =>
            node.props.src === 'http://cpa.test:8317/v0/management/plugins/manual-plugin/logo.svg'
        )
    ).toBe(true);
    expect(text(renderer.root)).not.toContain('https://plugin.example/login');
    expect(window.setInterval).not.toHaveBeenCalled();
  });

  it('submits only declared fields, publishes credential refresh, and clears secrets on success', async () => {
    await startManual();
    await fill('base_url', 'https://api.example/v1');
    await fill('api_key', 'secret-value');
    await fill('name', 'Work');
    await fill('token', 'extra-secret');
    await submit();
    expect(mocks.submit).toHaveBeenCalledWith(
      'manual-plugin',
      metadata.submit_path,
      {
        base_url: 'https://api.example/v1',
        api_key: 'secret-value',
        name: 'Work',
        token: 'extra-secret',
      },
      { apiBase: mocks.apiBase, managementKey: mocks.managementKey }
    );
    expect(mocks.publish).toHaveBeenCalledWith(
      expect.objectContaining({ provider: 'manual', kind: 'credential' })
    );
    expect(mocks.marker).toHaveBeenCalledOnce();
    expect(mocks.notify).toHaveBeenCalledWith('凭证添加成功', 'success');
    expect(
      renderer.root.findAllByType(Input).find((node) => node.props.name === 'api_key')!.props.value
    ).toBe('');
    expect(text(renderer.root)).not.toContain('secret-value');
  });

  it('surfaces plugin errors verbatim without success or credential refresh', async () => {
    mocks.submit.mockRejectedValue(new Error('plugin: invalid credential / quota exhausted'));
    await startManual();
    await fill('base_url', 'https://api.example/v1');
    await fill('api_key', 'secret-value');
    await submit();
    expect(renderer.root.findByProps({ role: 'alert' }).children).toEqual([
      'plugin: invalid credential / quota exhausted',
    ]);
    expect(mocks.notify).toHaveBeenCalledWith(
      'plugin: invalid credential / quota exhausted',
      'error'
    );
    expect(mocks.publish).not.toHaveBeenCalled();
    expect(mocks.marker).not.toHaveBeenCalled();
    expect(mocks.notify).not.toHaveBeenCalledWith(expect.anything(), 'success');
  });

  it('redacts submitted secrets if a plugin error echoes them', async () => {
    mocks.submit.mockRejectedValue(new Error('invalid secret-value'));
    await startManual();
    await fill('base_url', 'https://api.example/v1');
    await fill('api_key', 'secret-value');
    await submit();
    expect(mocks.notify).toHaveBeenCalledWith('invalid [redacted]', 'error');
    expect(text(renderer.root)).not.toContain('secret-value');
  });

  it('redacts longer secrets before overlapping shorter ones', async () => {
    mocks.submit.mockRejectedValue(new Error('invalid sk-prod-unique'));
    await startManual();
    await fill('base_url', 'https://api.example/v1');
    await fill('api_key', 'sk-prod');
    await fill('token', 'sk-prod-unique');
    await submit();
    expect(mocks.notify).toHaveBeenCalledWith('invalid [redacted]', 'error');
    expect(text(renderer.root)).not.toContain('-unique');
  });

  it('shows both plan modules while a manual metadata probe is pending', async () => {
    let resolve!: (response: unknown) => void;
    mocks.startAuth.mockReturnValue(
      new Promise((done) => {
        resolve = done;
      })
    );
    mocks.plugins = [
      plugin('qwen-cliproxyapi', 'qwen'),
      plugin('opencode-go-cliproxyapi', 'opencode-go'),
    ];
    await render(<PlanCredentialsPage />);
    expect(renderer.root.findAllByType('form')).toHaveLength(2);
    expect(findButton(renderer, '添加凭证').props.disabled).toBe(true);
    await act(async () => {
      resolve({ url: 'https://plugin.example/login' });
    });
    expect(findButton(renderer, '添加凭证').props.disabled).toBe(false);
  });

  it('uses only the exact plugin-id fallback when login metadata is absent', async () => {
    mocks.plugins = [plugin('qwen-cliproxyapi', 'qwen')];
    mocks.startAuth.mockResolvedValue({
      url: 'https://plugin.example/login',
      state: 'unused-state',
    });
    await render(<PlanCredentialsPage />);
    expect(findButton(renderer, '添加凭证')).toBeDefined();
    expect(
      renderer.root
        .findAllByType(Input)
        .filter((node) => node.props.name)
        .map((node) => node.props.name)
    ).toEqual(['name', 'base_url', 'api_key']);
    expect(window.setInterval).not.toHaveBeenCalled();
    expect(getPluginCredentialFormFallback('opencode-go')).toBeUndefined();
    expect(getPluginCredentialFormFallback('qwen')).toBeUndefined();
  });

  it('prefers Qwen login-start metadata over the allowlist fallback', async () => {
    mocks.plugins = [plugin('qwen-cliproxyapi', 'qwen')];
    mocks.startAuth.mockResolvedValue({
      metadata: {
        ...metadata,
        fields: metadata.fields.slice(0, 3),
        submit_path: '/v0/management/plugins/qwen-cliproxyapi/alternate',
      },
    });
    await render(<PlanCredentialsPage />);
    expect(findButton(renderer, '保存凭证')).toBeDefined();
  });

  it('retains fallback if an older host rejects the metadata probe', async () => {
    mocks.plugins = [plugin('qwen-cliproxyapi', 'qwen')];
    mocks.startAuth.mockRejectedValue(new Error('unsupported'));
    await render(<PlanCredentialsPage />);
    expect(findButton(renderer, '添加凭证')).toBeDefined();
  });

  it('keeps non-manual plugins and all built-in login buttons and polling untouched', async () => {
    mocks.plugins = [plugin('opencode-go', 'opencode-go')];
    mocks.startAuth.mockResolvedValue({
      url: 'https://oauth.example/login',
      state: 'oauth-state',
      metadata,
    });
    await render();
    expect(mocks.startAuth).not.toHaveBeenCalled();
    for (const id of ['codex', 'anthropic', 'antigravity', 'kimi', 'xai', 'devin', 'meta']) {
      expect(findButton(renderer, `auth_login.${id}_oauth_button`)).toBeDefined();
    }
    await act(async () => {
      await findButton(renderer, 'auth_login.codex_oauth_button').props.onClick();
    });
    expect(renderer.root.findAllByType('form')).toHaveLength(0);
    expect(window.setInterval).toHaveBeenCalledOnce();
    mocks.startAuth.mockResolvedValue({
      url: 'https://oauth.example/opencode',
      state: 'plugin-state',
    });
    await act(async () => {
      await findButton(renderer, 'auth_login.plugin_oauth_button').props.onClick();
    });
    expect(text(renderer.root)).toContain('https://oauth.example/opencode');
    expect(window.setInterval).toHaveBeenCalledTimes(2);
  });

  it('does not submit empty required values', async () => {
    await startManual();
    await submit();
    expect(mocks.submit).not.toHaveBeenCalled();
  });

  it('ignores late submission success after the management connection changes', async () => {
    let resolve!: () => void;
    mocks.submit.mockReturnValue(
      new Promise<void>((done) => {
        resolve = done;
      })
    );
    await startManual();
    await fill('base_url', 'https://api.example/v1');
    await fill('api_key', 'secret-value');
    let pending!: Promise<void>;
    await act(async () => {
      pending = renderer.root.findByType('form').props.onSubmit({ preventDefault: vi.fn() });
    });
    mocks.apiBase = 'http://other-cpa.test:8317';
    await act(async () => {
      renderer.update(<OAuthPage />);
    });
    await act(async () => {
      resolve();
      await pending;
    });
    expect(mocks.publish).not.toHaveBeenCalled();
    expect(mocks.notify).not.toHaveBeenCalled();
  });

  it('defaults the submit label and rejects unsafe metadata destinations and duplicate fields', () => {
    expect(
      readPluginCredentialForm('manual-plugin', { ...metadata, submit_label: undefined })
        ?.submitLabel
    ).toBe('添加凭证');
    expect(() =>
      readPluginCredentialForm('manual-plugin', {
        ...metadata,
        submit_path: 'https://untrusted.example/credentials',
      })
    ).toThrow('Invalid plugin credential form metadata');
    expect(() =>
      readPluginCredentialForm('manual-plugin', {
        ...metadata,
        fields: [metadata.fields[0], metadata.fields[0]],
      })
    ).toThrow('Invalid plugin credential form field');
  });
});
