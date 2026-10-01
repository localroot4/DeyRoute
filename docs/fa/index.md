# راهنمای DEYROUTE Tunnel Manager

<div dir="rtl">

[English](../en/index.md)

**DEYROUTE** بین سرور ایران (**Hub**) و یک یا چند سرور خارج (**Node**) یک تانل مقاوم در برابر فیلترینگ می‌سازد. کاربر فقط به `IP ایران:پورت` وصل می‌شود و ترافیکش، بدون اینکه تانل به TLS آن دست بزند، به سرویس اصلی روی سرور خارج (Xray، Marzban، 3x-ui و…) می‌رسد. اگر فیلترینگ روش فعلی تانل را ببندد، DEYROUTE خودش به روش بعدی می‌رود و اگر سرور خارج از کار بیفتد، به سرور خارج پشتیبان.

## شروع سریع (۵ دقیقه)

چیزهایی که لازم دارید: یک سرور ایران برای Hub، یک سرور خارج که سرویس VPN شما از قبل روی آن کار می‌کند (مثلاً Xray روی پورت 443)، و دسترسی root روی هر دو. سیستم‌عامل پیشنهادی: Ubuntu 22.04/24.04/26.04 یا Debian 12/13 (amd64 یا arm64). فهرست کامل در [نصب](install.md).

۱. **روی سرور ایران (Hub)** با کاربر root بزنید:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
```

نقش را `1` (یعنی hub) بدهید، یک نام بگذارید (مثلاً `ir-1`) و بقیه سؤال‌ها را با Enter رد کنید. در آخر یک **Join command** نشان داده می‌شود.

۲. **روی سرور خارج (Node)** با کاربر root همان Join command را بزنید. شکلش این است و فقط یک بار و تا ۱۵ دقیقه معتبر است:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://TOKEN@HUB_IP:44433#sha256:…' --version 1.0.0
```

معمولاً ظرف ۳۰ ثانیه Node روی Hub ظاهر می‌شود (`deyroute node list`).

۳. **روی Hub** تانل را بسازید (شناسه Node را از خروجی `deyroute node list` بردارید):

```bash
deyroute tunnel add --node de-1 --ports 443,2053 --name main
```

یا منو را با `deyroute` (یا `dey`) باز کنید و `2) Tunnels` ← `1) Add tunnel` را بزنید. تانل روی پله اول نردبان پیش‌فرض بالا می‌آید و در آخر خطی مثل `Tunnel main is UP via backhaul/wssmux (41ms)` می‌بینید.

۴. **بررسی:**

```bash
deyroute status
deyroute port check 443
```

۵. **در کانفیگ کاربران** فقط آدرس سرور خارج را به IP سرور ایران عوض کنید. بقیه چیزها (پورت، UUID، SNI، host، path) همان بماند.

پیشنهاد: یک Node دوم هم Join کنید و آن را پشتیبان تانل کنید:
`deyroute tunnel backup add main --node nl-1` (صفحه [Node پشتیبان](backup-node.md)).

## صفحه‌ها

| صفحه | موضوع |
| --- | --- |
| [نصب](install.md) | پیش‌نیازها، نصب Hub، نصب بدون سؤال و آفلاین، Mirror، کارهایی که نصاب چک می‌کند، اجرای دوباره = تعمیر/ارتقا، فایل‌ها |
| [Join کردن Node](join.md) | Join command، مدت اعتبار، اتفاقاتی که روی Node می‌افتد، تغییر IP، نسخه ناسازگار |
| [اولین تانل](first-tunnel.md) | ساخت تانل با منو و با دستور، نوشتن پورت‌ها، مراحل پیشرفت، بررسی سلامت، لاگ |
| [Node پشتیبان](backup-node.md) | Node پشتیبان، طرز کار Failover و Failback، زمان‌ها، پله‌های گرم، سوییچ و توقف دستی |
| [عیب‌یابی](troubleshooting.md) | doctor، لاگ، رویدادها، شکل خطاها، کدهای DEY رایج و راه‌حل هرکدام |
| [پرسش‌های فیلترینگ](faq-filtering.md) | نردبان ترنسپورت‌ها، پله skip‌شده، decoy SNI، چرا TLS دست نمی‌خورد، IP واقعی کاربر، کارهایی که DEYROUTE نمی‌کند |
| [جابه‌جایی Hub](hub-move.md) | بردن Hub به سرور یا IP جدید بدون Join دوباره Nodeها |
| [بکاپ و ریستور](backup.md) | بکاپ، رمز عبور، `DEYROUTE_BACKUP_PASSPHRASE`، بکاپ خودکار، محتوای بکاپ |
| [آپدیت و حذف](update-uninstall.md) | آپدیت deyroute و بک‌اندها، برگشت به نسخه قبل، حذف کامل |
| [امنیت](security.md) | کانال کنترل، توکن Join، جدول فایروال، رازها، عوض کردن توکن و CA، TLS، audit، بدون telemetry |
| [Benchmarks (انگلیسی)](../en/benchmarks.md) | روش اندازه‌گیری مصرف منابع و زمان Failover (هنوز اندازه‌گیری نشده) |

