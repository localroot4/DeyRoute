# اولین تانل

<div dir="rtl">

[English](../en/first-tunnel.md) · [فهرست](index.md)

تانل یک یا چند پورت Hub را به یک Node می‌رساند. کاربر به `HUB_IP:443` وصل می‌شود و بایت‌ها روی Node به `127.0.0.1:443` می‌رسند؛ همان‌جا سرویس VPN شما (Xray، Marzban، 3x-ui و…) دقیقاً مثل قبل جواب می‌دهد.

## قبل از شروع

- حداقل یک Node وصل شده باشد (`deyroute node list` آن را `online` نشان دهد).
- سرویس روی Node روی پورتی که می‌خواهید، روی `127.0.0.1` یا `0.0.0.0` گوش بدهد (تانل به‌طور پیش‌فرض به `127.0.0.1:<port>` تحویل می‌دهد).
- پورت روی Hub آزاد باشد. پورت 22، پورت کنترل (44433) و بازه 30000 تا 31999 رزرو هستند (`DEY-P011`). `deyroute port suggest` پورت‌های آزاد را نشان می‌دهد، اول پورت‌های سازگار با Cloudflare (443، 2053، 2083، 2087، 2096، 8443).

## با منو

`deyroute` را باز کنید و `2) Tunnels` ← `1) Add tunnel` را بزنید. در حالت Simple ویزارد حداکثر سه سؤال می‌پرسد:

۱. **Which node?** — فهرست شماره‌دار Nodeهای آنلاین. اگر فقط یک Node باشد خودکار انتخاب و اعلام می‌شود: `Only one node is online: de-1. It is used for this tunnel.`

۲. **Ports?** — مثلاً `443,2053`. هر پورت همان لحظه چک می‌شود: `443/tcp is free` یا `443/tcp is used by nginx`. برای پورت مشغول می‌توانید `Change port` (عوض کردن پورت)، `Skip this port` (رد کردن آن پورت) یا — فقط اگر یک تانل deyroute آن را گرفته باشد — `Stop that service` را انتخاب کنید. deyroute هیچ‌وقت برنامه‌های دیگر را متوقف نمی‌کند.

۳. **Confirm** — خلاصه (Node، پورت‌ها، نردبان، پشتیبان). Enter تانل را می‌سازد.

بعد صفحه پیشرفت می‌آید:

```text
  ✔ install backend on hub
  ✔ install on node
  ✔ render
  ✔ firewall
  ✔ start
  ✔ probe
  Tunnel main is UP via backhaul/wssmux (41ms)
```

اگر مرحله‌ای شکست بخورد، کد خطا با Why و Fix نشان داده می‌شود و می‌توانید `1) Retry` (تلاش دوباره) یا `0) Back` را بزنید.

حالت Advanced (`12) Settings` ← `1) UI mode`) سؤال‌های اختیاری اضافه می‌کند: نام تانل، مقصد دلخواه برای هر پورت، Node پشتیبان، ترتیب نردبان، حالت TLS و آستانه‌های Failover.

## با دستور

```bash
deyroute tunnel add --node de-1 --ports 443,2053 --name main
```

اگر در ترمینال باشید، اول خلاصه را نشان می‌دهد و می‌پرسد `Create the tunnel? [Y/n]`؛ با `--yes` بی‌سؤال می‌سازد. بعد همان مراحل منو را چاپ می‌کند.

| گزینه | معنی |
| --- | --- |
| `--node <id>` | Node اصلی (اجباری) |
| `--ports …` | پورت‌ها (اجباری)، جدول پایین را ببینید |
| `--name N` | نام نمایشی؛ **شناسه** تانل از همین ساخته می‌شود (`main`) |
| `--ladder default` | نام یک پروفایل نردبان، یا یک فهرست مستقیم مثل `backhaul/wssmux,rathole/noise` |
| `--backup <id>` | Node(های) پشتیبان؛ گزینه را تکرار کنید یا شناسه‌ها را با کاما جدا کنید |
| `--yes` | خلاصه را نشان نده و بپرس |

همه دستورهای بعدی با شناسه تانل کار می‌کنند (`deyroute tunnel show main`). اگر `--name` ندهید، شناسه `tunnel` می‌شود (و بعدی‌ها `tunnel-2` و…)؛ `deyroute tunnel list` شناسه‌ها را نشان می‌دهد.

مثال‌های بیشتر:

```bash
deyroute tunnel add --node de-1 --ports 443,27015/udp --name "Main" --backup nl-1 --yes
deyroute tunnel add --node de-1 --ports 443 --ladder backhaul/wssmux,rathole/noise
```

### نوشتن پورت‌ها

| ورودی | معنی |
| --- | --- |
| `443` | TCP 443 روی Hub ← `127.0.0.1:443` روی Node |
| `443/udp` | UDP 443 |
| `443,2053,8443` | چند پورت |
| `443/tcp,27015/udp` | TCP و UDP با هم |
| `2000-2010` | یک بازه (هر پورت یک نگاشت جدا؛ برای UDP: `2000-2010/udp`) |
| `443:8443` | روی 443 گوش بده، به `127.0.0.1:8443` روی Node تحویل بده |
| `443:10.0.0.5:8443` | تحویل به آدرس دیگری در سمت Node |

هر تانل حداکثر ۶۴ نگاشت پورت دارد (`DEY-C015`) و دو تانل نمی‌توانند یک پورت و پروتکل مشترک داشته باشند (`DEY-C003`).

