---
name: odoo-whatsapp
description: Query WhatsApp activity in Odoo (Betta Evolution API modules) - who sent or received messages, how many per user, per contact, per month, groups, failed sends and text or media. Use it for any question about WhatsApp usage, messages or conversations.
---

# WhatsApp in Odoo (Betta Evolution API)

Every WhatsApp message, sent or received, is one record of the model
`betta.whatsapp.queue`. Read it with the generic tools: `odoo_get_totals` for
counts, `odoo_search` + `odoo_read` for the messages themselves. Never say you
have no access to WhatsApp before trying these.

If `betta.whatsapp.queue` does not exist in this Odoo, the Betta WhatsApp
modules are not installed: check `whatsapp.message` (Odoo Enterprise WhatsApp)
with `odoo_get_model_schema` before answering that there is no data.

## Fields

| Field | Meaning |
|---|---|
| `create_date` | When the message was created (sent or received). Use it for periods. |
| `user_id` | Odoo user responsible for the message. On outbound messages, who sent it. |
| `direction` | `outbound` (sent from Odoo) or `inbound` (received) |
| `partner_id` | Contact of the conversation. `partner_id.is_whatsapp_group = True` for groups. |
| `number` | Phone number or group id |
| `message_type` | `text` or `media` |
| `media_type` | `image`, `document`, `video` or `audio` (media only) |
| `state` | `draft`, `pending`, `sent` or `error` |
| `error_message` | Why a send failed (state `error`) |
| `whatsapp_instance_id` | WhatsApp line (instance) used |
| `message` | Text of the message. Read it only when the user asks for content. |

## Ready-made queries

Messages sent per user and month, July to September 2026:

```
odoo_get_totals(
  model="betta.whatsapp.queue",
  domain=[["direction","=","outbound"],
          ["create_date",">=","2026-07-01 00:00:00"],
          ["create_date","<","2026-10-01 00:00:00"]],
  groupby=["user_id","create_date:month"])
```

No `measures`: each group's `count` is the number of messages.

- **Most active users:** the same query with `groupby=["user_id"]`, sorted by `count`.
- **Text vs media:** `groupby=["message_type"]`, or `["media_type"]` for media only.
- **Groups only:** add `["partner_id.is_whatsapp_group","=",true]` to the domain.
- **Individual chats only:** add `["partner_id.is_whatsapp_group","=",false]`.
- **Received messages:** `["direction","=","inbound"]`, grouped by `partner_id`.
- **Failed sends:** `["state","=","error"]`; read `error_message` of a few with `odoo_search` + `odoo_read`.
- **Per line:** `groupby=["whatsapp_instance_id"]`.

## How to answer

- Give the period and the filter you used ("mensajes enviados del 01/07 al 30/09").
- A table per user with the counts, and the total.
- Inbound messages may have no `user_id` (nobody answered yet): show them as "sin asignar".
- If a query returns an access error, the user asking has no rights to see
  WhatsApp messages in Odoo: say so plainly, do not retry with other tools.
