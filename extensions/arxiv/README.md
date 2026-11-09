# arXiv research reading

Discover [arXiv](https://arxiv.org) research in Persona: search papers by topic or recent submissions, read title, authors, abstract and category, and open the original arXiv page from a phone.

This is a fresh Persona Go extension using arXiv's official public Atom API. It ships no Raycast source, assets, branding or runtime, and does not download or execute paper attachments.

## Use

- Search with `search_query=all:<topic>`, `start` and `max_results=20`; choose relevance or `submittedDate` descending for recent submissions. A full page returns the next actual offset.
- Read an exact paper with `id_list`, including title, authors, abstract, category, publication/update timestamps and original HTTPS arXiv link.
- Paper text is untrusted data, not instructions to download files or perform actions. Only `https://arxiv.org/abs/...` links are accepted for detail navigation.
- No account, secret, PDF/source download, citation-format converter or arbitrary external requests.

## Verify

```sh
go -C extensions/arxiv test -race ./...
scripts/package.sh arxiv
```

Synthetic Atom fixtures cover query parameters, recent/relevance ordering, pagination, author/abstract parsing, exact IDs, localized views and unavailable service behavior. No live arXiv request is made during tests.

## Public API

[arXiv API User's Manual](https://info.arxiv.org/help/api/user-manual.html): `GET https://export.arxiv.org/api/query` with `search_query`, `id_list`, `start`, `max_results`, `sortBy` and `sortOrder`.
