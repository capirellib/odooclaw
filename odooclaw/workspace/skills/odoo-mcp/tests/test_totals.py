from unittest.mock import MagicMock

import pytest

from odoo_mcp.core.client import OdooClient
from odoo_mcp.tools.records import odoo_get_totals

SEPTEMBER = [
    ["move_type", "in", ["out_invoice", "out_refund"]],
    ["state", "=", "posted"],
    ["invoice_date", ">=", "2026-09-01"],
    ["invoice_date", "<=", "2026-09-30"],
]


@pytest.fixture
def client():
    return MagicMock(spec=OdooClient)


def test_grand_total_is_computed_by_odoo(client):
    client.call_kw.return_value = [
        {"__count": 56, "amount_untaxed_signed:sum": 22170301.4, "amount_total_signed:sum": 25808197.123}
    ]
    result = odoo_get_totals(client, 2, "account.move", SEPTEMBER, ["amount_untaxed_signed", "amount_total_signed"])
    assert result["count"] == 56
    assert result["totals"] == {"amount_untaxed_signed": 22170301.4, "amount_total_signed": 25808197.12}
    method = client.call_kw.call_args.args[1]
    assert method == "formatted_read_group"


def test_grouped_by_month_keeps_the_label(client):
    client.call_kw.return_value = [
        {"invoice_date:month": ["2026-08-01", "agosto 2026"], "__count": 206, "amount_total_signed:sum": 113306780.0},
        {"invoice_date:month": ["2026-09-01", "septiembre 2026"], "__count": 56, "amount_total_signed:sum": 25808197.0},
    ]
    result = odoo_get_totals(client, 2, "account.move", [], ["amount_total_signed"], ["invoice_date:month"])
    assert [g["invoice_date:month"] for g in result["groups"]] == ["agosto 2026", "septiembre 2026"]
    assert result["totals"]["amount_total_signed"] == 139114977.0
    assert result["count"] == 262


def test_falls_back_to_read_group(client):
    client.call_kw.side_effect = [
        Exception("formatted_read_group does not exist"),
        [{"__count": 3, "amount_total": 30.0}],
    ]
    result = odoo_get_totals(client, 2, "sale.order", [], ["amount_total"])
    assert result["totals"] == {"amount_total": 30.0}
    assert client.call_kw.call_args.args[1] == "read_group"


@pytest.mark.parametrize("measures, groupby", [([], None), (["amount; drop"], None), (["amount_total"], ["date:century"])])
def test_rejects_bad_input(client, measures, groupby):
    with pytest.raises(ValueError):
        odoo_get_totals(client, 2, "account.move", [], measures, groupby)
