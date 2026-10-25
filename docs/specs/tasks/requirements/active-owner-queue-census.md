---
status: draft
system: tasks
created: 2026-10-25
owners:
  - kandev
---

# Active-owner queue census requirements

## Overview

A running task agent needs to observe pending prompt identities without reading
their bodies or changing delivery. Tasks owns this outcome because it owns the
session incarnation and prompt FIFO. Authentication and the existing trusted
execution context remain authoritative.

This is a proposed contract for [issue #4758](https://github.com/kdlbs/kandev/issues/4758).
That issue is open with no comments as observed on 2026-10-25; implementation and
upstream contribution fit are not approved by its existence. See the
[design](../system-design/active-owner-queue-census.md) and
[implementation package](../../../plans/active-owner-queue-census/plan.md).

## Terminology

- **Active owner:** The authenticated caller of a live execution bound to the
  exact task and running session being observed. A parent, sibling, workspace
  administrator, or external credential alone is insufficient.
- **Pending:** The current queue-status visibility set, including an unattempted
  durable plan-comment receipt and excluding other reserved in-flight entries.
- **Observation revision:** A digest identifying one complete snapshot's visible
  contents, identity and capacity. It grants no mutation or delivery authority.

## Requirements

### REQ-TASKS-ACTIVE-QUEUE-CENSUS-001: Bounded owner observation

**Intent:** Let an agent compare accepted pending identities without disclosure
of prompt bodies or changes to delivery.

#### Acceptance criteria

- **AC-TASKS-ACTIVE-QUEUE-CENSUS-001.1:** A live task or Office task execution
  shall discover and call `get_message_queue_census_kandev` with an empty argument
  object for its own running session. The result shall contain only schema
  version, own task/session/incarnation, observation revision, complete marker,
  pending count, configured capacity, and ordered entry descriptors. Each
  descriptor shall contain entry ID, contiguous ordinal, SHA-256 of the exact
  UTF-8 content bytes, and UTF-8 byte length. Capacity zero means unlimited
  admission, not an unlimited census.
- **AC-TASKS-ACTIVE-QUEUE-CENSUS-001.2:** Missing trusted execution or principal,
  wrong task/session pair, stale execution/incarnation, inactive session/task,
  denied owner access or unavailable authority shall fail before accessing queue
  dependencies. The denial shall reveal no queue existence, count or content.
  Caller-authored identity fields shall never replace trusted context.
- **AC-TASKS-ACTIVE-QUEUE-CENSUS-001.3:** External, configuration, managed
  conversation, automation and coordinator surfaces shall neither discover nor
  directly execute this tool. Workspace membership, parenthood and administrator
  status shall not broaden the active caller's target.
- **AC-TASKS-ACTIVE-QUEUE-CENSUS-001.4:** The census shall observe one coherent
  FIFO snapshot, fence its exact incarnation and authority, and return a
  deterministic content-addressed observation revision. A visible content edit,
  pending-set change, order change, capacity change or incarnation change shall
  produce a different revision when the resulting observations differ. Equal
  observations can have equal revisions after an intervening change; revisions
  shall not claim chronological ordering, mutation CAS, or ABA protection.
- **AC-TASKS-ACTIVE-QUEUE-CENSUS-001.5:** Success shall be complete or an explicit
  error. It shall not return a prefix, partial count, truncated identifier or a
  fabricated empty result. The operation shall reject more than 128 visible
  entries, more than 1024 stored entries examined, more than 8 MiB of aggregate
  content and visibility metadata examined, metadata exceeding 32 KiB for one
  examined row, an invalid UTF-8/over-256-byte identifier, or a final encoded MCP
  result exceeding 65536 bytes. Cancellation or unavailable storage shall error.
- **AC-TASKS-ACTIVE-QUEUE-CENSUS-001.6:** Success, denial, failure and repetition
  shall leave queue rows, incarnations, counters, policies, receipts,
  reservations, attachments, runtime and workflow state unchanged. They shall
  emit no delivery, readiness or wake event. Message bodies, attachment data,
  sender metadata, timestamps, reservation data and claim tokens shall appear
  in neither tool output nor tool diagnostics.
- **AC-TASKS-ACTIVE-QUEUE-CENSUS-001.7:** Memory, SQLite and PostgreSQL providers
  shall satisfy the same visibility, order, byte/hash, authorization and bounded
  failure contract. A successful empty queue shall return `entries: []` and count
  zero. Missing or legacy authority evidence shall fail closed.
- **AC-TASKS-ACTIVE-QUEUE-CENSUS-001.8:** Documentation shall identify the result
  as an observation at a snapshot boundary, which can become stale before the
  caller receives it. Absence shall not prove delivery or cancellation, and an
  enqueue acknowledgement shall not prove an entry remains pending.

## Out of scope

Entry removal, claims, acknowledgement, coalescing, FIFO changes, parent/child or
terminal recovery readers, new trust authorities, queue mutation revisions,
runtime installation, rollout flags, UI controls and live queue experiments are
excluded. No old source stack, fixture or runtime verdict is adopted.
