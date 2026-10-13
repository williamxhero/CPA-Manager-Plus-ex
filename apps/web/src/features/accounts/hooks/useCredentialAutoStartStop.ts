import { useCallback, useEffect, useRef, useState } from 'react';
import {
  usageServiceApi,
  type AccountProcessingPolicy,
  type AccountProcessingPolicyPatch,
} from '@/services/api/usageService';

/**
 * SPEC13: the credential-area “自动启停” (auto start/stop) switch is the
 * provider-quota auto start/stop policy. It reuses the server-persisted
 * `codexQuotaCooldownEnabled` capability exposed by the Manager
 * account-processing-policy API, so this toolbar switch and the config page
 * always read and write the same persisted value.
 */
export const buildAutoStartStopPatch = (value: boolean): AccountProcessingPolicyPatch => ({
  codexQuotaCooldownEnabled: value,
});

export type CredentialAutoStartStopStatus = 'idle' | 'loading' | 'ready' | 'error';

export interface CredentialAutoStartStopState {
  status: CredentialAutoStartStopStatus;
  /** Last value known to be persisted on the server (never optimistic). */
  enabled: boolean;
  locked: boolean;
  saving: boolean;
  loadError: string;
  saveError: string;
  updatedAtMs?: number;
}

export interface CredentialAutoStartStopController extends CredentialAutoStartStopState {
  setEnabled: (value: boolean) => Promise<void>;
}

const initialState: CredentialAutoStartStopState = {
  status: 'idle',
  enabled: false,
  locked: false,
  saving: false,
  loadError: '',
  saveError: '',
};

const readCapability = (
  policy: AccountProcessingPolicy
): { enabled: boolean; locked: boolean; updatedAtMs?: number } => {
  const capability = policy.codexQuotaCooldown;
  return {
    enabled: Boolean(capability?.configured ?? capability?.enabled),
    locked: Boolean(capability?.locked),
    updatedAtMs: policy.updatedAtMs,
  };
};

const readErrorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message;
  if (typeof error === 'string') return error;
  return '';
};

/**
 * Reads and writes the persisted auto start/stop policy for the credential
 * toolbar. State is only reported as ON/OFF once it is confirmed persisted;
 * a failed save keeps the previous persisted value and surfaces an error.
 */
export function useCredentialAutoStartStop(
  managerServiceBase: string,
  managementKey: string
): CredentialAutoStartStopController {
  const [state, setState] = useState<CredentialAutoStartStopState>(initialState);
  const connectionKeyRef = useRef('');

  const available = Boolean(managerServiceBase && managementKey);

  useEffect(() => {
    const connectionKey = `${managerServiceBase}\u001f${managementKey}`;
    connectionKeyRef.current = connectionKey;
    const isCurrent = () => connectionKeyRef.current === connectionKey;
    if (!available) {
      setState(initialState);
      return;
    }
    setState((previous) => ({
      ...previous,
      status: 'loading',
      saving: false,
      loadError: '',
      saveError: '',
    }));
    usageServiceApi
      .getAccountProcessingPolicy(managerServiceBase, managementKey)
      .then((policy) => {
        if (!isCurrent()) return;
        setState({
          status: 'ready',
          ...readCapability(policy),
          saving: false,
          loadError: '',
          saveError: '',
        });
      })
      .catch((error: unknown) => {
        if (!isCurrent()) return;
        setState((previous) => ({
          ...previous,
          status: 'error',
          enabled: false,
          saving: false,
          loadError: readErrorMessage(error),
        }));
      });
  }, [available, managementKey, managerServiceBase]);

  const setEnabled = useCallback(
    async (value: boolean) => {
      if (!available) return;
      const connectionKey = `${managerServiceBase}\u001f${managementKey}`;
      const isCurrent = () => connectionKeyRef.current === connectionKey;
      setState((previous) =>
        previous.status === 'ready' ? { ...previous, saving: true, saveError: '' } : previous
      );
      try {
        const policy = await usageServiceApi.updateAccountProcessingPolicy(
          managerServiceBase,
          managementKey,
          buildAutoStartStopPatch(value)
        );
        if (!isCurrent()) return;
        setState((previous) => ({
          ...previous,
          status: 'ready',
          ...readCapability(policy),
          saving: false,
          saveError: '',
        }));
      } catch (error: unknown) {
        if (!isCurrent()) return;
        // Keep the last confirmed persisted value; never show an unsaved state as ON/OFF.
        setState((previous) => ({
          ...previous,
          saving: false,
          saveError: readErrorMessage(error),
        }));
      }
    },
    [available, managementKey, managerServiceBase]
  );

  return { ...state, setEnabled };
}
