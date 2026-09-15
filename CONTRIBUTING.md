# Contributing to Exo CLI

Thanks for your interest in contributing to `exo`! This document explains how to
build the project, run the tests, and submit changes.

## Code of Conduct

By participating in this project you agree to uphold a respectful, harassment-free
environment for everyone. Please be kind and constructive in issues, reviews, and
discussions.

## Getting started

`exo` is a Go CLI that orchestrates `git-annex` operations. To build and test it
locally you will need:

- [Go](https://go.dev/dl/) 1.24 or newer
- [`git-annex`](https://git-annex.branchable.com/install/) (required for
  integration tests and most runtime workflows)
- `git`
- `make`

Clone the repository and build the binaries:

```sh
git clone https://github.com/Genentech/exohub.git
cd exohub
make binaries
```

## Running the tests

Unit tests:

```sh
make test
```

Integration tests (require `git-annex` on your `PATH`):

```sh
make test-integration
```

Everything, with coverage:

```sh
make test-all-coverage
```

## Making changes

1. **Open an issue first** for anything beyond a trivial fix, so we can agree on
   the approach before you invest time in an implementation.
2. **Create a topic branch** off `main` for your work.
3. **Keep changes focused.** One logical change per pull request makes review
   faster and history easier to follow.
4. **Add or update tests** for any behaviour you change.
5. **Run `gofmt`/`go vet`** and make sure `make test` passes before opening a PR.
6. **Write clear commit messages.** A short imperative summary line, followed by a
   body explaining the *why* when it isn't obvious.

## Pull requests

- Describe what the change does and why.
- Link the issue it addresses (e.g. `Closes #123`).
- Ensure CI is green. Maintainers may ask for changes; please keep the discussion
  on the PR thread.

## Reporting bugs and requesting features

Use the GitHub issue tracker. For bug reports, include:

- What you expected to happen and what actually happened.
- The `exo` version (`exo --version`) and your OS.
- Minimal steps to reproduce, and any relevant logs or error output.

## Security

Please do **not** report security vulnerabilities through public issues. See
[SECURITY.md](SECURITY.md) for how to report them responsibly.

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE) that covers this project.
