# Domains

Look up one `.com` or `.net` domain's public registration record in Persona through the IANA RDAP bootstrap and Verisign's public HTTPS RDAP service. No account is required.

- Search a single explicitly supplied domain, including an internationalized domain name.
- See supplied registration, expiration and change dates, raw registry statuses, registrar display name when published, and the canonical registry record.
- Keep up to 20 successful lookups in a bounded, installation-owned history.
- Ask the assistant to inspect a domain with `domains_lookup`.
- Read the IANA status glossary for the meaning of registry-specific status values.

A missing record is reported as missing, not as proof that a domain can be registered. The extension does not probe DNS or hosts, scrape WHOIS pages, collect contact details, suggest names, scan for availability, judge ownership or security, or manage registrations and transfers. It refuses redirects rather than following a registry response to an arbitrary host. Only `data.iana.org` and `rdap.verisign.com` are allowed by the manifest, and requests carry no credentials. Other suffixes are rejected before any registry request.

History uses Persona's declared installation-owned `history` storage. Mutations use host-issued request IDs and compare-and-swap revisions. Failure to save history never turns a successful registry read into a failed one.

This source package is an original implementation for Persona; it contains no Raycast source or UI.

## Development

```sh
npm ci
npm test
npm run build:source
```

The build checks the official source bundle and writes a deterministic development archive. It does not sign a catalog or activate the extension in production.
