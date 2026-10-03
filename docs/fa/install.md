# نصب

<div dir="rtl">

[English](../en/install.md) · [فهرست](index.md)

نصب همیشه یک خط است. اگر همان خط را دوباره بزنید، نصب قبلی تعمیر یا ارتقا پیدا می‌کند؛ هیچ‌وقت از صفر نصب نمی‌شود و به تنظیمات شما دست نمی‌خورد.

## پیش‌نیازها

| | |
| --- | --- |
| پشتیبانی اصلی (تست‌شده) | Ubuntu 22.04، 24.04، 26.04 · Debian 12، 13 |
| باید کار کند | Ubuntu 20.04 · Debian 11 · Rocky / AlmaLinux 8، 9، 10 · دو نسخه آخر Fedora · Arch Linux |
| پردازنده | amd64 (x86_64) یا arm64 (aarch64) |
| سیستم | کاربر root، systemd نسخه ۲۴۵ به بالا، کرنل لینوکس ۵.۴ به بالا |
| بسته‌ها | `iproute2`، یکی از `nftables` یا `iptables`، `ca-certificates`، و برای نصاب `curl` یا `wget` |

به هیچ چیز دیگری نیاز نیست: نه Python، نه Docker، نه Node.js. روی Hub و Node همان یک برنامه نصب می‌شود.

نصاب همه پیش‌نیازها را چک می‌کند و اگر چیزی کم باشد با یک کد خطا و یک راه‌حل یک‌خطی می‌ایستد:

| کد | مشکل | راه‌حل |
| --- | --- | --- |
| `DEY-I001` | با root اجرا نشده | `sudo -i` و دوباره همان خط |
| `DEY-I002` / `DEY-I008` | systemd نیست / قدیمی‌تر از ۲۴۵ است | یک توزیع پشتیبانی‌شده نصب کنید |
| `DEY-I003` | پردازنده amd64 یا arm64 نیست | سرور دیگری بگیرید |
| `DEY-I009` | کرنل قدیمی‌تر از ۵.۴ است | کرنل یا توزیع را ارتقا دهید |
| `DEY-I007` | نه curl هست نه wget | `apt-get install -y curl` |
| `DEY-I010` | دستور `ip` نیست | `apt-get install -y iproute2` (در dnf: `iproute`) |
| `DEY-I011` | نه nftables هست نه iptables | `apt-get install -y nftables` |
| `DEY-I012` | گواهی‌های CA سیستم نیست | `apt-get install -y ca-certificates` |

## نصب Hub (سرور ایران)

با کاربر root:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
```

نصاب برنامه را دانلود و بررسی می‌کند و بعد ویزارد راه‌اندازی را باز می‌کند. ویزارد حداکثر پنج سؤال می‌پرسد. هر سؤال یک بلوک جداست: خط اول شمارهٔ مرحله و خود سؤال، بعد هر گزینه در یک خط، یک توضیح کوتاه، و خط آخر که دقیقاً می‌گوید چه تایپ کنید و Enter خالی چه چیزی را انتخاب می‌کند:

```text
Step 1 of 5 · Role of this server
   Set up the hub first; each node then joins it with the command the hub prints.
   1) hub    the server in Iran: your users connect to it
   2) node   the server abroad: runs your VPN service (Xray, Marzban node ...)
   Type 1 or 2 and press Enter. Enter alone = 1 (hub).
 › 1

Step 2 of 5 · Name of this hub
   Shown in the menu, the logs and the messages. Letters, digits and -, e.g. ir-1.
   Press Enter to use myhost, or type another value and press Enter.
 › ir-1

Step 3 of 5 · Public IP of this server
   The address your users and your nodes connect to.
   Detected automatically: 5.6.7.8
   Press Enter to use 5.6.7.8, or type another value and press Enter.
 ›

