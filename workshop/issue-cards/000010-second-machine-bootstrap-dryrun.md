---
id: '000010'
status: done
created: 2026-05-06
updated: 2026-05-07
actual_hours: 0.5
---

# second-machine bootstrap dry-run for the encrypted brain

## Problem

`nous#3` M3 step 3a was scoped out of the cutover — the destructive rename-and-cutover landed on a single machine without first verifying the bootstrap procedure works on a fresh second machine. This was an **accepted risk** taken because the cloud + local backup channels (`xianxu/brain-backup` and `~/workspace/brain.legacy*`) were still in place and the personal-MVP rollback story was intact. 

That risk needs to be repaid before the safety nets get cleaned up:
- Local cleanup: `~/workspace/brain.legacy*` removable after 1 week (~2026-05-13).
- Cloud cleanup: `xianxu/brain-backup` removable after 1 month (~2026-06-06).
