# Contributing

[README](README.md) · [Documentation](docs/README.md)

## Language and scope

- Use English for manually maintained documentation, code comments, commit subjects and bodies, and pull request titles and descriptions. Keep published history intact: do not rewrite historical commits, and do not force-push.
- Keep [README.md](README.md) as the concise English overview and [README.zh-CN.md](README.zh-CN.md) as its corresponding Chinese translation, with reciprocal links at the top. Put detailed instructions in [docs/](docs/README.md).
- Preserve localized UI text, runtime messages, assistant prompts and skills, and language-sensitive test fixtures unless a change explicitly targets those behaviors. A runtime `SKILL.md` is shipped to the assistant, not merely a contributor guide.
- The [SDK copy](sdk/VERSION) and generated wire types/schemas follow the upstream sync process; do not hand-edit them to clean up wording. See [SDK ownership](docs/reference.md#sdk-ownership).

This is a public repository: contributions use public services and protocols only, and nothing that leaks company-internal data or personal data may be committed. [CLAUDE.md](CLAUDE.md) spells out the rule and where such extensions go instead.

## Workflow

1. Check [open issues](https://github.com/nwverse/persona-extensions/issues) and [pull requests](https://github.com/nwverse/persona-extensions/pulls) for related work; coordinate overlapping changes before editing.
2. Work on a non-default branch. Follow the [development guide](docs/development.md), and keep behavioral changes separate from documentation-only cleanup.
3. Run `scripts/ci.sh` and the [documentation checks](docs/development.md#documentation-checks). Report commands, results and any checks you could not run.
4. Submit a focused pull request when you are authorized to publish the changes. A version bump on `main` can trigger a release; see [release behavior](docs/releases.md#automated-releases) before changing manifests.

Review translated documentation against the original: preserve setup requirements, safety boundaries, compatibility notes and troubleshooting details, not just headings.
