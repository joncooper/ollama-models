# ollama-models

Command-line tool for browsing the official Ollama library over HTTP.

It does not call the local `ollama` daemon. It only fetches:

- `https://ollama.com/search?q=...`
- `https://ollama.com/library/<model>/tags`
- `https://ollama.com/library/<model:tag>`

## Commands

### `search <query>`

Lists model families from official Ollama search results.

The CLI applies an additional local name filter so broad server-side matches do not pull in unrelated families. For example, `search qwen3.5` keeps `qwen3.5` and `user/qwen3.5-*` results, but excludes `qwen2.5`.

```bash
go run . search qwen3.5
```

### `tags <model>`

Lists available tags for a model from `/library/<model>/tags`.

```bash
go run . tags qwen3.5
```

### `info <model:tag>`

Prints summary, downloads, updated time, metadata, and a README snippet for a specific model tag.

```bash
go run . info qwen3.5:latest
```

## Help

Root help:

```bash
go run . --help
```

Subcommand help:

```bash
go run . search --help
go run . tags --help
go run . info --help
```

## Tests

Run the test suite with:

```bash
go test ./...
```
