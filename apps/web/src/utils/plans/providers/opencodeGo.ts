import { normalizeRawPlanType } from '../normalize';
import type { PlanResolverDescriptor } from './types';

const goDescriptor: PlanResolverDescriptor = {
  canonicalPlanType: 'go',
  shortLabelKey: 'plans.codex.go',
  shortDefault: 'Go',
};

export const OPENCODE_GO_PLAN_DESCRIPTORS: Readonly<Record<string, PlanResolverDescriptor>> = {
  'opencode go': goDescriptor,
  'opencode-go': goDescriptor,
  go: goDescriptor,
};

export const resolveOpenCodeGoPlanDescriptor = (
  planType: unknown
): PlanResolverDescriptor | null => {
  const normalized = normalizeRawPlanType(planType);
  return normalized ? (OPENCODE_GO_PLAN_DESCRIPTORS[normalized] ?? null) : null;
};
