import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { OfficialBalancePanel } from './OfficialBalancePanel';
import { ProviderDetailDrawer } from './ProviderDetailDrawer/ProviderDetailDrawer';
import { buildProviderRows } from './ProviderTable/rowData';
import type { BalanceBinding, OfficialBalanceStatus } from '@/services/api/officialBalance';

afterEach(() => vi.unstubAllGlobals());

const binding: BalanceBinding = {
  scope: {
    instance_id: 'issued-instance',
    instance_incarnation: 'issued-incarnation',
    boot_epoch: 'issued-boot',
    snapshot_revision: 1,
  },
  targets: [
    {
      resource_id: 'issued-resource',
      incarnation: 'issued-resource-incarnation',
      identity_version: 1,
      credential_version: 1,
      config_version: 1,
      membership_version: 1,
    },
  ],
};
const balanceStatus = (version: number): OfficialBalanceStatus => ({
  ...binding,
  version,
  adapter: 'deepseek',
  endpoint: 'https://api.deepseek.com/user/balance',
  configured: true,
  state: 'configured',
  managerDirectQuery: true,
  coreQuery: false,
  coreGate: false,
  stale: false,
  lastSuccess: {
    queryStatus: 'success',
    observedAtMs: 1793500000000,
    semantic: 'account_wallet',
    amountDecimal: '41.25',
    currency: 'USD',
    available: true,
    balances: [{ currency: 'USD', total: '41.25', granted: '0', toppedUp: '41.25' }],
  },
});

const fixtureDir = process.env.OFFICIAL_BALANCE_UI_FIXTURE;
it.skipIf(!fixtureDir)(
  'provider config -> exact wallet -> auth failure retains stale wallet through live Manager/SQLite/TLS',
  async () => {
    const f = JSON.parse(readFileSync(`${fixtureDir}/fixture.json`, 'utf8'));
    expect(f.binding.targets).toHaveLength(2);
    const row = buildProviderRows({
      gemini: [],
      codex: [],
      claude: [],
      vertex: [],
      openai: [
        {
          name: 'Synthetic DeepSeek',
          baseUrl: 'https://api.deepseek.com/v1',
          resourceId: f.binding.targets[0].resource_id,
          apiKeyEntries: [
            {
              apiKey: 'SYNTHETIC-INFERENCE-NOT-BILLING',
              resourceId: f.binding.targets[1].resource_id,
            },
          ],
        },
      ],
      usageByProvider: new Map(),
    })[0];
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <ProviderDetailDrawer
          row={row}
          open
          usageByProvider={new Map()}
          resolvedTheme="light"
          actionsDisabled={false}
          toggleDisabled={false}
          onClose={() => {}}
          onEdit={() => {}}
          onDelete={() => {}}
          onToggle={() => {}}
          onToggleWebsockets={() => {}}
          onToggleCloak={() => {}}
          onToggleDisableCooling={() => {}}
          officialBalance={{ base: f.base, managementKey: f.managementKey, binding: f.binding }}
        />
      );
    });
    for (
      let i = 0;
      i < 100 && !JSON.stringify(renderer.toJSON()).includes('needs_configuration');
      i++
    ) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, 10));
      });
    }
    expect(JSON.stringify(renderer.toJSON())).toContain('needs_configuration');
    const waitText = async (text: string) => {
      for (let i = 0; i < 200 && !JSON.stringify(renderer.toJSON()).includes(text); i++) {
        await act(async () => {
          await new Promise((r) => setTimeout(r, 10));
        });
      }
      expect(JSON.stringify(renderer.toJSON())).toContain(text);
    };
    const change = async (label: string, value: string) => {
      await act(async () => {
        renderer.root.findByProps({ 'aria-label': label }).props.onChange({ target: { value } });
      });
    };
    const click = async (label: string) => {
      await act(async () => {
        const button = renderer.root
          .findAllByType('button')
          .find((node) => node.children.join('') === label)!;
        expect(button.props.disabled).toBeFalsy();
        button.props.onClick();
      });
    };
    await change('只读查询密钥', 'synthetic-query-canary');
    await change('查询 HTTPS 地址', f.endpoint);
    await change('允许的私网 IP', '127.0.0.1');
    await click('保存余额来源');
    await waitText('configured');
    // The editor clears after encrypted server write plus authenticated readback.
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.value).toBe('');
    await click('手动查询余额');
    await waitText('-0.000000001');
    expect(JSON.stringify(renderer.toJSON())).toContain('account_wallet');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('synthetic-query-canary');
    await fetch(f.control, { method: 'PUT', body: JSON.stringify({ response: 'status:401' }) });
    await click('手动查询余额');
    await waitText('auth_error');
    await waitText('stale');
    expect(JSON.stringify(renderer.toJSON())).toContain('-0.000000001');
    expect(JSON.stringify(renderer.toJSON())).toContain('最近成功');
    await change('允许的私网 IP', '');
    expect(renderer.root.findByProps({ 'aria-label': '允许的私网 IP' }).props.value).toBe('');
    await click('保存余额来源');
    for (
      let i = 0;
      i < 100 && renderer.root.findAllByType('button').some((node) => node.props.disabled);
      i++
    ) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, 10));
      });
    }
    const readback = await fetch(
      `${f.base}/usage-service/official-balance?binding=${encodeURIComponent(JSON.stringify(f.binding))}`,
      { headers: { Authorization: `Bearer ${f.managementKey}` } }
    ).then((r) => r.json());
    expect(readback.privateTargets ?? []).toEqual([]);
    // Switching target must not render the previous target's secret or wallet.
    await act(async () => {
      renderer.update(
        <OfficialBalancePanel base={f.base} managementKey={f.managementKey} binding={undefined} />
      );
    });
    expect(JSON.stringify(renderer.toJSON())).toContain('identity_unavailable');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('-0.000000001');

    await act(async () => {
      renderer.unmount();
    });
  }
);

