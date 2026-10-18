"""The parts of the contract that hold for every operation, read from the generated
OpenAPI document, like the Go service's contract middleware: authentication,
authorization from x-modura-requires, and request validation against the schemas.
Handlers only ever see valid, authorized input.
"""

from __future__ import annotations

import json
import secrets
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from openapi_core import OpenAPI
from openapi_core.contrib.starlette import StarletteOpenAPIRequest
from openapi_core.exceptions import OpenAPIError
from starlette.requests import Request

from .errors import FORBIDDEN, UNAUTHENTICATED, VALIDATION_FAILED, ModuraError


@dataclass(frozen=True)
class Operation:
    id: str
    path: str
    kind: str
    requires: tuple[str, ...]


@dataclass(frozen=True)
class Identity:
    name: str
    permissions: frozenset[str]


def demo_identities(cashier_token: str, viewer_token: str) -> dict[str, Identity]:
    readers = frozenset({"catalog.read", "inventory.read", "registry.read", "reports.read"})
    return {
        cashier_token: Identity("cashier", readers | {"sales.write"}),
        viewer_token: Identity("viewer", readers),
    }


class Contract:
    def __init__(self, document: dict[str, Any]) -> None:
        self.document = document
        self.openapi = OpenAPI.from_dict(document)
        self.operations: dict[str, Operation] = {}
        for path, item in document["paths"].items():
            post = item["post"]
            requires = tuple(post.get("x-modura-requires") or ())
            if not post.get("x-modura-operation") or not requires:
                raise ValueError(f"{path}: missing x-modura-operation or x-modura-requires")
            self.operations[path] = Operation(post["x-modura-operation"], path, post["x-modura-kind"], requires)

    @classmethod
    def load(cls, path: Path) -> Contract:
        return cls(json.loads(path.read_text(encoding="utf-8")))

    async def admit(self, request: Request, identities: dict[str, Identity]) -> tuple[Operation, dict[str, Any]]:
        """Authenticates, authorizes and validates a request; returns its operation and body."""
        operation = self.operations[request.url.path]
        request.state.operation = operation.id

        identity = identities.get(bearer_token(request))
        if identity is None:
            raise ModuraError(UNAUTHENTICATED, "Send a valid bearer token.")
        for permission in operation.requires:
            if permission not in identity.permissions:
                raise ModuraError(
                    FORBIDDEN,
                    f"{operation.id} requires {permission}, which {identity.name} does not have.",
                    {"permission": permission},
                )

        body = await request.body()
        try:
            self.openapi.validate_request(StarletteOpenAPIRequest(request, body=body))
        except OpenAPIError as error:
            raise ModuraError(VALIDATION_FAILED, describe(error)) from None
        return operation, json.loads(body)


def bearer_token(request: Request) -> str:
    scheme, _, token = request.headers.get("authorization", "").partition(" ")
    return token.strip() if scheme == "Bearer" else ""


def describe(error: BaseException) -> str:
    """The most specific message in openapi-core's chain of causes."""
    messages = []
    current: BaseException | None = error
    while current is not None:
        messages.append(str(current))
        schema_errors = getattr(current, "schema_errors", None)
        if schema_errors:
            first = schema_errors[0]
            location = "/".join(str(p) for p in getattr(first, "absolute_path", []))
            messages.append(f"{location}: {first.message}" if location else first.message)
            break
        current = current.__cause__
    return messages[-1] if messages else "The request does not match the operation input schema."


def new_request_id() -> str:
    return "req_" + secrets.token_hex(8)
