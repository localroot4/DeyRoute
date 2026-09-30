# Join کردن Node

<div dir="rtl">

[English](../en/join.md) · [فهرست](index.md)

Node همان سرور خارج است که سرویس VPN واقعی شما روی آن اجرا می‌شود. Join یعنی یک بار Node را به Hub معرفی کنیم؛ از آن به بعد Node یک اتصال رمزنگاری‌شده کنترل به Hub نگه می‌دارد و هر کاری Hub بخواهد انجام می‌دهد (نصب بک‌اند، روشن و خاموش کردن ترنسپورت، اجرای پروب). Hub هیچ‌وقت برای کنترل به Node وصل نمی‌شود و هیچ‌جا از SSH استفاده نمی‌شود.

## ۱. گرفتن Join command روی Hub

```bash
deyroute node join-command
```

یا در منو: `3) Nodes` ← `1) Show join command` (آنجا با `r` یک دستور تازه می‌گیرید). خروجی:

```text
Run this command on each node (valid until 12:15, 15m):

bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://TOKEN@5.6.7.8:44433#sha256:…' --version 1.0.0
```

- لینک داخل دستور این شکل را دارد: `dey://TOKEN@HUB_IP:CONTROL_PORT#CA_FINGERPRINT`. کل خط را کپی کنید و چیزی از آن را عوض نکنید.
- هر Join command **یک‌بارمصرف** است: هر دستور فقط یک Node را وصل می‌کند. برای هر Node یک دستور تازه بسازید (با وجود اینکه متن خروجی می‌گوید «on each node»).
- اعتبارش ۱۵ دقیقه است. برای زمان بیشتر از `--ttl` استفاده کنید، از `1m` تا `24h`: `deyroute node join-command --ttl 1h`.
- `--version` باعث می‌شود Node همان نسخه‌ای را نصب کند که روی Hub است.
- اگر Hub با `DEYROUTE_MIRROR` راه‌اندازی شده باشد، دستور نصاب را از همان Mirror دانلود می‌کند.

## ۲. اجرای آن روی Node

روی سرور خارج با کاربر root خط را paste کنید. اگر می‌خواهید شناسه Node را خودتان انتخاب کنید، `--name` را به آخر خط اضافه کنید:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) join 'dey://TOKEN@5.6.7.8:44433#sha256:…' --version 1.0.0 --name de-1
```

اگر `deyroute` از قبل روی Node نصب است، شکل کوتاه هم کار می‌کند:

```bash
deyroute join 'dey://TOKEN@5.6.7.8:44433#sha256:…' --name de-1
```

Node فقط یک سؤال می‌پرسد — اینکه پروفایل کرنل balanced اعمال شود یا نه (Enter یعنی بله) — و بعد مراحل را نشان می‌دهد:

```text
  ✔ Read join link
  ✔ Create node key
  ✔ Join hub
  ✔ Save certificates
  ✔ Apply kernel settings
  ✔ Write /etc/deyroute/config.yaml
  ✔ Install and start service

