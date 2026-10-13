---
name: Branch names carry the ticket
description: Name branches TRK-123-short-words so the ticket shows in every list.
metadata:
  type: project
---
Branches in the upload service are named after the ticket, for example `TRK-1234-upload-retry`.

**Why:** the release script groups commits by the ticket in the branch name.
