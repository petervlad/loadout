# loadout

The words loadout uses for teams sharing agents, skills, knowledge and memory. `docs/DESIGN.md` is the source of truth for packages, layers and roles. This glossary must agree with it.

## Team memory

**Team memory**:
The set of memories a namespace keeps for its members' agents, switched on per namespace. A team reads its own and its project's team memory, and writes only its own.
_Avoid_: Shared memory, team brain, knowledge store

**Memory**:
One short typed fact in a team memory (feedback, project or reference), with an author, dates and links to other memories. It is not a document: anything long belongs in the team's docs, and the memory links to them.
_Avoid_: Note, entry, record, doc

**Personal memory**:
The memory a person's own AI tool keeps on their machine. It never leaves that machine and is never stored by loadout.
_Avoid_: Local memory, L2 memory

**Memory index**:
The list of a team memory's active memories, one line each and capped in length, that a client receives when it connects.
_Avoid_: MEMORY.md, table of contents

**Memory token**:
A token that only the team memory endpoints accept, made by a person for their AI tools. It carries that person's identity and none of their other rights.
_Avoid_: MCP key, API key

**Memory bridge**:
The local command a person's AI tool launches to reach a team memory, forwarding every call with the person's memory token.
_Avoid_: Proxy, shim, stdio server

**Confirmed**:
The last date someone checked that a memory is still true. A memory unconfirmed for six months is marked, never hidden.
_Avoid_: Verified, reviewed, fresh

**Retract**:
To hide a memory from search and the index while keeping its history.
_Avoid_: Delete, archive

**Purge**:
To remove a memory and all its history for good, done only by a registry admin on the host, for content that should never have been stored.
_Avoid_: Hard delete, wipe

**Move to project**:
To make a team's memory part of the project's team memory, keeping its author, history and id.
_Avoid_: Promote, publish

**Memory switch**:
The setting that turns team memory on for one namespace. Maintainers and owners change it. While it is off, the namespace's memory pages and server answer as if they did not exist.
_Avoid_: Memory toggle, memory flag

**Duplicate check**:
What `save` does before it stores a memory. If close matches exist in the memory the team reads, it returns them and saves nothing, unless the agent says the memory is new.
_Avoid_: Dedupe, similarity check

**Memory import**:
Offering the memory files on a person's own machine to a team memory. The person confirms every memory, files about a person are never offered, and anything that looks like a secret stays on the machine.
_Avoid_: Memory sync, upload
