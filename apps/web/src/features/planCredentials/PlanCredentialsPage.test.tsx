import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Input } from '@/components/ui/Input';
import { OAuthPage } from '@/features/oauth/OAuthPage';
import type { PluginListEntry } from '@/types';
import zhCN from '@/i18n/locales/zh-CN.json';
import { PlanCredentialsPage } from './PlanCredentialsPage';

const { mocks } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  return {
    mocks: {
      apiBase: 'http://cpa.test:8317',
      managementKey: 'test-key',
      plugins: [] as PluginListEntry[],
      list: vi.fn(),
      start: vi.fn(),
      submit: vi.fn(),
      publish: vi.fn(),
      marker: vi.fn(),
      notify: vi.fn(),
    },
  };
});
vi.mock('react-i18next', () => {
  const t = (key: string, options?: Record<string, string>) => {
    const [namespace, name] = key.split('.');
    const strings = zhCN as unknown as Record<string, Record<string, string>>;
    let value = strings[namespace]?.[name] ?? key;
    for (const [field, replacement] of Object.entries(options ?? {})) {
      value = value.split(`{{${field}}}`).join(replacement);
    }
    return value;
  };
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
  oauthApi: { startAuth: mocks.start },
  pluginsApi: { list: mocks.list },
  authFilesApi: { list: async () => ({ files: [] }) },
}));
vi.mock('@/services/api/oauth', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/services/api/oauth')>()),
  oauthApi: { submitPluginCredential: mocks.submit },
}));
vi.mock('@/features/accounts/model/accountCredentialMutationMarker', () => ({
  recordAccountCredentialMutationMarker: mocks.marker,
  createAccountCredentialMutationBaseline: vi.fn(),
}));

const ids = ['qwen-cliproxyapi', 'opencode-go-cliproxyapi'] as const;
const plugin = (id: string, provider: string): PluginListEntry => ({
  id,
  oauthProvider: provider,
  supportsOAuth: true,
  enabled: true,
  effectiveEnabled: true,
  configured: true,
  registered: true,
  path: '',
  configFields: [],
  menus: [],
  logo: '',
  metadata: null,
});
const metadata = (id: string) => ({
  auth_kind: 'manual_api_key',
  submit_path: `/v0/management/plugins/${id}/credentials`,
  submit_label: '添加',
  fields: [
    {
      name: 'base_url',
      label: 'Base URL',
      placeholder: `https://${id}.example/v1`,
      required: true,
    },
    { name: 'api_key', label: 'API Key', type: 'password', required: true },
    { name: 'name', label: '别名 (Alias)', placeholder: '留空则显示脱敏 API Key', required: false },
  ],
});
const text = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : text(child))).join('');
let renderer: ReactTestRenderer | undefined;
const render = async (page = <PlanCredentialsPage />) => {
  await act(async () => {
    renderer = create(page);
  });
};
const section = (index = 0) => renderer!.root.findAllByType('section')[index];
const fill = async (name: string, value: string, index = 0) => {
  await act(async () => {
    section(index)
      .findAllByType(Input)
      .find((input) => input.props.name === name)!
      .props.onChange({ target: { value } });
  });
};
const submit = async (index = 0) => {
  await act(async () => {
    await section(index).findByType('form').props.onSubmit({ preventDefault: vi.fn() });
  });
};
const valid = async (index = 0, alias = 'Work') => {
  await fill('base_url', 'https://upstream.example/v1', index);
  await fill('api_key', 'test-secret-key', index);
  await fill('name', alias, index);
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.apiBase = 'http://cpa.test:8317';
  mocks.managementKey = 'test-key';
  mocks.plugins = [plugin(ids[0], 'qwen'), plugin(ids[1], 'opencode-go')];
  mocks.list.mockImplementation(async () => ({ plugins: mocks.plugins }));
  mocks.start.mockImplementation(async (provider: string) => ({
    metadata: metadata(provider === 'qwen' ? ids[0] : ids[1]),
  }));
  mocks.submit.mockResolvedValue({ ok: true, id: 'credential-1', label: 'Work' });
  vi.stubGlobal('window', {
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    setInterval: vi.fn(),
    clearInterval: vi.fn(),
    setTimeout: vi.fn(),
    clearTimeout: vi.fn(),
  });
});
afterEach(async () => {
  if (renderer)
    await act(async () => {
      renderer!.unmount();
    });
  renderer = undefined;
  vi.unstubAllGlobals();
});

