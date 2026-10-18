"""The canonical error model (ADR-0003), as the Python service emits it.

ERROR_CATALOG mirrors the IR; tests/test_contract.py compares the two, so this
service cannot invent a code or change a category on its own, like the Go service.
"""

from __future__ import annotations

from typing import Any

from starlette.requests import Request
from starlette.responses import JSONResponse

CATEGORY_STATUS = {
    "validation": 400,
    "unauthenticated": 401,
    "forbidden": 403,
    "not_found": 404,
    "conflict": 409,
    "unprocessable": 422,
    "rate_limited": 429,
    "internal": 500,
    "unavailable": 503,
}

VALIDATION_FAILED = "VALIDATION_FAILED"
UNAUTHENTICATED = "UNAUTHENTICATED"
FORBIDDEN = "FORBIDDEN"
INTERNAL = "INTERNAL"
UNAVAILABLE = "UNAVAILABLE"
INVALID_RANGE = "reports.INVALID_RANGE"

# code -> (category, retryable)
ERROR_CATALOG: dict[str, tuple[str, bool]] = {
    VALIDATION_FAILED: ("validation", False),
    UNAUTHENTICATED: ("unauthenticated", False),
    FORBIDDEN: ("forbidden", False),
    "RATE_LIMITED": ("rate_limited", True),
    INTERNAL: ("internal", False),
    UNAVAILABLE: ("unavailable", True),
    INVALID_RANGE: ("unprocessable", False),
}


class ModuraError(Exception):
    """Raised by handlers; turned into the canonical error response."""

    def __init__(self, code: str, message: str, details: dict[str, Any] | None = None) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.details = details


def error_response(request: Request, code: str, message: str, details: dict[str, Any] | None = None) -> JSONResponse:
    category, retryable = ERROR_CATALOG[code]
    body: dict[str, Any] = {"code": code, "message": message, "retryable": retryable}
    if operation := getattr(request.state, "operation", None):
        body["operation"] = operation
    if request_id := getattr(request.state, "request_id", None):
        body["requestId"] = request_id
    if details:
        body["details"] = details
    return JSONResponse(body, status_code=CATEGORY_STATUS[category])
