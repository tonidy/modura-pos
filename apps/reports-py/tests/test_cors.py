"""CORS (plan M7): the UI calls this service directly, so its origin is allowed and
nothing else is. No credentials mode: tokens travel in a header, not cookies."""

from __future__ import annotations

import pytest

from .conftest import UI_ORIGIN, VIEWER

pytestmark = pytest.mark.anyio

PREFLIGHT = {
    "Access-Control-Request-Method": "POST",
    "Access-Control-Request-Headers": "authorization,content-type",
}


async def test_preflight_from_the_ui_origin_is_granted(client):
    response = await client.options("/v1/reports.dailySummary", headers={"Origin": UI_ORIGIN, **PREFLIGHT})

    assert response.status_code == 200
    assert response.headers["access-control-allow-origin"] == UI_ORIGIN
    assert "POST" in response.headers["access-control-allow-methods"]
    assert {"authorization", "content-type"} <= {
        h.strip().lower() for h in response.headers["access-control-allow-headers"].split(",")
    }
    assert response.headers["access-control-max-age"] == "600"
    assert "access-control-allow-credentials" not in response.headers


async def test_other_origins_get_no_grant(client):
    preflight = await client.options("/v1/reports.dailySummary", headers={"Origin": "https://evil.example", **PREFLIGHT})
    request = await client.post(
        "/v1/reports.dailySummary", json={}, headers={"Origin": "https://evil.example", "Authorization": f"Bearer {VIEWER}"}
    )

    assert preflight.status_code == 400
    assert "access-control-allow-origin" not in preflight.headers
    assert "access-control-allow-origin" not in request.headers


async def test_responses_to_the_ui_expose_the_request_id(client):
    response = await client.post(
        "/v1/reports.dailySummary", json={}, headers={"Origin": UI_ORIGIN, "Authorization": f"Bearer {VIEWER}"}
    )

    assert response.status_code == 200
    assert response.headers["access-control-allow-origin"] == UI_ORIGIN
    assert "x-request-id" in response.headers["access-control-expose-headers"].lower()
    assert response.headers["x-request-id"].startswith("req_")
