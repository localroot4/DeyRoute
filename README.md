# DEYROUTE Tunnel Manager

<div dir="rtl">

**DEYROUTE** تانل ضدفیلتر بین سرور ایران (**Hub**) و سرورهای خارج (**Node**) می‌سازد. کاربر فقط به `IP ایران:پورت` وصل می‌شود و ترافیکش بدون دست خوردن TLS به سرویس روی سرور خارج (Xray / Marzban / 3x-ui …) می‌رسد. اگر فیلترینگ روش فعلی را ببندد، خودکار به روش بعدی و بعد به سرور خارج پشتیبان می‌رود.

> **وضعیت: نسخهٔ توسعه (edge).** نصب یک‌خطی، Join، ساخت تانل با نردبان ۸ پله‌ای، failover خودکار بین روش‌ها و Nodeها، Node پشتیبان، منوی کامل انگلیسی و همه دستورهای CLI کار می‌کنند و روی systemd و nftables واقعی آزموده شده‌اند ([شواهد](docs/en/acceptance.md)). مانده: تست ۷۲ ساعته روی سرور واقعی در ایران. همان دستور نصب را دوباره بزنید تا ارتقا بگیرید؛ کانفیگ دست نمی‌خورد.
>
> 📖 راهنمای کامل فارسی: [docs/fa](docs/fa/index.md) — نصب، Join، اولین تانل، Node پشتیبان، عیب‌یابی، جابه‌جایی Hub، بکاپ

## نصب با یک خط

روی **سرور ایران (Hub)** با کاربر root:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
```

ویزارد فقط نقش (`Hub`) و نام (مثلاً `ir-1`) را می‌پرسد و در پایان یک **Join command** نشان می‌دهد. همان یک خط را روی **سرور خارج (Node)** بزنید:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://TOKEN@HUB_IP:44433#sha256:…'
```

نصب بدون سؤال:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --role hub --name ir-1 --yes
```

| گزینه | کار |
| --- | --- |
| `--version V` | نصب یک نسخهٔ مشخص |
| `--mirror URL` یا `DEYROUTE_MIRROR` | اول از Mirror خودتان دانلود کن |
| `--local FILE.tar.gz` | نصب آفلاین (وقتی GitHub از سرور ایران در دسترس نیست) |
| `--no-setup` | فقط نصب باینری |
| `--skip-signature` | بدون بررسی امضا (فقط برای تست) |

نصاب فقط فایل‌های ریلیز DEYROUTE را دانلود می‌کند، `SHA256SUMS` و امضای minisign را حتماً بررسی می‌کند، و هیچ دادهٔ دیگری به بیرون نمی‌فرستد. اجرای دوباره = تعمیر یا ارتقا.

## مدیریت

`deyroute` یا `dey` بدون آرگومان منوی کامل را باز می‌کند. با عدد و Enter کار کنید؛ `q` برگشت است و `?` راهنما.

```text
 1) Dashboard   2) Tunnels   3) Nodes   4) Ports   5) Failover   6) Diagnostics
 7) Optimize    8) Security  9) Notifications  10) Backup & Restore  11) Update  12) Settings
```

دستورهای پرکاربرد (هر کاری که منو می‌کند با CLI هم می‌شود؛ `--json` برای اسکریپت):

```bash
deyroute status                          # داشبورد متنی
deyroute node join-command               # ساخت Join command برای سرور خارج
deyroute tunnel add --node de-1 --ports 443,2053
deyroute tunnel backup add main --node nl-1
deyroute port check 443                  # چک چهارمرحله‌ای پورت
deyroute logs main -f                    # لاگ Hub و Node با هم
deyroute doctor                          # گزارش عیب‌یابی (بدون رازها)
deyroute update                          # ارتقا بدون قطع تانل‌ها
deyroute uninstall                       # حذف کامل و برگرداندن سیستم
```

هر خطا یک کد ثابت دارد (مثل `DEY-P012`) با سه خط «چه شد / چرا / راه‌حل». فهرست کامل: [docs/ERRORS.md](docs/ERRORS.md).

## پیش‌نیازها

Ubuntu 22.04 / 24.04 / 26.04 یا Debian 12 / 13 (پشتیبانی اصلی)؛ Rocky / Alma / Fedora / Arch هم باید کار کنند. معماری amd64 یا arm64، systemd ۲۴۵ به بالا، کرنل ۵.۴ به بالا، و root. به Python، Docker یا Node نیازی نیست.

## فایل‌ها

| مسیر | محتوا |
| --- | --- |
| `/etc/deyroute/config.yaml` | تنها منبع پیکربندی |
| `/etc/deyroute/secrets/` | کلیدها و توکن‌ها (0600) |
| `/var/lib/deyroute/state.db` | وضعیت، رویدادها و آمار |
| `/var/log/deyroute/` | لاگ‌ها |

</div>

---

## English summary

DEYROUTE is a single static Go binary that turns an Iran server (**hub**) and foreign servers (**nodes**) into a censorship-resistant tunnel. It has multiple transports (Backhaul, Rathole, FRP, Xray-Reality, Hysteria2, Waterwall, WireGuard/AmneziaWG, direct) with automatic transport and node failover.

**Status:** development (edge) builds. One-line install, join, tunnels on an 8-rung transport ladder, automatic transport and node failover, backup nodes, the complete English menu and every CLI command work, tested with real systemd, nftables and backends ([evidence](docs/en/acceptance.md)). Still to do: the 72-hour test on real servers. Re-running the install command upgrades in place.

```bash
# hub (Iran server), as root
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
# node (foreign server): the join line printed by the hub
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://…'
```

- Guides: [English](docs/en/index.md) · [فارسی](docs/fa/index.md) · [error codes](docs/ERRORS.md) · [backends](docs/backends/) · [`--json` output](docs/cli-json.md)
- Project: [architecture](docs/dev/ARCHITECTURE.md) · [specification (fa)](docs/spec/DEYROUTE-spec-v1.0.fa.md) · [decisions and open questions](QUESTIONS.md) · [acceptance evidence](docs/en/acceptance.md)
- Tests: `make test lint` · integration scenarios S01–S30 in systemd containers: `test/integration/run.sh` (see the script header)
- Build from source: `make build` (static, `CGO_ENABLED=0`) · `make build-all` · `make test lint`

### Releases (maintainers)

Each push to `main` or the development branch runs `.github/workflows/release-edge.yml`. It tests, builds amd64 and arm64, signs `SHA256SUMS` with minisign and publishes a GitHub release marked *latest*, so the URLs above always point to the newest build. Signing needs the repository secret **`MINISIGN_SECRET_KEY`**, whose public half is `internal/install/keys.go` / `installer/install.sh`. Generate a new pair with `go run ./scripts/minisign keygen`.
