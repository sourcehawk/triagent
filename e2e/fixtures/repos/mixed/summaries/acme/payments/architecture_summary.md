---
generated_at: 2026-05-01T12:00:00Z
kind: freeform
byte_count: 184
---
# acme/payments — architecture summary

TRIAGENT-E2E-FLOW5-PAYMENTS-SUMMARY-MARKER

The payments service owns the billing-path state machine. Charges flow
through the `charge` package; reconciliation runs in `recon/`.
