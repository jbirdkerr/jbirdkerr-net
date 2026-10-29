# Pants targets for apps/shortener.
#
# Assumes `apps/shortener/src` is a Pants source root (add it to [source].root_patterns in
# pants.toml). Imports of `workers` and `pyodide` only exist inside the Cloudflare runtime, so the
# files that use them carry `# pants: no-infer-dep` on those lines.

python_requirements(
    name="reqs",
    source="pyproject.toml",
)

python_sources(
    name="src",
    sources=["src/**/*.py", "scripts/*.py"],
)

files(
    name="migrations",
    sources=["migrations/*.sql"],
)

python_test_utils(
    name="test_utils",
    sources=["tests/conftest.py"],
)

python_tests(
    name="tests",
    sources=["tests/test_*.py"],
    dependencies=[":migrations"],
)
