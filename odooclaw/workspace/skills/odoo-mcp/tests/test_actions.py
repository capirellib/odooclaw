import asyncio
from unittest.mock import MagicMock

import pytest

import odoo_mcp.server as server


def run(coro):
    return asyncio.run(coro)


@pytest.fixture
def client(monkeypatch):
    fake = MagicMock()
    fake.odoo_session.uid = 2
    monkeypatch.setattr(server, "get_odoo_client", lambda: fake)
    return fake


def test_only_generic_tools_are_listed():
    names = {t.name for t in run(server.mcp.list_tools())}
    assert names == server.ALWAYS_VISIBLE
    assert "odoo_find_pending_invoices" in server._ACTIONS
    assert len(server._ACTIONS) > 100


def test_search_in_spanish_finds_the_vendor_bill_from_pdf(client):
    client.call_kw.return_value = False
    result = server.odoo_list_actions(search="factura proveedor pdf", sender_id=7)
    names = [a["name"] for a in result["actions"]]
    assert result["matches"] > 0
    assert "odoo_create_vendor_bill_from_ocr_validated" in names


def test_describing_an_action_hides_the_trusted_arguments(client):
    result = server.odoo_list_actions(name="odoo_find_pending_invoices", sender_id=7)
    assert "sender_id" not in result["arguments"]["properties"]
    assert "partner_id" in result["arguments"]["properties"]


def test_run_action_keeps_the_real_user(client, monkeypatch):
    seen = {}

    def fake_find(c, uid, partner_id, move_type, limit):
        seen["uid"] = uid
        return [{"id": 1}]

    monkeypatch.setattr(server, "find_pending_invoices", fake_find)
    # The model tries to act as user 1: OdooClaw's sender_id (7) wins.
    out = run(server.odoo_run_action(
        name="odoo_find_pending_invoices",
        arguments={"partner_id": 5, "sender_id": 1},
        sender_id=7,
    ))
    assert seen["uid"] == 7
    assert out == [{"id": 1}]


def test_bad_arguments_answer_with_the_expected_ones(client):
    out = run(server.odoo_run_action(name="odoo_find_pending_invoices", arguments={"limit": "many"}, sender_id=7))
    assert "error" in out and "arguments" in out


def test_unknown_action_is_an_answer_not_a_crash(client):
    out = run(server.odoo_run_action(name="odoo_whatsapp_get_conversations_summary", sender_id=7))
    assert "No action named" in out["error"]


def test_view_and_report_patches_only_for_administrators(client):
    client.call_kw.return_value = False
    names = [a["name"] for a in server.odoo_list_actions(sender_id=7)["actions"]]
    assert not any(server.ADMIN_ONLY.search(n) for n in names)
    out = run(server.odoo_run_action(name="odoo_apply_view_patch_safe", arguments={}, sender_id=7))
    assert "No action named" in out["error"]

    client.call_kw.return_value = True
    names = [a["name"] for a in server.odoo_list_actions(sender_id=2)["actions"]]
    assert "odoo_apply_view_patch_safe" in names
