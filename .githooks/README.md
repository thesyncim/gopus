# Git hooks

Enable once per clone:

```sh
git config core.hooksPath .githooks
chmod +x .githooks/prepare-commit-msg
```

`prepare-commit-msg` removes `Co-authored-by: Cursor <cursoragent@cursor.com>` from commit messages before they are finalized.
