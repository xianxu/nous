---
id: '000043'
status: done
created: 2026-06-06
updated: 2026-06-06
estimate_hours: 0.5
actual_hours: 0.01
---

# conformance: move gh fixture repo to the test account (ephemeral, two throwaway accounts)

## Problem

nous#42's gh conformance run used the operator's own `gh auth` account (xianxu) as
operator and a throwaway (yingtest42) as invitee, with a **standing** fixture repo
on xianxu (`xianxu/shim-conformance`). Two issues: (1) it puts a test repo on the
real account, and (2) the operator token lacked `delete_repo`, so the fixture
couldn't be ephemeral. Operator wants the **real account fully off the test path**.
