# Supported bank statement formats

## Preferred order

1. OFX or QFX: structured transaction IDs make deduplication more reliable.
2. CSV: reliable when headers and debit/credit semantics are known.
3. XLSX: supported across multiple sheets.
4. PDF with embedded text.
5. Scanned PDF: requires OCR and must be reviewed carefully.

## CSV and XLSX

Recognized concepts include date/fecha, description/concepto/detalle, amount/importe/monto, debit/débito/cargo, credit/crédito/abono, balance/saldo, reference/referencia and partner/beneficiario.

Argentine and European numbers such as `1.234,56` and US numbers such as `1,234.56` are supported. Check the preview to confirm whether debit and credit columns have the correct sign for that bank.

## OFX and QFX

SGML 1.x and XML 2.x transaction blocks are accepted. `FITID` is preserved as the preferred transaction identifier. Compact timestamps such as `20260905123000[-3:ART]` are normalized to `2026-09-05`.

## PDF and TXT

PDF extraction is heuristic. Text-based PDFs are read first. If no usable text is found and OCR dependencies are installed, OCR is attempted. A scanned or unusual bank layout may require CSV or OFX instead.

Never bypass the preview for PDF-derived transactions.

