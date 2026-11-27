import { officialBalanceRequest, type BalanceBinding } from './officialBalance';

const paths = {
  deepseek: '/user/balance',
  deepinfra: '/payment/checklist',
  'openai-compat-usage-v1': '/v1/usage',
} as const;
type MatchAdapter = keyof typeof paths;
const isMatchAdapter = (value: unknown): value is MatchAdapter =>
  typeof value === 'string' && Object.prototype.hasOwnProperty.call(paths, value);

const matchedFields = new Set([
  'is_available',
  'balance_infos[].currency',
  'balance_infos[].total_balance',
  'balance_infos[].granted_balance',
  'balance_infos[].topped_up_balance',
  'stripe_balance',
  'recent',
  'balance',
  'data.balance',
]);
const reasons = new Set([
  'match_busy',
  'access_unavailable',
  'invalid_base_url',
  'credential_unavailable',
  'no_usable_candidate',
  'probe_budget_exhausted',
  'low_confidence',
  'tied_candidates',
  'unsupported_adapter',
  'invalid_endpoint',
  'upstream_unavailable',
  'upstream_status',
  'unreadable_response',
  'contract_mismatch',
  'non_json_content_type',
]);
const safeReason = (value: unknown) =>
  typeof value === 'string' && reasons.has(value) ? value : undefined;

export interface BalanceMatchEvidence {
  source: 'probe' | 'fingerprint';
  adapter: MatchAdapter;
  host: string;
  url: string;
  status: number;
  fields: string[];
  reason?: string;
}
export type BalanceMatch =
  | { status: 'unmatched'; reason?: string; candidates: BalanceMatchEvidence[] }
  | {
      status: 'matched';
      confidence: number;
      evidence: BalanceMatchEvidence;
      applied: boolean;
      applyReason?: 'manual_configuration' | 'credential_unavailable';
    };

const record = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' ? (value as Record<string, unknown>) : {};

function readEvidence(value: unknown): BalanceMatchEvidence | undefined {
  const { source, adapter, url, status, matchedFields: fields, reason } = record(value);
  if (
    (source !== 'probe' && source !== 'fingerprint') ||
    !isMatchAdapter(adapter) ||
    typeof url !== 'string' ||
    typeof status !== 'number' ||
    !Number.isInteger(status) ||
    (status !== 0 && (status < 100 || status > 599))
  )
    return;
  try {
    // Manager supplies a credential-free origin and a fixed adapter path.
    const parsed = new URL(url);
    if (parsed.protocol !== 'https:' || url !== parsed.origin + paths[adapter]) return;
    return {
      source,
      adapter,
      url,
      host: parsed.hostname,
      status,
      fields: Array.isArray(fields)
        ? fields.filter(
            (field): field is string => typeof field === 'string' && matchedFields.has(field)
          )
        : [],
      reason: safeReason(reason),
    };
  } catch {
    return undefined;
  }
}

export async function matchOfficialBalance(
  base: string,
  managementKey: string,
  binding: BalanceBinding,
  signal: AbortSignal,
  apply = false
): Promise<BalanceMatch> {
  // The matcher resolves credentials and the upstream URL server-side. No raw
  // provider config, credential, or endpoint is sent by this client.
  const result = record(
    await officialBalanceRequest<unknown>(
      base,
      managementKey,
      'POST',
      '/match',
      {
        binding: { scope: binding.scope, targets: binding.targets },
        ...(apply ? { apply: true } : {}),
      },
      signal
    )
  );
  // Project only the public evidence contract; never retain extra response fields.
  if (result.status === 'unmatched') {
    const candidates = Array.isArray(result.candidates) ? result.candidates : [];
    return {
      status: 'unmatched',
      reason: safeReason(result.reason),
      candidates: candidates.flatMap((candidate) => {
        const evidence = readEvidence(record(candidate).evidence);
        return evidence ? [evidence] : [];
      }),
    };
  }
  const evidence = readEvidence(result.evidence);
  const confidence = result.confidence;
  if (
    result.status !== 'matched' ||
    !evidence ||
    result.adapter !== evidence.adapter ||
    result.endpoint !== evidence.url ||
    typeof confidence !== 'number' ||
    !Number.isInteger(confidence) ||
    confidence < 0 ||
    confidence > 100
  ) {
    throw new Error('invalid_balance_match');
  }
  return {
    status: 'matched',
    confidence,
    evidence,
    applied: result.applied === true,
    applyReason:
      result.applyReason === 'manual_configuration' ||
      result.applyReason === 'credential_unavailable'
        ? result.applyReason
        : undefined,
  };
}
