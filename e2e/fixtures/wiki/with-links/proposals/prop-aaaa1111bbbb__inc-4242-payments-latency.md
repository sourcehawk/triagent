---
schema_version: 1
id: inc-4242-payments-latency
date: "2026-05-20"
title: Payments p99 latency breach during checkout
status: resolved
severity: sev2
services:
  - payments-api
  - ledger
errors:
  - slo-breach
symptoms:
  - high-latency
links:
  investigation: https://example.test/investigations/inv-4242
  incident_io: https://app.incident.io/acme/incidents/4242
---

## Summary

Payments API p99 latency breached the checkout SLO for ~40 minutes
during the evening peak. Customer-facing checkout timeouts followed.

## Root cause

A misconfigured connection-pool ceiling on the ledger client starved
the payments hot path under peak concurrency. PROPOSED-ROOT-CAUSE-EDIT:
the ceiling was inherited from a stale per-pod default that never scaled
with the replica count.

## Fix

Raised the ledger client pool ceiling and added a saturation alert so
the next approach to the ceiling pages before it breaches the SLO.