Node de-1 joined hub ir-1; it appears on the hub dashboard shortly
```

شناسه Node از `--name` ساخته می‌شود (حروف کوچک انگلیسی، عدد و `-`، بین ۲ تا ۳۲ حرف، مثل `de-1`) و اگر `--name` ندهید، از نام میزبان (hostname). شناسه بعداً عوض نمی‌شود، ولی نام نمایشی عوض می‌شود: `deyroute node rename de-1 "Germany 1"`.

## ۳. بررسی روی Hub

```bash
deyroute node list
deyroute node test de-1
```

`node list` همه Nodeها را با وضعیت، تأخیر کانال کنترل، نسخه و مصرف منابع نشان می‌دهد. `node test` تأخیر کنترل را اندازه می‌گیرد، UDP بین Hub و Node را تست می‌کند (`UDP ok` یا `blocked`؛ اگر بسته باشد ترنسپورت‌هایی که UDP لازم دارند برای این Node کنار گذاشته می‌شوند) و اطلاعات سیستم را چاپ می‌کند. در داشبورد (`deyroute status`، یا `1) Dashboard (live)` در منو) هم Node زیر NODES دیده می‌شود.

قدم بعدی: [اولین تانل](first-tunnel.md).

## هنگام Join چه اتفاقی می‌افتد

۱. Node جفت‌کلید خودش (Ed25519) را می‌سازد و درخواست گواهی را همراه توکن به پورت کنترل Hub می‌فرستد.

۲. Node گواهی CA سرور Hub را با اثرانگشتی (fingerprint) که داخل لینک است مقایسه می‌کند و به هیچ چیز دیگری اعتماد نمی‌کند. اگر فرق داشته باشد، Join با `DEY-N002` متوقف می‌شود.

۳. Hub توکن را بررسی می‌کند (۳۲ بایت تصادفی، یک‌بارمصرف، که بلافاصله بعد از مصرف پاک می‌شود)، با CA داخلی خودش برای Node یک گواهی ۱۰ ساله صادر می‌کند و IP سرور Node را به مجموعه `@nodes` فایروال اضافه می‌کند.

۴. Node گواهی و `config.yaml` خودش را می‌نویسد، `deyroute-node.service` را روشن می‌کند و یک اتصال خروجی دائمی به Hub نگه می‌دارد (اگر قطع شود، با فاصله ۱ تا ۳۰ ثانیه دوباره وصل می‌شود). هر ۵ ثانیه heartbeat می‌فرستد و Hub اگر ۱۵ ثانیه چیزی نگیرد، Node را offline حساب می‌کند.

پورت کنترل در حالت عادی فقط Nodeهای شناخته‌شده را می‌پذیرد. تا وقتی یک توکن Join مصرف‌نشده وجود دارد، برای همه باز است تا Node جدید بتواند به آن برسد. اگر از یک IP در یک ساعت بیش از ۵ تلاش ناموفق برای Join شود، آن IP یک ساعت بلاک می‌شود (`DEY-N007`).

## وقتی IP سرور Node عوض می‌شود

لازم نیست کاری بکنید. Node با گواهی خودش از IP جدید دوباره وصل می‌شود؛ Hub آن را می‌پذیرد، IP جدید را در `@nodes` می‌گذارد و رویداد `node_ip_changed` را ثبت می‌کند (`deyroute events`).

اگر آدرس **Hub** عوض شود، باید به Nodeها خبر داد: صفحه [جابه‌جایی Hub](hub-move.md).

## نسخه ناسازگار

Hub و Node باید نسخه major.minor یکسان داشته باشند (مثلاً 1.0.x با 1.0.x). Nodeی که نسخه دیگری دارد وصل می‌شود، در `deyroute node list` با `(incompatible)` نشان داده می‌شود و تا آپدیت نشود هیچ دستوری اجرا نمی‌کند (`DEY-N004`). Join command خودش نسخه Hub را تعیین می‌کند، پس این وضعیت معمولاً فقط بعد از آپدیت یک طرف پیش می‌آید. برای رفعش هر دو را به یک نسخه برسانید:

- روی Hub: `deyroute update` (Nodeها دنبال Hub می‌آیند)، یا
- روی Node: نصاب را با نسخه Hub دوباره بزنید (ارتقا در جا، تنظیمات دست نمی‌خورد):

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --version 1.0.0
```

`deyroute version` نسخه هر سرور را چاپ می‌کند.

## حذف یا Join دوباره Node

```bash
deyroute node remove nl-1
```

این دستور همه ترنسپورت‌های تانل روی آن Node را متوقف می‌کند (تانل‌هایی که فقط همین Node را دارند DOWN می‌شوند)، گواهی Node را باطل می‌کند و IP آن را از فایروال برمی‌دارد. برای تأیید باید `yes` تایپ کنید (در اسکریپت: `--yes`). خود سرور Node پاک نمی‌شود: برای حذف deyroute از آن، روی خود Node بزنید `deyroute uninstall`.

برای Join دوباره یک سرور (مثلاً به یک Hub دیگر)، اول روی آن `deyroute uninstall` بزنید و بعد یک Join command تازه. اگر روی Hub از قبل Nodeی با همین شناسه باشد، یا اول آن را روی Hub حذف کنید یا با `--name` شناسه دیگری بدهید (`DEY-N010`).

## خطاهای Join

| کد | معنی | چه کنیم |
| --- | --- | --- |
| `DEY-N001` | توکن نامعتبر یا منقضی | دستور تازه بسازید: `deyroute node join-command` |
| `DEY-N002` | اثرانگشت CA سرور Hub نمی‌خواند | دستور را دوباره از Hub کپی کنید، ویرایشش نکنید، آدرس را چک کنید |
| `DEY-N006` | لینک ناقص است | کل خط را دوباره کپی کنید |
| `DEY-N007` | تلاش ناموفق زیاد از این IP | یک ساعت صبر کنید و با دستور تازه امتحان کنید |
| `DEY-N009` | Hub در دسترس نیست | روشن بودن Hub و دسترسی Node به `HUB_IP:44433` را چک کنید (فایروال پنل سرور) |
| `DEY-N010` | Nodeی با این شناسه هست | `--name` با شناسه دیگر، یا `deyroute node remove <id>` روی Hub |
| `DEY-N020` | جواب Hub قابل استفاده نیست | هر دو سرور هم‌نسخه شوند، بعد دستور Join تازه |
| `DEY-I013` | این سرور قبلاً راه‌اندازی شده | اول `deyroute uninstall` |

کدهای بیشتر: [عیب‌یابی](troubleshooting.md) و [ERRORS.md](../ERRORS.md).

</div>