Step 4 of 5 · Control port for the nodes
   Your nodes connect to the hub on this TCP port (mutual TLS). Allow it in your provider's firewall too.
   Press Enter to use 44433, or type another value and press Enter.
 ›

Step 5 of 5 · Tune this server automatically (recommended)
   deyroute measured this server and lists the kernel settings that suit it:
   Measured: 2.0 GiB RAM · 2 CPU · kernel 6.1.0-21-amd64 · eth0 MTU 1500
   9 settings change (4 of them only after a reboot or the next start):
     net.ipv4.tcp_congestion_control                     cubic   → bbr
     net.core.somaxconn                                  4096    → 65535
     net.core.rmem_max                                   212992  → 33554432
     …
     net.netfilter.nf_conntrack_max                      -       → 131072  (after reboot)
   Why each one: deyroute optimize auto --dry-run (after setup). You can undo it any time with: deyroute optimize revert
   Type y (yes) or n (no) and press Enter. Enter alone = y (yes).
 ›

Summary
   Role             hub
   Name             ir-1
   Public IP        5.6.7.8
   Control port     44433
   Kernel profile   automatic (9 changes)
```

- **Public IP**: خودکار پیدا می‌شود. اگر آدرس پیداشده خصوصی یا CGNAT باشد، ویزارد می‌گوید؛ آدرسی را که کاربران به آن وصل می‌شوند (از پنل سرور) تایپ کنید.
- **Control port**: پورتی که Nodeها به آن وصل می‌شوند. پیش‌فرض `44433` است و اگر گرفته باشد، اولین پورت آزاد بعدی پیشنهاد می‌شود.
- **تنظیم خودکار (Automatic tuning)**: ویزارد پیش از این سؤال سرور را اندازه می‌گیرد (RAM، تعداد CPU، کرنل، کارت شبکه، conntrack) و هر تنظیم کرنلی را که عوض می‌کند با مقدار فعلی و مقدار جدیدش فهرست می‌کند. با جواب *بله* فایل `/etc/sysctl.d/99-deyroute.conf` با همین برنامه نوشته می‌شود (پروفایل `auto`: مقادیر balanced به‌اضافهٔ بافرهای متناسب با RAM، حدهایی که فقط بالا برده می‌شوند و هرگز پایین نمی‌آیند، رزرو پورت‌های بک‌اند و در صورت نیاز جدول conntrack به اندازهٔ مناسب). مقادیر قبلی ذخیره می‌شوند و `deyroute optimize revert` آن‌ها را برمی‌گرداند. جواب *نه* به کرنل دست نمی‌زند (پروفایل `off`). در کانتینر (OpenVZ، LXC، Docker) کرنل مال میزبان است و همهٔ موارد کرنل کنار گذاشته می‌شوند (`DEY-X064`). همهٔ مراحل، دلیل امن بودنشان و راه برگرداندنشان در [تنظیم خودکار](tuning.md) آمده است.
- بعداً `deyroute optimize auto` (منوی `7) Optimize → 1) Automatic tuning`) همین کار را روی Hub و همهٔ Nodeهای آنلاین با یک تأیید انجام می‌دهد و `deyroute optimize check` مقادیری را که کس دیگری عوض کرده گزارش می‌کند. پروفایل‌های ثابت هم هنوز هستند: `deyroute optimize apply --profile P` پروفایل `balanced` یا `aggressive` را روی Hub و همهٔ Nodeهای آنلاین با `tuning.bbr` خود Hub اعمال می‌کند؛ هر چه یک Node کنار بگذارد با `node <id>: …` نشان داده می‌شود. `aggressive` (بافرهای ۶۴MB) برای سرورهای با RAM ۴ گیگ یا بیشتر است: منو پروفایل مناسب RAM هاب را نام می‌برد و پیش از `aggressive` روی Hub کوچک‌تر هشدار می‌دهد.

بقیه کارها خودکار است و مرحله به مرحله نشان داده می‌شود:

```text
  ✔ Detect public IP
  ✔ Create internal CA
  ✔ Issue hub certificate
  ✔ Apply firewall (table inet deyroute)
  ✔ Apply kernel settings
  ✔ Write /etc/deyroute/config.yaml
  ✔ Install and start service

