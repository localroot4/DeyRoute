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

۲. **Ports?** — مثلاً `443,2053`. هر پورت همان لحظه چک می‌شود: `443/tcp is free` یا `443/tcp is used by nginx`. برای پورت مشغول می‌توانید `Change port` (عوض کردن پورت)، `Skip` (رد کردن آن پورت) یا — فقط اگر یک تانل deyroute آن را گرفته باشد — `Stop that service` را انتخاب کنید. deyroute هیچ‌وقت برنامه‌های دیگر را متوقف نمی‌کند. ویزارد روی پورت آزادی هم که فایروال سرور (ufw، firewalld، iptables یا جدول nftables دیگری) آن را بسته، یا Node نمی‌تواند به آن برسد، می‌ایستد: `Change port`، `Skip`، `Open it in the firewall: ufw allow 443/tcp` (باز کردن در فایروال؛ فقط وقتی deyroute دستورش را می‌داند) یا `Keep it and continue` (نگه داشتن و ادامه). باز کردن، دستور یا دستورهای دقیق را نشان می‌دهد و فقط بعد از اینکه `yes` را تایپ کنید روی Hub اجرا می‌کند؛ بعد پورت دوباره چک می‌شود. پورتی که Node به آن نمی‌رسد معمولاً در فایروال پنل ارائه‌دهنده سرور بسته است و deyroute نمی‌تواند آن را تغییر دهد.

۳. **Confirm** — خلاصه، و Enter تانل را می‌سازد:

```text
 3. Confirm
  Node    de-1 (Germany 1)
  Ports   443/tcp, 2053/tcp
  Ladder  default ladder
  Backup  none
```

بعد صفحه پیشرفت می‌آید (جزئیات کوتاه شده‌اند):

```text
 Add tunnel

  install backend on hub ✔  backhaul v0.7.2, rathole v0.5.0, …
  install on node ✔  de-1
  render ✔  8 rungs on the hub, 8 on nodes
  firewall ✔  tcp 443, 2053
  start ✔  backhaul/wssmux on de-1
  probe ✔  41ms
  Tunnel main is UP via backhaul/wssmux (41ms) ✔

 Tunnel main is UP via backhaul/wssmux (41ms)

 Press Enter or q to go back.
```

خط فرمان همین مراحل را با علامت در ابتدای خط چاپ می‌کند (`  ✔ install backend on hub  backhaul v0.7.2, …`).

اگر مرحله‌ای شکست بخورد، کد خطا با Why و Fix نشان داده می‌شود و می‌توانید `1) Retry` (تلاش دوباره) یا `0) Back` را بزنید.

حالت Advanced (`12) Settings` ← `1) UI mode`) سؤال‌های اختیاری اضافه می‌کند: نام تانل، مقصد دلخواه برای هر پورت، نوع پروب هر پورت TCP (بخش «پروب سلامت پورت را چطور می‌آزماید» در پایین)، Node پشتیبان، ترتیب نردبان، حالت TLS و آستانه‌های Failover. سؤال‌هایی که جواب‌های ثابت دارند (Node پشتیبان، policy، حالت TLS) فهرست شماره‌دار هستند: شماره را بزنید، یا Enter برای گزینه داخل کروشه. انتخاب Node پشتیبان هشدار ثابت `Backup only works if the same service runs on both nodes.` را نشان می‌دهد و صفحه پیشرفت با `backup nl-1 ready (warm)` تمام می‌شود.

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

هر تانل حداکثر ۶۴ نگاشت پورت دارد (`DEY-C015` و `DEY-P016`). هر پورتِ یک بازه یک نگاشت جدا حساب می‌شود؛ پس `2000-2100` (۱۰۱ پورت) در یک تانل جا نمی‌شود و باید آن را بین دو تانل تقسیم کنید، مثلاً `2000-2063` و `2064-2100`. دو تانل نمی‌توانند یک پورت و پروتکل مشترک داشته باشند (`DEY-C003`).

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

اگر خط ۲ بگوید `closed (ufw); open it with: ufw allow 443/tcp`، با `deyroute port check 443 --open` همان دستور دقیق را deyroute اجرا می‌کند: دستور را نشان می‌دهد، می‌خواهد `yes` را تایپ کنید (`--yes` سؤال را حذف می‌کند؛ بدون ترمینال و بدون `--yes` با کد خروج ۳ می‌ایستد) و بعد فایروال را دوباره چک می‌کند. اگر فایروال دومی هنوز پورت را ببندد، دستور آن هم به همین شکل نشان داده و تأیید می‌شود. در منو: `4) Ports` ← `Check port` و بعد `1) Open it in the firewall`.

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
deyroute port add main 8080 --probe http              # همراه با نوع پروب (پایین‌تر)
deyroute port set main 443 --probe tls                # عوض کردن نوع پروب
deyroute port remove main 8443
deyroute tunnel edit main --name "Main 443"
deyroute tunnel edit main --probe-port 2053           # پورتی که پروب سلامت با آن کار می‌کند
deyroute tunnel restart main
deyroute tunnel disable main                          # ارسال را متوقف می‌کند (تأیید با yes)
deyroute tunnel enable main
deyroute tunnel delete main                           # تأیید با yes
```

افزودن یا حذف پورت، ترنسپورت فعال را ری‌استارت می‌کند (قطعی حداکثر ۳ ثانیه)؛ عوض کردن نوع پروب چیزی را ری‌استارت نمی‌کند. حذف تانل، unitها، قوانین فایروال، رازها و سابقه پروب آن را پاک می‌کند؛ رویدادهایش می‌مانند.

### پروب سلامت پورت را چطور می‌آزماید

پروب سلامت هر چند ثانیه اولین پورت TCP تانل را می‌آزماید (یا پورتی که با `tunnel edit --probe-port` تعیین شده)؛ `diag probe --all-ports` و گزارش هر ۶۰ ثانیه همه پورت‌ها را می‌آزمایند. هر نگاشت پورت TCP یک نوع پروب دارد (Advanced):

| نوع | پروب وقتی موفق است که … |
| --- | --- |
| `auto` (پیش‌فرض) | به یک TLS hello هر پاسخی برگردد؛ TLS باشد یا نه، همین که سرویس جواب بدهد |
| `tcp` | اتصال TCP باز شود |
| `tls` | دست‌دهی TLS کامل شود یا یک TLS alert برگردد |
| `http` | `HEAD /` یک خط وضعیت HTTP برگرداند |

`auto` برای تقریباً همه سرویس‌های VPN مناسب است. `tcp` را برای سرویسی بگذارید که با گرفتن TLS hello ساکت می‌ماند یا اتصال را می‌بندد (بعضی پراکسی‌ها این‌طورند)، و `tls` یا `http` را وقتی که می‌خواهید مطمئن شوید همان نوع سرویس جواب می‌دهد. نگاشت‌های UDP همیشه `auto` هستند (از روی unitهای ترنسپورت بررسی می‌شوند).

تنظیم با `deyroute port add … --probe <kind>` یا `deyroute port set <tunnel> <port> --probe <kind>`؛ در منو و در حالت Advanced: `4) Ports` ← `Probe kind *` (و سؤال نوع پروب در `Add port to tunnel` و در ویزارد Add tunnel). مقدارها مثل `ports[].probe` در `config.yaml` بررسی می‌شوند (`DEY-C013`).

قدم بعدی: [افزودن Node پشتیبان](backup-node.md). اگر چیزی کار نکرد: [عیب‌یابی](troubleshooting.md).

</div>