describe('PlanCredentialsPage', () => {
  it('allows a blank URL to use the provider default and shows alias before URL and key', async () => {
    await render();
    expect(
      section()
        .findAllByType(Input)
        .map((node) => node.props.name)
    ).toEqual(['name', 'base_url', 'api_key']);
    await fill('api_key', 'synthetic-key-a');
    await submit();
    expect(mocks.submit).toHaveBeenCalledWith(
      ids[0],
      expect.any(String),
      { name: '', base_url: '', api_key: 'synthetic-key-a' },
      expect.anything()
    );
  });
  it('renders one module per plan plugin with host field names, labels, placeholders and zh-CN title', async () => {
    mocks.plugins.push(plugin('other-plugin', 'other'));
    await render();
    expect(renderer!.root.findAllByType('section')).toHaveLength(2);
    expect(renderer!.root.findByType('h1').children).toEqual(['添加凭证']);
    expect(renderer!.root.findAllByType('section').map((node) => node.props['aria-label'])).toEqual(
      ['Qwen Token Plan', 'OpenCode Go']
    );
    const titleIcons = renderer!.root
      .findAllByType('section')
      .map((node) => node.findByType('img').props.src);
    expect(titleIcons.every((src: string) => typeof src === 'string' && src.length > 0)).toBe(true);
    expect(titleIcons[0]).not.toBe(titleIcons[1]);
    for (const [index, id] of ids.entries()) {
      const fields = section(index)
        .findAllByType(Input)
        .map((node) => ({
          name: node.props.name,
          label: node.props.label,
          placeholder: node.props.placeholder,
          type: node.props.type,
          required: node.props.required,
        }));
      expect(fields).toEqual(
        [...metadata(id).fields]
          .sort(
            (left, right) =>
              ['name', 'base_url', 'api_key'].indexOf(left.name) -
              ['name', 'base_url', 'api_key'].indexOf(right.name)
          )
          .map((field) => ({
            ...field,
            required: field.name === 'base_url' ? false : field.required,
            type: field.name === 'api_key' ? 'password' : 'text',
          }))
      );
      expect(text(section(index).findByType('button'))).toBe('添加');
    }
    expect(mocks.start.mock.calls.map(([provider]) => provider)).toEqual(['qwen', 'opencode-go']);
  });

  it.each(['stripped', 'rejected'] as const)(
    'renders both plugin-specific fallbacks when host metadata is %s',
    async (mode) => {
      if (mode === 'stripped') mocks.start.mockResolvedValue({ url: '' });
      else mocks.start.mockRejectedValue(new Error('unsupported'));
      await render();
      for (const [index, placeholder] of [
        'https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1',
        'https://opencode.ai/zen/go/v1',
      ].entries()) {
        expect(
          section(index)
            .findAllByType(Input)
            .map((node) => ({
              name: node.props.name,
              label: node.props.label,
              placeholder: node.props.placeholder,
            }))
        ).toEqual([
          { name: 'name', label: '别名 (Alias)', placeholder: '留空则显示脱敏 API Key' },
          { name: 'base_url', label: 'Base URL', placeholder },
          { name: 'api_key', label: 'API Key', placeholder: undefined },
        ]);
        expect(text(section(index).findByType('button'))).toBe('添加凭证');
      }
    }
  );

  it.each([
    ['Base URL containing whitespace', 'https://bad host/v1', 'key', 'Base URL'],
    ['missing API Key', 'https://valid.example', '  ', 'API Key'],
    ['invalid Base URL', 'not-a-url', 'key', 'Base URL'],
    ['non-HTTP URL', 'javascript:alert(1)', 'key', 'Base URL'],
  ])('validates %s locally without posting', async (_case, baseURL, key, field) => {
    await render();
    await fill('base_url', baseURL);
    await fill('api_key', key);
    await submit();
    expect(text(section().findByProps({ role: 'alert' }))).toContain(field);
    expect(mocks.submit).not.toHaveBeenCalled();
  });

  it.each(ids)(
    'submits the declared URL/body for %s, shows its label, clears all fields and refreshes credentials',
    async (id) => {
      const index = ids.indexOf(id);
      await render();
      await valid(index);
      await submit(index);
      expect(mocks.submit).toHaveBeenCalledWith(
        id,
        metadata(id).submit_path,
        {
          base_url: 'https://upstream.example/v1',
          api_key: 'test-secret-key',
          name: 'Work',
        },
        { apiBase: mocks.apiBase, managementKey: mocks.managementKey }
      );
      expect(text(section(index).findByProps({ role: 'status' }))).toBe('凭证添加成功: Work');
      expect(
        section(index)
          .findAllByType(Input)
          .map((node) => node.props.value)
      ).toEqual(['', '', '']);
      expect(mocks.publish).toHaveBeenCalledWith(
        expect.objectContaining({
          provider: index === 0 ? 'qwen' : 'opencode-go',
          kind: 'credential',
        })
      );
      expect(mocks.marker).toHaveBeenCalledOnce();
      expect(section(1 - index).findAllByProps({ role: 'status' })).toHaveLength(0);
    }
  );

  it('uses the returned masked-key label when alias is blank, without displaying the raw key', async () => {
    mocks.submit.mockResolvedValue({ ok: true, id: 'credential-2', label: 'sk-***1234' });
    await render();
    await valid(0, '');
    await submit();
    expect(text(section().findByProps({ role: 'status' }))).toContain('sk-***1234');
    expect(text(renderer!.root)).not.toContain('test-secret-key');
    expect(mocks.submit.mock.calls[0][2].name).toBe('');
  });

  it.each(
    ids.flatMap((id) => [
      { id, status: 409, message: 'duplicate api key' },
      { id, status: 400, message: 'invalid base_url' },
      { id, status: 404, message: 'plugin route not found' },
      { id, status: undefined, message: 'Network Error' },
    ])
  )('surfaces $status / $message honestly for $id', async ({ id, status, message }) => {
    mocks.submit.mockRejectedValue(Object.assign(new Error(message), { status }));
    const index = ids.indexOf(id);
    await render();
    await valid(index);
    await submit(index);
    expect(text(section(index).findByProps({ role: 'alert' }))).toBe(
      status === 409 ? `凭证已存在: ${message}` : message
    );
    expect(mocks.publish).not.toHaveBeenCalled();
    expect(mocks.marker).not.toHaveBeenCalled();
    expect(mocks.notify).not.toHaveBeenCalledWith(expect.anything(), 'success');
    expect(section(index).findAllByProps({ role: 'status' })).toHaveLength(0);
  });

  it('disables only the in-flight module, blocks duplicate submits, and keeps the other module independent', async () => {
    let resolve!: (value: unknown) => void;
    mocks.submit.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done;
      })
    );
    await render();
    await valid();
    let pending!: Promise<void>;
    await act(async () => {
      pending = section().findByType('form').props.onSubmit({ preventDefault: vi.fn() });
    });
    expect(section().findByType('button').props.disabled).toBe(true);
    expect(section(1).findByType('button').props.disabled).toBe(false);
    await submit();
    expect(mocks.submit).toHaveBeenCalledOnce();
    await valid(1);
    await submit(1);
    expect(text(section(1).findByProps({ role: 'status' }))).toContain('Work');
    await act(async () => {
      resolve({ ok: true, id: 'qwen-1', label: 'Qwen Work' });
      await pending;
    });
    expect(text(section().findByProps({ role: 'status' }))).toContain('Qwen Work');
    expect(section().findByType('button').props.disabled).toBe(false);
  });

  it.each(ids)(
    'still synchronizes %s credential creation after navigating away during a pending POST',
    async (id) => {
      const index = ids.indexOf(id);
      let resolve!: (value: unknown) => void;
      mocks.submit.mockReturnValue(
        new Promise((done) => {
          resolve = done;
        })
      );
      await render();
      await valid(index);
      let pending!: Promise<void>;
      await act(async () => {
        pending = section(index).findByType('form').props.onSubmit({ preventDefault: vi.fn() });
      });
      await act(async () => {
        renderer!.unmount();
      });
      renderer = undefined;
      await act(async () => {
        resolve({ ok: true, id: 'created-after-navigation', label: 'Work' });
        await pending;
      });
      expect(mocks.marker).toHaveBeenCalledOnce();
      expect(mocks.publish).toHaveBeenCalledWith(
        expect.objectContaining({
          provider: index === 0 ? 'qwen' : 'opencode-go',
          kind: 'credential',
        })
      );
      expect(mocks.notify).not.toHaveBeenCalled();
    }
  );

  it('does not publish stale credential success after changing management connections', async () => {
    let resolve!: (value: unknown) => void;
    mocks.submit.mockReturnValue(
      new Promise((done) => {
        resolve = done;
      })
    );
    await render();
    await valid();
    let pending!: Promise<void>;
    await act(async () => {
      pending = section().findByType('form').props.onSubmit({ preventDefault: vi.fn() });
    });
    mocks.apiBase = 'http://other-cpa.test:8317';
    await act(async () => {
      renderer!.update(<PlanCredentialsPage />);
    });
    await act(async () => {
      resolve({ ok: true, id: 'old-id', label: 'Old label' });
      await pending;
    });
    expect(mocks.publish).not.toHaveBeenCalled();
    expect(mocks.notify).not.toHaveBeenCalled();
    expect(text(renderer!.root)).not.toContain('Old label');
  });

  it('keeps the moved plugins out of OAuth while retaining built-in and unrelated plugin providers', async () => {
    mocks.plugins.push(plugin('other-plugin', 'other'));
    await render(<OAuthPage />);
    expect(renderer!.root.findAllByProps({ id: 'oauth-provider-qwen' })).toHaveLength(0);
    expect(renderer!.root.findAllByProps({ id: 'oauth-provider-opencode-go' })).toHaveLength(0);
    expect(renderer!.root.findByProps({ id: 'oauth-provider-other' })).toBeDefined();
    for (const provider of ['codex', 'anthropic', 'antigravity', 'kimi', 'xai', 'devin', 'meta']) {
      expect(renderer!.root.findByProps({ id: `oauth-provider-${provider}` })).toBeDefined();
    }
    expect(renderer!.root.findAllByType('form')).toHaveLength(0);
    expect(mocks.start).not.toHaveBeenCalled();
  });

  it('surfaces plugin-list errors rather than showing a false empty success', async () => {
    mocks.list.mockRejectedValue(new Error('Unable to load plugins'));
    await render();
    expect(text(renderer!.root.findByProps({ role: 'alert' }))).toBe('Unable to load plugins');
    expect(renderer!.root.findAllByType('form')).toHaveLength(0);
  });
});
