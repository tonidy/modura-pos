"""`uv run python -m modura_reports` serves the reports API and runs the consumer."""

import logging

import uvicorn

from .app import create_app
from .config import load


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    settings = load()
    uvicorn.run(create_app(settings), host="0.0.0.0", port=settings.port, log_level="info")


if __name__ == "__main__":
    main()
