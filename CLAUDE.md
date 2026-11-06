# CLAUDE.md

Instructions for AI agents (and people) changing this repository. [CONTRIBUTING.md](CONTRIBUTING.md) has the full workflow; this file holds the rules that must never be broken.

## No company-internal data, no personal data

This is a public repository. Never commit an extension, a file, a test fixture, a screenshot, a commit message or a pull request text that leaks:

- **Company-internal data:** internal systems and services, internal addresses (intranet domains, IPs, URLs, host names), internal APIs and their endpoints or schemas, internal tools and the data they return, private or internal repository and project names, and employee or organization information (names, departments, org charts, office locations).
- **Personal data:** real names, email addresses, phone numbers, accounts and user ids, tokens, passwords, API keys, private keys and key identifiers, and any real message, document or link.

Extensions here use public services and protocols only, and do not depend on company accounts, private networks or internal tools. An extension that needs any of the above belongs in the internal plugin repository on the company network, not here. When one is found here, move it there and remove it from this repository and its market index in the same change. If you cannot tell whether something is safe to publish, keep it out until that is settled.

Use synthetic fixtures and placeholders (`<hub address>`, `me@example.com`) instead. Specific values of a person's environment go in configuration, and secrets go through the hub's encrypted secret flow.

## Language

English for documentation, code comments, commit messages and pull request texts. Shipped assistant skills, prompts and UI text keep their original language.
