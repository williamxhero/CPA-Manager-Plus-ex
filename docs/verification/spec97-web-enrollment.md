# Pending web enrollment in the ordinary Manager

The ordinary product accepts a durable **request for verification** at the
authenticated `/usage-service/official-balance/web-enrollment` endpoint. This is
the first enrollment slice of #97. It does not activate a browser or parser.

Configure the typed web source through the existing official-balance API with
`parsingRule: "unverified"`. Use the returned `version` and
`webSourceAuthority`, together with the complete current `scope` and `targets`,
to PUT a registration:

```json
{
  "scope": {"...": "the complete current Core scope"},
  "targets": [{"...": "every current target and version"}],
  "version": 1,
  "sourceAuthority": "the exact webSourceAuthority returned by the source API",
  "registrationVersion": 0,
  "proofReference": "operator-review-record-1"
}
```

`proofReference` must be a non-secret opaque review-record ID of 1–128 ASCII
letters, digits, `.`, `_`, `:`, or `-`, beginning with a letter or digit.
The server does not dereference it. URLs, paths, page content, scripts,
selectors and verification assertions are not accepted. A reference is a
request for review, never evidence that verification succeeded.

GET the endpoint with the same JSON `binding` query parameter as the source
API. Registration returns `pending_verification`, a monotonic
`registrationVersion`, `parserSupported: false` for the unverified rule and
`collectionAuthorized: false`. No observation or amount is created. Restarting
the Manager preserves the request in SQLite. PUT with the current versions,
`withdraw: true` and an empty reference withdraws it; its CAS revision remains
so a delayed writer cannot resurrect it.

Source replacement, account/tenant/role/operator/browser changes, or any
resource version/boot change cannot inherit the old reference. Source and
registration CAS failures return 409. The endpoint retains panel authentication,
body limits, strict JSON decoding and the source API's Core identity checks.
It does not change source versions or clear existing observations.

The default production parser registry remains empty. Neither registration nor
fixture environment variables grant `verified_browser` authority. A future
trusted verifier must establish the actual Edge Profile 1 mapping, execution
user and observed normal-page parser contract before production reads can be
enabled. No DOM contract is guessed by this slice.

Narrow checks (run offline with the project toolchain/dependency cache):

```text
go test -mod=readonly -count=1 -json -timeout=90s ./internal/httpapi -run '^TestOfficialBalanceWebEnrollment'
python -B docs/verification/spec97-main-recovery/enrollment_acceptance.py OUTPUT
```

The opt-in Windows artifact runner builds the ordinary, untagged real main
from a clean commit. It uses temporary SQLite and a loopback synthetic Core;
the browser boundary is never invoked. It verifies registration, process
restart, withdrawal, identity rejection and refusal to self-certify. It reuses
the owned-process shutdown helper from `acceptance.py`. Set `CPAMP_ENTRY_GO`
and `CPAMP_ENTRY_GOCACHE` to the offline Go toolchain/cache. An optional
`--artifact PATH` runs the same scenario against an existing executable for RED.
Every executable is checked offline with `go version -m` before launch; a tagged
or different entry point is rejected. An external executable's source commit
is reported as unknown by the runner and must be linked to its original build
manifest by the evidence recorder; current worktree HEAD is only the harness
commit. Default clean builds record their actual build commit.
These controlled checks are not real-account or real-browser acceptance.
