import type { CredentialQuotaSource, QuotaBinding } from './credentialQuota';

const safeErrors = new Set([
  'gate_source_unsupported',
  'gate_eligibility_invalid',
  'identity_changed',
  'invalid_policy',
  'policy_generation_conflict',
  'credential_policy_unavailable',
  'environment_locked_off',
  'gate_contract_unavailable',
  'gate_contract_invalid',
  'gate_application_unverified',
  'gate_connection_unavailable',
  'gate_connection_changed',
  'gate_core_unavailable',
  'gate_auth_error',
  'gate_not_ready',
  'gate_mode_unsupported',
]);

export class CredentialPolicyError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string
  ) {
    super(code);
    this.name = 'CredentialPolicyError';
  }
}

async function policyError(response: Response): Promise<CredentialPolicyError> {
  let code = 'credential_policy_request_failed';
  try {
    const body: unknown = await response.json();
    if (
      body &&
      typeof body === 'object' &&
      'error' in body &&
      typeof body.error === 'string' &&
      safeErrors.has(body.error)
    )
      code = body.error;
  } catch {
    /* Unknown/HTML/auth proxy errors are deliberately not echoed. */
  }
  return new CredentialPolicyError(response.status, code);
}

// Durable intent and actual protection are distinct. Never render enabled as an
// applied pause; unavailable and failed writes must use backend readback.
export interface CredentialPolicyStatus extends Omit<CredentialQuotaSource, 'state' | 'reason'> {
  subjectId: string;
  bindingCurrent: boolean;
  enabled: boolean;
  generation: number;
  owner: string;
  state: string;
  reason: string;
  availability: { state: 'available' | 'unavailable'; reason: string };
  queryAvailability: { state: string; reason: string };
  applied: boolean;
}

export interface CredentialPolicyUpdate extends QuotaBinding {
  owner?: string;
  auth_index: string;
  sourceVersion: string;
  expectedGeneration: number;
  enabled: boolean;
}

export async function getStoredCredentialPolicy(
  base: string,
  managementKey: string,
  subjectId: string,
  signal?: AbortSignal
): Promise<CredentialPolicyStatus> {
  const response = await fetch(
    `${base.replace(/\/$/, '')}/usage-service/credential-policy?subjectId=${encodeURIComponent(subjectId)}`,
    {
      method: 'GET',
      headers: { Authorization: `Bearer ${managementKey}`, 'Content-Type': 'application/json' },
      signal,
      cache: 'no-store',
      redirect: 'error',
    }
  );
  if (!response.ok) throw await policyError(response);
  return response.json() as Promise<CredentialPolicyStatus>;
}

export async function saveCredentialPolicy(
  base: string,
  managementKey: string,
  update: CredentialPolicyUpdate,
  signal?: AbortSignal
): Promise<CredentialPolicyStatus> {
  const response = await fetch(`${base.replace(/\/$/, '')}/usage-service/credential-policy`, {
    method: 'PUT',
    headers: { Authorization: `Bearer ${managementKey}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(update),
    signal,
    cache: 'no-store',
    redirect: 'error',
  });
  if (!response.ok) throw await policyError(response);
  return response.json() as Promise<CredentialPolicyStatus>;
}

export async function getCredentialPolicy(
  base: string,
  managementKey: string,
  authIndex: string,
  signal?: AbortSignal
): Promise<CredentialPolicyStatus> {
  const response = await fetch(
    `${base.replace(/\/$/, '')}/usage-service/credential-policy?authIndex=${encodeURIComponent(authIndex)}`,
    {
      method: 'GET',
      headers: { Authorization: `Bearer ${managementKey}`, 'Content-Type': 'application/json' },
      body: undefined,
      signal,
      cache: 'no-store',
      redirect: 'error',
    }
  );
  if (!response.ok) throw await policyError(response);
  return response.json() as Promise<CredentialPolicyStatus>;
}
