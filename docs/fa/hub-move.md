# جابه‌جایی Hub

<div dir="rtl">

[English](../en/hub-move.md) · [فهرست](index.md)

وقتی سرور یا ارائه‌دهنده را عوض می‌کنید، یا IP سرور ایران بلاک شده و IP جدید گرفته‌اید، Hub را جابه‌جا کنید. Nodeها **نیازی** به Join دوباره ندارند: CA داخلی Hub داخل بکاپ است، پس Nodeها به Hub بازیابی‌شده اعتماد می‌کنند و فقط باید آدرس جدیدش را بدانند.

## بردن Hub به سرور جدید

**۱. روی Hub قدیم — بکاپ بگیرید.**

```bash
deyroute backup
```

دو بار رمز عبور را وارد کنید و اسم فایلی را که چاپ می‌شود یادداشت کنید، مثلاً `/var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age` ([بکاپ و ریستور](backup.md)). فایل را به سرور جدید ببرید:

```bash
scp /var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age root@NEW_IP:/root/
```

**۲. روی سرور جدید — فقط نصب، بدون راه‌اندازی.**

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --no-setup
```

(اگر آنجا GitHub بسته است از `--mirror` یا `--local` استفاده کنید؛ صفحه [نصب](install.md).)

**۳. روی سرور جدید — ریستور.**

```bash
deyroute restore /root/deyroute-backup-20260930T120000Z.tar.gz.age
```

رمز عبور را می‌پرسد، بکاپ را بررسی می‌کند و متوجه می‌شود که IP عمومی این سرور فرق دارد:

```text
This server's public IP is 7.7.7.7, but the backup's hub address is 5.6.7.8. Use this server's address (the hub moved here)?
   Type y (yes) or n (no) and press Enter. Enter alone = y (yes).
```

جواب بله بدهید. بعد فهرست چیزهایی که جایگزین می‌شوند و عوض شدن آدرس Hub نشان داده می‌شود — `yes` تایپ کنید. گواهی Hub با همان CA برای آدرس جدید دوباره صادر می‌شود، سرویس روشن می‌شود و همه تانل‌ها را دوباره می‌سازد. آخر خروجی قدم بعدی را می‌گوید:

```text
Hub ir-1 restored.
If the hub moved to this server, tell the nodes the new address:
  on the old hub:   deyroute hub announce-move 7.7.7.7:44433
  or on each node:  deyroute node set-hub 7.7.7.7:44433
```

در منو: `10) Backup & Restore` ← `2) Restore from a backup` همین سؤال را می‌پرسد (`Use this server's address (the hub moved here)? (y/n) [y]`) و نتیجه‌اش گزینه‌های منوی قدم ۴ را نام می‌برد.

سرور جدید پشت NAT است یا آدرسش پیدا نمی‌شود (مثلاً یک آدرس خصوصی مثل `10.0.0.5`)؟ آن‌وقت ریستور نمی‌تواند آدرسی را که Nodeها باید به آن وصل شوند ببیند. در ترمینال (و در منو) آن را می‌پرسد و آدرس داخل بکاپ پیش‌فرض است. در اسکریپت آن را بدهید:

```bash
DEYROUTE_BACKUP_PASSPHRASE='…' deyroute restore /root/deyroute-backup-20260930T120000Z.tar.gz.age --yes --public-ip 7.7.7.7
```

برای آدرس IPv6 هم `--public-ip6` را اضافه کنید؛ بدون آن `hub.public_ip6` داخل بکاپ پاک می‌شود. بدون `--public-ip`، ریستور با `--yes` (یا بدون ترمینال) با `DEY-I025` متوقف می‌شود و چیزی را عوض نمی‌کند. `--keep-address` به‌جای آن آدرس داخل بکاپ را نگه می‌دارد (فقط وقتی این سرور واقعاً صاحب آن آدرس است، مثلاً یک IP شناور که همراهش جابه‌جا شده). قدم بعدی که آخر خروجی چاپ می‌شود همیشه آدرسی را نام می‌برد که واقعاً در کار است.

**۴. روی Hub قدیم — به Nodeها خبر بدهید.** تا وقتی Hub قدیم روشن است و Nodeها هنوز به آن وصل‌اند:

```bash
deyroute hub announce-move 7.7.7.7:44433
```

```text
New hub address 7.7.7.7:44433 sent to: de-1, nl-1
```

در منو: `10) Backup & Restore` ← `3) Announce hub move`، آدرس `7.7.7.7:44433` را تایپ کنید و برای تأیید Enter بزنید.

هر Node آنلاین آدرس جدید را ذخیره می‌کند و به Hub جدید وصل می‌شود. Nodeهایی که آفلاین بودند فهرست می‌شوند (`Offline, not told: …`)؛ روی هرکدام بزنید:

```bash
deyroute node set-hub 7.7.7.7:44433
```

