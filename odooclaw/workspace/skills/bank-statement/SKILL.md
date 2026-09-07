---
name: bank-statement
description: Importa extractos bancarios o de tarjeta en Odoo 19 desde adjuntos CSV, XLSX, OFX, QFX, PDF o TXT, con vista previa, deduplicación y conciliación asistida. Usar cuando el usuario pida cargar movimientos, importar un extracto o preparar una conciliación bancaria.
---

# Bank Statement

Use this skill to preview and import bank statement transactions into Odoo 19.

## Required workflow

1. Identify the Odoo `ir.attachment` and the bank or cash `journal_id`.
2. Call `odoo_import_bank_statement` with `dry_run=true` and `confirm=false`.
3. Show parsed totals, invalid rows, duplicate status, opening/closing balance checks, and a sample of transactions.
4. Resolve every validation error before continuing.
5. Ask for explicit confirmation.
6. Call the tool again with `dry_run=false` and `confirm=true`.
7. Present the created statement and reconciliation suggestions. Never reconcile automatically.

Prefer `attachment_id` for files uploaded through Odoo. Use `file_content_b64` only when the content is already available to the caller and always provide `filename`.

## Safety rules

- Never pass or invent arbitrary server file paths.
- Never import when the preview reports invalid transactions, inconsistent balances, mixed duplicates, or an inaccessible journal.
- Never create records with only `dry_run=false`; `confirm=true` is also required.
- Keep import and reconciliation as separate confirmed actions.
- Respect the requesting user, allowed companies, Odoo ACLs, and record rules.
- Do not infer that a sales order was paid. Payment state comes from invoices and reconciled accounting entries.

## Tool

`odoo_import_bank_statement` accepts:

- `journal_id`: required bank or cash journal.
- `attachment_id`: preferred Odoo attachment source.
- `file_content_b64` and `filename`: alternative source.
- `company_id` and `allowed_company_ids`: optional company scope.
- `dry_run`: defaults to true.
- `confirm`: must be true for creation.
- `suggest_reconciliation`: return read-only candidates after creation.

Read [references/input-formats.md](references/input-formats.md) when the preview contains parser warnings or the user asks about supported formats.