it('keeps an unimplemented source honestly unsupported, without configuration or query controls', async () => {
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(<OfficialBalancePanel base="" managementKey="" supported={false} />);
  });
  expect(JSON.stringify(renderer.toJSON())).toContain('unsupported');
  expect(renderer.root.findAllByType('input')).toHaveLength(0);
  expect(renderer.root.findAllByType('button')).toHaveLength(0);
  await act(async () => {
    renderer.unmount();
  });
});

it('locks the stale baseline and distinguishes a saved source from failed readback', async () => {
  let saved = false;
  const request = vi.fn(async (_url: string, options?: RequestInit) => {
    if (options?.method === 'PUT') {
      saved = true;
      return Response.json({ version: 8 });
    }
    return saved ? new Response(null, { status: 503 }) : Response.json(balanceStatus(7));
  });
  vi.stubGlobal('fetch', request);
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
      );
    });
    const button = (name: string) =>
      renderer.root.findAllByType('button').find((node) => node.children.join('') === name)!;
    await act(async () => button('保存余额来源').props.onClick());
    expect(button('保存余额来源').props.disabled).toBe(true);
    expect(button('手动查询余额').props.disabled).toBe(true);
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.disabled).toBe(true);
    expect(JSON.stringify(renderer.toJSON())).toContain('已保存，但回读失败');
    expect(JSON.stringify(renderer.toJSON())).toContain('41.25');
    await act(async () => {
      button('保存余额来源').props.onClick();
      button('手动查询余额').props.onClick();
    });
    expect(request.mock.calls.filter(([, options]) => options?.method === 'PUT')).toHaveLength(1);
    expect(request.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(0);
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('retries readback without replaying the write and only unlocks the recovered version', async () => {
  let failReads = false;
  let recovery: Promise<Response> | undefined;
  const writtenVersions: number[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        writtenVersions.push(JSON.parse(String(options.body)).version);
        failReads = writtenVersions.length === 1;
        return Response.json({});
      }
      if (failReads) return new Response(null, { status: 503 });
      return recovery ?? Response.json(balanceStatus(writtenVersions.length ? 9 : 7));
    })
  );
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
      );
    });
    const button = (name: string) =>
      renderer.root.findAllByType('button').find((node) => node.children.join('') === name)!;
    await act(async () =>
      renderer.root
        .findByProps({ 'aria-label': '只读查询密钥' })
        .props.onChange({ target: { value: 'synthetic-query-secret' } })
    );
    await act(async () => button('保存余额来源').props.onClick());
    expect(button('重试回读')).toBeDefined();
    await act(async () => button('重试回读').props.onClick());
    expect(button('保存余额来源').props.disabled).toBe(true);
    expect(JSON.stringify(renderer.toJSON())).toContain('已保存，但回读失败');
    expect(writtenVersions).toEqual([7]);

    let finishRecovery!: (response: Response) => void;
    recovery = new Promise<Response>((resolve) => {
      finishRecovery = resolve;
    });
    failReads = false;
    await act(async () => button('重试回读').props.onClick());
    expect(button('重试回读').props.disabled).toBe(true);
    expect(button('保存余额来源').props.disabled).toBe(true);
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.value).toBe(
      'synthetic-query-secret'
    );
    await act(async () => finishRecovery(Response.json(balanceStatus(8))));
    recovery = undefined;
    expect(button('保存余额来源').props.disabled).toBeFalsy();
    expect(button('手动查询余额').props.disabled).toBeFalsy();
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.value).toBe('');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('已保存，但回读失败');
    expect(writtenVersions).toEqual([7]);
    await act(async () => button('保存余额来源').props.onClick());
    expect(writtenVersions).toEqual([7, 8]);
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('keeps a failed write editable without reporting it as saved', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) =>
      options?.method === 'PUT'
        ? new Response(null, { status: 503 })
        : Response.json(balanceStatus(7))
    )
  );
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
      );
    });
    await act(async () =>
      renderer.root
        .findByProps({ 'aria-label': '只读查询密钥' })
        .props.onChange({ target: { value: 'synthetic-query-secret' } })
    );
    await act(async () =>
      renderer.root
        .findAllByType('button')
        .find((node) => node.children.join('') === '保存余额来源')!
        .props.onClick()
    );
    expect(
      renderer.root
        .findAllByType('button')
        .find((node) => node.children.join('') === '保存余额来源')!.props.disabled
    ).toBeFalsy();
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.value).toBe(
      'synthetic-query-secret'
    );
    expect(JSON.stringify(renderer.toJSON())).toContain('balance_request_failed_reload_identity');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('已保存，但回读失败');
    expect(
      renderer.root.findAllByType('button').some((node) => node.children.join('') === '重试回读')
    ).toBe(false);
  } finally {
    await act(async () => renderer.unmount());
  }
});

