# Security policy

## Reporting a vulnerability
Please report privately through GitHub's "Report a vulnerability" (Security tab) rather than a public issue.
Include steps to reproduce and the version (`jobscout version`). We aim to acknowledge within 5 working days.

## Scope notes
jobscout is a local application. The dashboard binds to `127.0.0.1`, rejects non-loopback `Host` headers, requires a custom
header on mutating requests, and ships a strict Content-Security-Policy. Do not expose it to a network without adding
authentication in front of it. Scraped job text is untrusted input.