✔ Hub ir-1 is ready: 5.6.7.8, control port 44433.

Next: add a node (the server abroad that runs your VPN service)
   1. Run this command on the node (one node per command, valid until 12:15, 15m):

bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://…@5.6.7.8:44433#sha256:…' --version 1.0.0

   2. When the node is online (deyroute node list), create a tunnel on this hub:
      in the menu: run deyroute, then 2) Tunnels → 1) Add tunnel
      or directly: deyroute tunnel add --node <node id> --ports 443
```

این Join command فقط **یک بار** و برای یک Node کار می‌کند. برای هر Node بعدی یک Join command تازه بسازید: `deyroute node join-command`. ادامه کار: [Join کردن Node](join.md).

بعد از نصب، `deyroute` (یا `dey`) منو را باز می‌کند و `deyroute status` داشبورد را چاپ می‌کند.

## نصب بدون سؤال

جواب‌ها را به‌صورت گزینه بدهید؛ `--yes` یعنی همه پیش‌فرض‌ها قبول:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --role hub --name ir-1 --yes
```

با `--yes`، IP عمومی خودکار پیدا می‌شود، پورت کنترل 44433 (یا اولین پورت آزاد بعدی) است و سرور خودکار تنظیم می‌شود (پروفایل `auto`). همین گزینه‌ها روی برنامه نصب‌شده هم کار می‌کنند:

```bash
deyroute setup --role hub --name ir-1 --yes
deyroute setup --role hub --name ir-1 --control-port 44500
```

اگر ترمینال نباشد و گزینه‌ای هم ندهید، راه‌اندازی با `DEY-I014` می‌ایستد (ترمینالی برای ویزارد نیست). Node هیچ‌وقت این‌طور راه‌اندازی نمی‌شود؛ همیشه با لینکی که Hub می‌دهد Join می‌کند (`install.sh join 'dey://…'`).

## گزینه‌های نصاب

| گزینه | کار |
| --- | --- |
| `join 'dey://…' [--name N]` | نصب و وصل شدن به Hub به‌عنوان Node (صفحه [Join](join.md)) |
| `--role hub\|node --name N --yes` | راه‌اندازی بدون سؤال |
| `--version V` | نصب نسخه `V` به‌جای آخرین نسخه |
| `--mirror URL` | اول از این Mirror دانلود کن (همان متغیر محیطی `DEYROUTE_MIRROR`) |
| `--local FILE` | نصب آفلاین از فایل `deyroute_<ver>_linux_<arch>.tar.gz` |
| `--no-setup` | فقط نصب؛ بعد `deyroute setup` یا `deyroute restore FILE` را بزنید |
| `--skip-signature` | امضای `SHA256SUMS` را چک نکن (فقط برای تست) |
| `-h`، `--help` | نمایش گزینه‌ها |

## وقتی سرور ایران به GitHub دسترسی ندارد

GitHub از دیتاسنترهای ایران معمولاً بسته یا کند است. فقط **اولین** نصب Hub به یک منبع دانلود نیاز دارد: بعد از اینکه اولین Node وصل شد، Hub باینری بک‌اندها و آپدیت‌ها را **از طریق همان Node** می‌گیرد و دیگر خودش به GitHub نیازی ندارد.

**راه ۱ — Mirror خودتان.** فایل‌های ریلیز را روی هر سرور HTTPS که در اختیار دارید، با این چیدمان بگذارید:

```text
<base>/latest/install.sh
<base>/latest/SHA256SUMS
<base>/latest/SHA256SUMS.minisig
<base>/latest/deyroute_<version>_linux_amd64.tar.gz
<base>/latest/deyroute_<version>_linux_arm64.tar.gz
<base>/v<version>/…              (همین فایل‌ها، برای --version)
```