## هنگام ساخت تانل چه اتفاقی می‌افتد

۱. پورت‌ها چک می‌شوند و یک بکاپ خودکار از `/etc/deyroute` گرفته می‌شود (`/var/lib/deyroute/backups/auto/`).

۲. تانل در `config.yaml` نوشته می‌شود.

۳. باینری بک‌اندها روی Hub و Node نصب می‌شود (اگر Hub به GitHub دسترسی نداشته باشد، از طریق Node دانلود می‌کند)، همه پله‌های نردبان ساخته و **گرم** می‌شوند (آماده ولی خاموش)، فایروال باز می‌شود و گواهی TLS مخصوص خود تانل ساخته می‌شود.

۴. پله اول روشن و پروب می‌شود؛ با اولین پروب موفق، تانل `UP` می‌شود.

اگر مرحله‌ای شکست بخورد، خطا نمایش داده می‌شود ولی تانل در تنظیمات می‌ماند و خودش به تلاش ادامه می‌دهد. علت را برطرف کنید و بزنید `deyroute tunnel restart main`.

تانلی که فقط پورت UDP دارد (مثلاً سرور بازی) نردبان مخصوص خودش را دارد (`udp-default`) و پله‌هایی که پروتکل تانل را پشتیبانی نمی‌کنند خودکار کنار گذاشته می‌شوند. صفحه [پرسش‌های فیلترینگ](faq-filtering.md) را ببینید.

## بررسی درست کار کردن

```bash
deyroute status
deyroute tunnel show main
deyroute diag probe main --all-ports
deyroute port check 443
```

- `status` همان داشبورد است: هر تانل با Node فعال، ترنسپورت، وضعیت (`UP`، `DEGR`، `SWITCHING`، `DOWN`، `DISABLED`، `PAUSED`)، تأخیر (RTT)، مدت روشن بودن و پورت‌ها؛ همه Nodeها؛ و آخرین رویدادها. `deyroute status --watch` هر ۲ ثانیه تازه می‌شود.
- `tunnel show` پورت‌ها، نردبان، تنظیمات Failover، اینکه IP کاربر `preserved` (حفظ‌شده) است یا `masked` (پنهان)، وضعیت همه پله‌ها (`active`، `warm`، `skipped: …`، `quarantined until …`) و نتیجه آخرین پروب‌ها را نشان می‌دهد.
- `diag probe` همین حالا پورت‌های تانل را پروب می‌کند.
- `port check` چک چهارمرحله‌ای را اجرا می‌کند:

```text
Port 443/tcp
  1. local bind              ✖ used by backhaul … (a deyroute unit)
  2. firewall                ✔ open (nftables)
  3. reachable from node     ✔ de-1: yes (39ms)
  4. reachable via tunnel    ✔ main: yes (41ms)
  Note: this shows the port is open from the internet; it does not measure filtering inside Iran.
```

برای پورتی که مال یک تانل در حال کار است، خط ۱ نشان می‌دهد که سرویس خود deyroute آن را گرفته؛ این طبیعی است. برای پورت جدید باید `free` باشد. خط ۳ را یک Node از بیرون ایران تست می‌کند؛ یعنی ثابت می‌کند پورت از اینترنت باز است، نه اینکه از داخل ایران در دسترس است.

در آخر با یک کلاینت واقعی تست کنید: در کانفیگ کلاینت فقط آدرس سرور را به IP سرور ایران عوض کنید. پورت، UUID یا رمز، SNI، host، path و fingerprint را دست نزنید — تانل به TLS دست نمی‌زند، پس کلاینت همان گواهی و تنظیمات Node را می‌بیند.

## لاگ و رویدادها

```bash
deyroute logs main -f           # لاگ زنده تانل، سمت Hub و Node ([hub] / [node])
deyroute logs hub --since 1h    # سرویس Hub
deyroute events --tunnel main   # سوییچ‌ها، خرابی‌ها، برگشت‌ها (۲۴ ساعت اخیر)
```

بدون `--since`، دستور `logs` ۲۰۰ خط آخر را نشان می‌دهد. رازها همیشه پوشانده می‌شوند. فایل‌ها در `/var/log/deyroute/` هستند (برای این تانل: `tunnels/main.log`). در منو: `6) Diagnostics` ← `4) Logs`.

## تغییر تانل در آینده

```bash
deyroute port add main 8443                           # افزودن پورت
deyroute port add main 8443/tcp --target 127.0.0.1:9443
deyroute port remove main 8443
deyroute tunnel edit main --name "Main 443"
deyroute tunnel edit main --probe-port 2053           # پورتی که پروب سلامت با آن کار می‌کند
deyroute tunnel restart main
deyroute tunnel disable main                          # ارسال را متوقف می‌کند (تأیید با yes)
deyroute tunnel enable main
deyroute tunnel delete main                           # تأیید با yes
```

افزودن یا حذف پورت، ترنسپورت فعال را ری‌استارت می‌کند (قطعی حداکثر ۳ ثانیه). حذف تانل، unitها، قوانین فایروال، رازها و سابقه پروب آن را پاک می‌کند؛ رویدادهایش می‌مانند.

قدم بعدی: [افزودن Node پشتیبان](backup-node.md). اگر چیزی کار نکرد: [عیب‌یابی](troubleshooting.md).

</div>
