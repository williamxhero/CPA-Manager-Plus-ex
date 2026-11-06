// Package bss provides signed, bounded Alibaba billing RPC reads. Its module
// import path is cpa-credential-qwen/internal/bss. The implementation and baseline
// tests were migrated from CPA-Aliyun-Quota/internal/bss; no plugin entrypoint,
// SDK dependency, configuration, quota projection or automatic query is added.
//
// Balance fails when no amount is present. Bill and Packages are independent,
// opt-in operations: callers own required-balance/optional-enrichment policy and
// must not turn optional query errors, missing data or nil amounts into zero.
// Real zero values are retained. Incomplete pages are rejected, not summed as
// full totals. Existing float64 result types are retained for API compatibility;
// this package does not add an exact-decimal/policy-authority contract.
//
// Production endpoints require HTTPS, trusted TLS and public destination IPs.
// Admission conservatively excludes special-purpose IPv4/IPv6 (including
// protocol/anycast assignments); ordinary IPv6 must be within 2000::/3. This
// static policy is not a live routing guarantee or an online registry lookup.
// Every connection resolves and validates the entire DNS answer, then dials a
// checked numeric IP. Environment proxies and redirects are not used. AllowHTTP
// authorizes only HTTP loopback fixtures, never private HTTPS/network targets.
// Errors redact the configured synthetic-or-real secrets and signed URL text;
// the package never loads credentials from environment variables or files.
package bss
