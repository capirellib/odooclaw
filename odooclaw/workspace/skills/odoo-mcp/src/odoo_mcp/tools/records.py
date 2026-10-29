import re
from typing import Any, Dict, List, Optional
from odoo_mcp.core.client import OdooClient
from odoo_mcp.security.guards import guard_model_access, guard_write_fields
from odoo_mcp.security.audit import audit_action
from odoo_mcp.core.domains import validate_domain
from odoo_mcp.core.serializers import serialize_records
from odoo_mcp.services.partner_service import find_existing_partner_id


def odoo_search(
    client: OdooClient, user_id: int, model: str, domain: List[Any], limit: int
) -> List[int]:
    """Search for record IDs matching domain."""
    validate_domain(domain)
    return client.call_kw(
        model, "search", args=[domain], kwargs={"limit": limit}, sender_id=user_id
    )


def odoo_read(
    client: OdooClient,
    user_id: int,
    model: str,
    ids: List[int],
    fields: Optional[List[str]] = None,
) -> List[Dict[str, Any]]:
    """Read fields for a list of record IDs."""
    kwargs = {"fields": fields} if fields else {}
    records = client.call_kw(
        model, "read", args=[ids], kwargs=kwargs, sender_id=user_id
    )
    return serialize_records(records)


def odoo_search_read(
    client: OdooClient,
    user_id: int,
    model: str,
    domain: List[Any],
    fields: Optional[List[str]] = None,
    limit: int = 80,
) -> List[Dict[str, Any]]:
    """Search and read in a single call."""
    validate_domain(domain)
    kwargs = {"limit": limit}
    if fields:
        kwargs["fields"] = fields
    records = client.call_kw(
        model, "search_read", args=[domain], kwargs=kwargs, sender_id=user_id
    )
    return serialize_records(records)


def odoo_create(
    client: OdooClient, user_id: int, model: str, values: Dict[str, Any]
) -> int:
    """Create a new record after checking allowlist."""
    guard_model_access(model)
    audit_action("CREATE", user_id, model, [], values)

    if model == "res.partner":
        existing_partner_id = find_existing_partner_id(
            client,
            user_id,
            name=values.get("name"),
            vat=values.get("vat"),
            email=values.get("email"),
            allow_fuzzy_name=False,
        )
        if existing_partner_id:
            return existing_partner_id

    return client.call_kw(model, "create", args=[values], sender_id=user_id)


def odoo_write(
    client: OdooClient, user_id: int, model: str, ids: List[int], values: Dict[str, Any]
) -> bool:
    """Update records, respecting denylists and allowlists."""
    guard_model_access(model)
    guard_write_fields(values)
    audit_action("WRITE", user_id, model, ids, values)
    return client.call_kw(model, "write", args=[ids, values], sender_id=user_id)


_FIELD_NAME = re.compile(r"^[a-z_][a-z0-9_]*$")
_GROUPBY = re.compile(r"^[a-z_][a-z0-9_]*(:(day|week|month|quarter|year))?$")


def odoo_get_totals(
    client: OdooClient,
    user_id: int,
    model: str,
    domain: List[Any],
    measures: List[str],
    groupby: Optional[List[str]] = None,
    limit: int = 100,
) -> Dict[str, Any]:
    """Sum numeric fields over every matching record, optionally grouped.

    Odoo does the arithmetic (like a grouped list view), so a total never
    depends on how many records a search returned or on the model adding
    them up. ``groupby`` takes fields, dates with a period
    (``invoice_date:month``) or nothing for one grand total.
    """
    validate_domain(domain)
    groupby = list(groupby or [])
    if not measures:
        raise ValueError("measures needs at least one numeric field to sum.")
    for name in measures:
        if not _FIELD_NAME.match(name):
            raise ValueError(f"Invalid measure: {name!r}")
    for name in groupby:
        if not _GROUPBY.match(name):
            raise ValueError(f"Invalid groupby: {name!r}")

    aggregates = [f"{name}:sum" for name in measures]
    try:
        # Odoo 19: formatted_read_group replaces the deprecated read_group.
        rows = client.call_kw(
            model,
            "formatted_read_group",
            args=[domain],
            kwargs={"groupby": groupby, "aggregates": ["__count", *aggregates], "limit": limit},
            sender_id=user_id,
        )
    except Exception:
        rows = client.call_kw(
            model,
            "read_group",
            args=[domain],
            kwargs={"fields": aggregates, "groupby": groupby, "lazy": False, "limit": limit},
            sender_id=user_id,
        )

    groups = []
    for row in rows:
        group = {"count": row.get("__count", 0)}
        for name in groupby:
            value = row.get(name)
            # Many2one and dated groups come back as [id, label]: keep the label.
            if isinstance(value, (list, tuple)) and len(value) == 2:
                value = value[1]
            group[name] = value
        for name in measures:
            value = row.get(f"{name}:sum", row.get(name))
            group[name] = round(value or 0.0, 2)
        groups.append(group)

    totals = {name: round(sum(g[name] for g in groups), 2) for name in measures}
    return {
        "model": model,
        "domain": domain,
        "count": sum(g["count"] for g in groups),
        "totals": totals,
        "groups": groups if groupby else [],
    }
