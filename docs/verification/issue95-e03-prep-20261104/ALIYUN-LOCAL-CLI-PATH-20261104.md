# Aliyun local CLI path (2026-11-04)

Operator decision: keep Aliyun quota as **local CLI** (`bailian-quota --source bsk`), not OfficialBalance billing AK/SK.

## Runtime

- CLI: `D:\WILL\AGENT\CPA\runtime\CPA-Core\tools\bailian-quota.exe`
- bsk: `C:\Users\will\.local\bin\bsk.exe` daemon; Edge instance `5cfdda27` connected after launch
- First live attempt without browser → `no_browser`
- With browser, console not logged in → `browser_failed` / Bailian evaluation failed; snapshot shows **登录**

## Next operator step

In the BrowserSkill-enabled Edge profile, sign in to `https://bailian.console.aliyun.com/`, then:

```text
bailian-quota.exe --json --source bsk --timeout 90s
```

Do not put Aliyun AK/SK into OfficialBalance for this gate.
