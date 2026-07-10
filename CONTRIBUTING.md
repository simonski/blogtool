# Contributing

Thanks for your interest in blogtool!

## Issues first

The best way to contribute is to [open a GitHub issue](https://github.com/simonski/blogtool/issues)
— bug reports, feature ideas, docs gaps, or just "this surprised me" are all
welcome. For bugs, please include:

- the output of `blog version`
- your OS and how you installed (`brew` or from source)
- what you ran and what happened (paste the command and its output)

## Pull requests

Small fixes are welcome directly; for anything larger, please open an issue
first so we can agree on the approach before you invest the time.

To work on the code:

```bash
make build      # -> bin/blog
make test       # go vet + go test
```

Keep the tool's philosophy in mind: a single binary, minimal dependencies,
and the generated site stays fully static — anything dynamic (live reload,
search, the editor) happens at design time only and must never leak into
`output/`.
