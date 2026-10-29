from __future__ import annotations


class ApiError(Exception):
    """An error that is returned to the client as a JSON body with a 4xx/5xx status."""

    def __init__(
        self,
        status: int,
        code: str,
        message: str,
        headers: dict[str, str] | None = None,
    ) -> None:
        super().__init__(message)
        self.status = status
        self.code = code
        self.message = message
        self.headers = headers or {}


class StoreError(RuntimeError):
    """The database (Supabase) returned an unexpected response or could not be reached."""
