# AppOps — Initial Repository Reconnaissance

Read `infra/appops/docs/design/IMPLEMENTATION.md` in full.

I want to begin implementing AppOps, a Go/Pulumi project that provisions isolated PostgreSQL application roles and schemas and publishes application connection strings to Cloudflare Secrets Store.

**Do not implement anything yet.** First, inspect the existing repository and compare its actual structure and conventions against the implementation document.

Investigate:

1. **Application discovery**
   - Identify the immediate child directories under `apps/`.
   - Identify which applications currently use PostgreSQL.
   - Identify existing application configuration conventions that AppOps should respect.

2. **Database and migrations**
   - Find the shared migration directory and determine how migrations are executed.
   - Identify the current database roles and schema usage, especially anything using `public`.
   - Inspect how applications configure their PostgreSQL connections.
   - Determine whether the existing migration workflow can target individual schemas without restructuring the shared migration directory.

3. **Cloudflare integration**
   - Identify how applications currently consume secrets and database connection strings.
   - Determine which Cloudflare resources or configuration conventions AppOps will need to integrate with.

4. **Pulumi and Go**
   - Check whether the repository already contains Pulumi projects, Go modules, or relevant tooling conventions.
   - Identify any existing patterns worth reusing.

## Deliverables

Produce a report containing:

- A summary of the existing repository structure relevant to AppOps.
- A list of applications that should initially be onboarded.
- Any discrepancies between the implementation document and the actual repository.
- Important unknowns or decisions that need to be resolved before implementation.
- A proposed implementation sequence broken into small, independently testable milestones.
- The files you expect to create or modify in the first milestone.

**Constraints**

- Do not modify existing files.
- Do not create infrastructure or execute migrations.
- Do not access or expose existing credentials or secret values.
- Do not invent repository conventions when the evidence is unclear.
- Prefer the simplest implementation that satisfies the implementation document.
- Preserve the existing shared migration directory structure.

Wait for my review of the report before proceeding with implementation.
