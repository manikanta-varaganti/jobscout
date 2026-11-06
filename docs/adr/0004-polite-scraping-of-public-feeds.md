# ADR 0004: Only public ATS JSON feeds, scraped politely

Status: accepted

## Decision
Providers use the public JSON endpoints that ATS vendors publish for career pages. The client identifies itself with a
configurable User-Agent, caps concurrency per host, retries with backoff and `Retry-After`, sends `If-None-Match`
where supported, and caps response size. Providers needing logins, CAPTCHA bypass or ToS violations are out of scope.

## Consequences
Coverage is limited to platforms with public feeds. That is an acceptable trade for a tool distributed to other users.
