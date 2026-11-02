"""Cloudflare Python Worker entrypoint."""

from shortener.app import app
from workers import asgi  # pants: no-infer-dep

Default = asgi.entrypoint(app)