یا در منوی همان Node: `10) Backup & Restore` ← `3) Set hub address`. همیشه از `hub.public_ip` سرور Hub جدید استفاده کنید؛ اگر IPv4 است، هیچ‌وقت آدرس IPv6 سرور Hub را ندهید ([چرا](#nodeها-و-پورت-کنترل)).

**۵. روی Hub جدید — بررسی.**

```bash
deyroute node list
deyroute status
```

**۶. IP جدید را به کاربران بدهید.** کانفیگ کاربران به آدرس Hub اشاره می‌کند. اگر از دامنه استفاده می‌کنند، رکورد DNS آن را به IP جدید عوض کنید.

**۷. Hub قدیم را کنار بگذارید.** وقتی Hub جدید کار کرد:

```bash
deyroute uninstall
```

به سؤال `Also uninstall deyroute from every online node?` جواب **نه** بدهید و اینجا هیچ‌وقت از `--nodes` استفاده نکنید — Nodeها حالا مال Hub جدید هستند.

### اگر Hub قدیم دیگر در دسترس نیست

قدم ۴ را رد کنید و روی همه Nodeها بزنید `deyroute node set-hub NEW_IP:44433` (در منو: `10) Backup & Restore` ← `3) Set hub address`). این دستور حتی وقتی سرویس Node خاموش است هم کار می‌کند (آدرس در `config.yaml` خود Node نوشته می‌شود و در روشن شدن بعدی استفاده می‌شود). ولی بکاپ Hub قدیم را لازم دارید؛ اگر ندارید، بخش «بکاپ ندارم» پایین را ببینید.

## سرور Hub همان است ولی IP آن عوض شده

۱. آدرس جدید را در تنظیمات Hub بگذارید:

```bash
deyroute config edit
```

مقدار `hub.public_ip` زیر `hub:` را عوض و ذخیره کنید؛ فایل بررسی و اعمال می‌شود.

۲. Nodeها هنوز سراغ آدرس قدیم می‌روند. روی هر Node:

```bash
deyroute node set-hub NEW_IP:44433
```

(در منو: `10) Backup & Restore` ← `3) Set hub address`). `NEW_IP` همان آدرسی است که در `hub.public_ip` گذاشتید، نه آدرس دیگری از سرور Hub.

۳. با `deyroute node list` بررسی کنید و بعد IP جدید را به کاربران بدهید.

## Nodeها و پورت کنترل

آدرسی که اعلام می‌کنید `IP:CONTROL_PORT` مربوط به Hub جدید است. تنظیمات ریستورشده همان پورت کنترل Hub قدیم را نگه می‌دارد (پیش‌فرض 44433)؛ مطمئن شوید روی سرور جدید آزاد است و در فایروال پنل آن سرور هم باز است.

برای `announce-move` و `set-hub` از IPی استفاده کنید که در `hub.public_ip` سرور Hub جدید است. اگر IPv4 است، هیچ‌وقت آدرس IPv6 سرور Hub را به Nodeها ندهید: IPv6 بین Hub و Node پشتیبانی نمی‌شود و در این حالت فایروال Hub ترنسپورت‌هایی را که در آن‌ها Node به Hub وصل می‌شود دور می‌اندازد. صفحه [Join](join.md#node-را-از-طریق-ipv6-وصل-نکنید) را ببینید.

## نسخه‌ها بعد از جابه‌جایی

Hub جدید همان نسخه‌ای را اجرا می‌کند که رویش نصب کرده‌اید و ممکن است با نسخه بکاپ فرق داشته باشد. ریستور نسخه بکاپ را یادداشت می‌کند؛ Hub در اولین بالا آمدن به همه Nodeها می‌گوید وقتی وصل شدند نسخه Hub را نصب کنند. Nodeی با major.minor دیگر (ناسازگار، `DEY-N004`) در هر حال همان لحظه که وصل می‌شود نسخه Hub را می‌گیرد. تا وقتی آن را اجرا نکند، Node ناسازگار هیچ دستوری اجرا نمی‌کند. برای این کار `deyroute update` لازم نیست؛ با `deyroute node list` (ستون `VERSION`) دنبالش کنید. Nodeی که از Hub جدید جدیدتر است به نسخه Hub برمی‌گردد. صفحه [Nodeها چطور دنبال می‌کنند](update-uninstall.md#nodeها-چطور-دنبال-می‌کنند) را ببینید.

## بکاپ ندارم: Join دوباره Nodeها

بدون بکاپ، CA قبلی از دست رفته و Nodeها نمی‌توانند به Hub جدید اعتماد کنند. از اول شروع کنید:

۱. Hub جدید را معمولی راه‌اندازی کنید ([نصب](install.md)).

۲. روی هر Node، deyroute را حذف کنید و به Hub جدید Join کنید:

```bash
deyroute uninstall --yes
```

بعد یک Join command تازه از Hub جدید بزنید (`deyroute node join-command`، برای هر Node یکی).

۳. تانل‌ها را دوباره بسازید ([اولین تانل](first-tunnel.md)).

`deyroute uninstall` روی Node فقط DEYROUTE را پاک می‌کند؛ به سرویس VPN شما روی Node دست نمی‌زند.

از این به بعد بعد از هر تغییر بکاپ بگیرید — با بکاپ، جابه‌جایی Hub یک کار پنج‌دقیقه‌ای است.

</div>