it('does not publish an old recovery or clear the editor after the source changes', async () => {
  const nextBinding: BalanceBinding = {
    ...binding,
    targets: binding.targets.map((target) => ({ ...target, resource_id: 'new-issued-resource' })),
  };
  let saved = false;
  let recovering = false;
  let finishRecovery!: (response: Response) => void;
  const recovery = new Promise<Response>((resolve) => {
    finishRecovery = resolve;
  });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        saved = true;
        return Response.json({});
      }
      if (decodeURIComponent(url).includes('new-issued-resource'))
        return Response.json({ ...balanceStatus(37), ...nextBinding, state: 'new-source-ready' });
      if (recovering) return recovery;
      return saved ? new Response(null, { status: 503 }) : Response.json(balanceStatus(7));
    })
  );
  let renderer!: ReactTestRenderer;
  try {
    await act(async () => {
      renderer = create(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={binding} />
      );
    });
    const button = (name: string) =>
      renderer.root.findAllByType('button').find((node) => node.children.join('') === name)!;
    await act(async () => button('保存余额来源').props.onClick());
    recovering = true;
    await act(async () => button('重试回读').props.onClick());
    await act(async () => {
      renderer.update(
        <OfficialBalancePanel base="http://fixture" managementKey="test" binding={nextBinding} />
      );
    });
    await act(async () =>
      renderer.root
        .findByProps({ 'aria-label': '只读查询密钥' })
        .props.onChange({ target: { value: 'new-source-draft' } })
    );
    await act(async () => finishRecovery(Response.json(balanceStatus(8))));
    expect(JSON.stringify(renderer.toJSON())).toContain('new-source-ready');
    expect(JSON.stringify(renderer.toJSON())).not.toContain('已保存，但回读失败');
    expect(renderer.root.findByProps({ 'aria-label': '只读查询密钥' }).props.value).toBe(
      'new-source-draft'
    );
    expect(button('保存余额来源').props.disabled).toBeFalsy();
  } finally {
    await act(async () => renderer.unmount());
  }
});
