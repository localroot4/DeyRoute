# Contributing

Issues and pull requests are welcome, in English or Persian.

## Reports from real networks help most

Filtering changes often and differs between providers. If a transport stops
working, or a new pattern appears, open an issue with the version, the
providers of hub and node, `deyroute status`, and the `DEY-` codes you see.
Never paste tokens, keys or join commands; `deyroute doctor` makes a report
with secrets removed.

## Building and testing

```bash
make build        # static binary in dist/
make test lint    # unit tests and linters
make screens      # redraw the screenshots after a change to the output
test/integration/run.sh S01   # one integration scenario in systemd containers
```

Go 1.25 or newer. The integration scenarios need Docker; see the head of
`test/integration/run.sh`.

## Pull requests

- Keep a change focused, and add or update tests for it.
- User-facing text lives in `internal/i18n`; errors get a fixed code in
  `internal/errors` (`docs/ERRORS.md` is generated from it: `make docs`).
- Add a line to `CHANGELOG.md` under `[Unreleased]`.
- How the parts fit together: [docs/dev/ARCHITECTURE.md](docs/dev/ARCHITECTURE.md).

---

<div dir="rtl">

## مشارکت

از Issue و Pull Request، به فارسی یا انگلیسی، استقبال می‌کنیم. بیشترین کمک، گزارش از شبکه‌های
واقعی است: اگر روشی از کار افتاد یا الگوی تازه‌ای از فیلترینگ دیدید، نسخه، دیتاسنتر هاب و نود،
خروجی `deyroute status` و کدهای `DEY-` را بنویسید. توکن، کلید یا دستور join را هرگز نگذارید.

</div>