مرجع‌ها (انگلیسی): [کدهای خطا](../ERRORS.md) · [خروجی `--json`](../cli-json.md) · صفحه هر بک‌اند: [backhaul](../backends/backhaul.md)، [rathole](../backends/rathole.md)، [frp](../backends/frp.md)، [xray](../backends/xray.md)، [hysteria2](../backends/hysteria2.md)، [waterwall](../backends/waterwall.md)، [wireguard / awg](../backends/wireguard.md)، [direct](../backends/direct.md)، [gost](../backends/gost.md)، [chisel](../backends/chisel.md).

## چند واژه

| واژه | معنی |
| --- | --- |
| Hub | سرور ایران؛ کاربران به آن وصل می‌شوند؛ منو، پایگاه داده و موتور Failover روی آن است |
| Node | سرور خارج؛ سرویس VPN واقعی شما و سر دیگر تانل روی آن است |
| تانل (Tunnel) | یک مسیر از Hub به یک Node، با یک یا چند پورت |
| ترنسپورت (Transport) | یک روش مشخص تانل‌زدن، مثل `backhaul/wssmux` یا `rathole/noise` |
| نردبان و پله (Ladder / rung) | فهرست مرتب ترنسپورت‌های یک تانل؛ هر ردیف یک پله است |
| Failover / Failback | سوییچ خودکار بعد از خرابی تأییدشده / برگشت خودکار به پله اول |
| گرم (Warm) | نصب و آماده ولی خاموش؛ حافظه‌ای مصرف نمی‌کند |

## منو

زدن `deyroute` یا `dey` بدون هیچ چیز دیگر، منو را باز می‌کند. عدد گزینه را بزنید و Enter؛ `q` یا Esc برگشت است، `r` تازه‌سازی و `?` راهنمای همان صفحه. گزینه‌های ستاره‌دار (`*`) فقط در حالت Advanced دیده می‌شوند (`12) Settings` ← `1) UI mode (Simple / Advanced)`).

```text
 1) Dashboard (live)
 2) Tunnels        add / edit / enable-disable / restart / switch transport / delete
 3) Nodes          show join command / list / rename / remove / test
 4) Ports          add port to tunnel / remove / check port / firewall status
 5) Failover       policy / ladder order / backup nodes / thresholds *
 6) Diagnostics    port check · tunnel test · speed test · logs · doctor
 7) Optimize       sysctl profile · BBR · limits *
 8) Security       rotate tokens · TLS · firewall · view fingerprints *
 9) Notifications  Telegram bot setup / test message
10) Backup & Restore
11) Update         deyroute / backends / manifest
12) Settings       ui mode (Simple/Advanced) · language · uninstall
 0) Exit
```

روی سرور Node هم شماره‌ها همین‌ها هستند. گزینه‌هایی که فقط کار Hub است با `(hub only)` مشخص شده‌اند و انتخابشان فقط می‌گوید که این کار از منوی Hub انجام می‌شود. روی Node این‌ها کار می‌کنند: `1) Dashboard`، `6) Diagnostics` (لاگ‌ها و doctor)، `10) Backup & Restore` (همراه با `3) Set hub address` بعد از [جابه‌جایی Hub](hub-move.md)) و `12) Settings` (حذف).

هر کاری که منو می‌کند با یک دستور هم انجام می‌شود. `deyroute --help` همه دستورها را نشان می‌دهد و `deyroute <دستور> --help` گزینه‌ها و مثال‌ها را. همه دستورها `--json` را برای اسکریپت می‌پذیرند ([شکل خروجی](../cli-json.md)) و `--debug` (یا `DEYROUTE_DEBUG=1`) را برای لاگ مفصل.

کد خروج دستورها: `0` موفق، `1` خطای کاربر (کدهای `DEY-C/P/T/N/B/F/S/I`، آرگومان غلط، تأیید نکردن)، `2` خطای سیستم (`DEY-X`)، `3` دستور مخرب بدون ترمینال اجرا شده و `--yes` لازم دارد.

## فایل‌ها کجا هستند

| مسیر | محتوا |
| --- | --- |
| `/usr/local/bin/deyroute` و `/usr/local/bin/dey` | خود برنامه و نام کوتاهش |
| `/etc/deyroute/config.yaml` | تنها فایل تنظیمات (0600) |
| `/etc/deyroute/secrets/` | کلیدها، توکن‌ها و گواهی‌ها (0700، فایل‌ها 0600) |
| `/var/lib/deyroute/state.db` | وضعیت لحظه‌ای، سابقه پروب‌ها، رویدادها |
| `/var/lib/deyroute/backups/` | بکاپ‌ها؛ بکاپ‌های خودکار در `auto/` |
| `/var/log/deyroute/` | `deyroute.log`، `hub.log` یا `node.log`، `events.log`، `tunnels/<tunnel>.log` |
| سرویس‌های systemd | `deyroute-hub.service`، `deyroute-node.service`، و `deyroute-tun@….service` (برای هر ترنسپورت گرم یکی) |

</div>