و با Mirror نصب کنید:

```bash
DEYROUTE_MIRROR=https://dl.example.com/deyroute bash <(curl -fsSL https://dl.example.com/deyroute/latest/install.sh)
```

فایل‌ها باز هم با امضای ریلیز بررسی می‌شوند، پس Mirror نمی‌تواند آن‌ها را عوض کند. اگر هنگام راه‌اندازی Hub متغیر `DEYROUTE_MIRROR` تنظیم باشد، به‌عنوان `hub.mirror` ذخیره می‌شود و Join commandهایی که Hub می‌سازد هم نصاب را از `<mirror>/latest/install.sh` می‌گیرند. (گزینه `--mirror URL` فقط روی همان یک بار اجرای نصاب اثر دارد.)

**راه ۲ — فایل آفلاین.** روی کامپیوتری که اینترنت آزاد دارد، از [صفحه ریلیزها](https://github.com/localroot4/DeyRoute/releases) این چهار فایل را بگیرید: `install.sh`، `SHA256SUMS`، `SHA256SUMS.minisig` و آرشیو مخصوص پردازنده سرورتان. هر چهار را در یک پوشه روی Hub بگذارید و بزنید:

```bash
scp install.sh SHA256SUMS SHA256SUMS.minisig deyroute_1.0.0_linux_amd64.tar.gz root@HUB_IP:/root/
bash /root/install.sh --local /root/deyroute_1.0.0_linux_amd64.tar.gz
```

`SHA256SUMS` و `SHA256SUMS.minisig` باید کنار آرشیو باشند؛ بدون آن‌ها هیچ چیزی نصب نمی‌شود (`DEY-I005` / `DEY-I006`).

اگر پراکسی لازم دارید، نصاب متغیر `https_proxy` را رعایت می‌کند.

## نصاب چه چیزهایی را بررسی می‌کند

۱. پیش‌نیازهای بالا.

۲. منبع دانلود به این ترتیب: `--mirror` یا `DEYROUTE_MIRROR`، بعد آدرس پیش‌فرضی که داخل نصاب ساخته شده (در بیلدهای فعلی خالی است)، بعد GitHub. هر منبع ۳ بار امتحان می‌شود (با ۲ و بعد ۴ ثانیه صبر) و بعد منبع بعدی.

۳. فایل `SHA256SUMS` باید امضای minisign معتبر کلید ریلیز DEYROUTE را داشته باشد (کلید عمومی داخل خود `install.sh` است). اگر ابزار `minisign` نصب باشد از آن استفاده می‌شود، وگرنه از OpenSSL 3.

۴. آرشیو باید با خط خودش در `SHA256SUMS` یکی باشد.

۵. باینری جدید قبل از جایگزینی باید روی همین سرور اجرا شود. جایگزینی اتمیک است و باینری قبلی در `/var/lib/deyroute/bin/deyroute.prev` نگه داشته می‌شود.

فقط فایل‌های ریلیز DEYROUTE دانلود می‌شوند و هیچ چیزی به جایی فرستاده نمی‌شود. خطاها: `DEY-I004` (دانلود از همه منبع‌ها ناموفق: از `--mirror` یا `--local` استفاده کنید)، `DEY-I005` (checksum نمی‌خواند: فایل خراب یا دست‌کاری شده)، `DEY-I006` (امضا نامعتبر: فقط فایل‌های رسمی؛ `--skip-signature` فقط برای تست است).

## اجرای دوباره نصاب

اگر خط نصب را روی سروری بزنید که قبلاً راه‌اندازی شده، پیام `existing installation found: repairing/upgrading (config untouched)` را می‌بینید: باینری جایگزین می‌شود و بعد `deyroute setup --repair` اجرا می‌شود: پوشه‌ها و فایل‌های unit گم‌شده برمی‌گردند و `deyroute-hub` یا `deyroute-node` فعال و ری‌استارت می‌شود؛ گزینه‌های `join`، `--role` و `--name` نادیده گرفته می‌شوند. پروسه‌های تانل سرویس‌های systemd جدا هستند و قطع نمی‌شوند. از این روش برای تعمیر باینری خراب یا رفتن به نسخه دیگر (`--version V`) استفاده کنید.

برای راه‌اندازی از صفر، اول با `deyroute uninstall` حذف کنید؛ `deyroute setup` و `deyroute join` روی راه‌اندازی موجود چیزی نمی‌نویسند (`DEY-I013`).

## فایل‌ها و دسترسی‌ها

| مسیر | دسترسی / مالک | محتوا |
| --- | --- | --- |
| `/usr/local/bin/deyroute` + لینک `dey` | 0755 root | خود برنامه |
| `/etc/deyroute/` | 0710 root:deyroute | پوشه تنظیمات |
| `/etc/deyroute/config.yaml` | 0600 root | تنها فایل تنظیمات |
| `/etc/deyroute/secrets/` | 0700 root، فایل‌ها 0600 | CA، کلیدها، توکن‌ها، گواهی‌ها |
| `/etc/deyroute/backends/` | 0750 root:deyroute | کانفیگ‌هایی که از `config.yaml` ساخته می‌شوند (دستی ویرایش نکنید) |
| `/var/lib/deyroute/` | 0750 root:deyroute | `state.db` و بقیه وضعیت |
| `/var/lib/deyroute/bin/` | 0755 root | باینری بک‌اندها (هر نسخه یک پوشه) و `deyroute.prev` |
| `/var/lib/deyroute/backups/` | 0700 root | بکاپ‌ها و پوشه `auto/` |
| `/var/log/deyroute/` | 0750 root:deyroute | لاگ‌ها؛ در `tunnels/` برای هر تانل یک لاگ |
| `/run/deyroute/daemon.sock` | 0600 root | رابط محلی منو و CLI (فقط root می‌تواند از آن‌ها استفاده کند) |
| `/etc/sysctl.d/99-deyroute.conf` | | پروفایل کرنل؛ مقادیر قبلی در `/var/lib/deyroute/sysctl-before-deyroute.conf` |

نصاب کاربر سیستمی `deyroute` را هم می‌سازد (بدون امکان لاگین). بک‌اندهای تانل با همین کاربر اجرا می‌شوند؛ فقط ترنسپورت‌های WireGuard با root اجرا می‌شوند.

سرویس‌های systemd: `deyroute-hub.service` (روی Hub)، `deyroute-node.service` (روی Node) و `deyroute-tun@<tunnel>.<node>.<backend>-<transport>.service` (برای هر ترنسپورت گرم یکی).

## فایروال‌های بیرون از DEYROUTE

DEYROUTE فقط جدول nftables خودش (`inet deyroute`) را مدیریت می‌کند (صفحه [امنیت](security.md)). اگر سرورتان در پنل ارائه‌دهنده فایروال دارد یا از ufw/firewalld استفاده می‌کنید، این‌ها را باز کنید:

- روی Hub: پورت‌های تانل (مثلاً 443 و 2053) برای همه؛ پورت کنترل (44433/tcp) و بازه 30000 تا 31999 (tcp و udp) برای IP سرورهای Node؛
- روی Nodeها: بازه 30000 تا 31999 (tcp و udp) برای IP سرور Hub (برای ترنسپورت‌هایی که Hub به Node وصل می‌شود: `xray/reality`، `hysteria2/udp`، `wireguard/kernel`، `direct/*`).

`deyroute port check <port>` نشان می‌دهد که آیا یک فایروال بیرونی پورت تانل را بسته است و دستور دقیق باز کردنش را هم چاپ می‌کند؛ با `--open` همان دستور را deyroute بعد از تأیید شما اجرا می‌کند.

</div>
