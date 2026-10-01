# بکاپ و ریستور

<div dir="rtl">

[English](../en/backup.md) · [فهرست](index.md)

بکاپ یک فایل است که هر چیزی برای ساختن دوباره یک Hub (یا Node) لازم است در آن هست: تنظیمات، کلیدها و گواهی‌ها، و تاریخچه رویدادها. با آن می‌توانید یک اشتباه را برگردانید، سرور خراب را از نو بسازید یا [Hub را جابه‌جا کنید](hub-move.md) بدون اینکه Nodeها دوباره Join شوند.

## گرفتن بکاپ

```bash
deyroute backup
```

```text
Backup passphrase:
Repeat the passphrase:
Backup written: /var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age
Keep the passphrase safe: the backup cannot be restored without it.
```

در منو: `10) Backup & Restore` ← `1) Create backup`.

| گزینه | معنی |
| --- | --- |
| `--out FILE` | ذخیره در `FILE` به‌جای `/var/lib/deyroute/backups/deyroute-backup-<UTC>.tar.gz.age` |
| `--no-encrypt` | یک `tar.gz` بدون رمز — کلید CA و همه رازها داخلش است، آن را جای امن نگه دارید |

فایل با [age](https://age-encryption.org) و رمز عبور (passphrase) شما رمزنگاری و با دسترسی 0600 ذخیره می‌شود. **بدون رمز عبور هیچ راهی برای باز کردنش نیست.**

فایل را از سرور بیرون ببرید (مثلاً با `scp` روی کامپیوتر خودتان). بکاپی که فقط روی خود سرور است، با سرور از دست می‌رود؛ ضمناً `deyroute uninstall` پوشه `/var/lib/deyroute/backups` را پاک می‌کند مگر اینکه نگهش دارید.

### در اسکریپت

اگر ترمینال نباشد، رمز عبور از متغیر `DEYROUTE_BACKUP_PASSPHRASE` خوانده می‌شود:

```bash
DEYROUTE_BACKUP_PASSPHRASE='long secret phrase' deyroute backup --out /root/hub.tar.gz.age --json
```

بدون این متغیر و بدون `--no-encrypt`، دستور با `DEY-S008` می‌ایستد (رمز عبور بکاپ لازم است). مراقب باشید رمز در تاریخچه shell نماند، مثلاً آن را از فایلی بخوانید که فقط root به آن دسترسی دارد.

## داخل بکاپ چه هست

| داخلش هست | داخلش نیست |
| --- | --- |
| کل `/etc/deyroute`: `config.yaml`، پوشه `secrets/` (CA داخلی و کلیدش، کلید و گواهی Hub یا Node، توکن و گواهی TLS تانل‌ها، فایل توکن تلگرام در جای پیش‌فرضش)، کانفیگ‌های ساخته‌شده بک‌اندها، و `backends.yaml` اگر آن را عوض کرده باشید | `state.db`: وضعیت لحظه‌ای تانل‌ها، سابقه پروب، آمار |
| تاریخچه رویدادهای Hub (اگر سرویس Hub روشن باشد) | باینری بک‌اندها (`/var/lib/deyroute/bin`؛ دوباره دانلود می‌شوند) |
| `manifest.json`: نسخه deyroute، زمان ساخت، نقش، نام | لاگ‌ها (`/var/log/deyroute`) |
| | سرویس VPN شما روی Node (Xray، Marzban و…) و کاربرانش |

اگر سرویس Hub روشن نباشد، بکاپ باز هم نوشته می‌شود ولی بدون رویدادها (`The daemon is not running: the backup has no event history.`). Node تاریخچه رویداد ندارد؛ بکاپ آن تنظیمات و گواهی‌هایش را دارد.

## بکاپ خودکار

Hub قبل از هر تغییری که اعمال می‌کند — ساخت تانل، تغییر پورت، `deyroute config apply` یا `config edit` و… — یک بکاپ از `/etc/deyroute` در `/var/lib/deyroute/backups/auto/` می‌گیرد. ۲۰ تای آخر نگه داشته می‌شوند. این بکاپ‌ها **رمز ندارند** (پوشه فقط برای root قابل خواندن است) و رویدادها را ندارند.

برای برگرداندن یک تغییر اشتباه از آن‌ها استفاده کنید:

```bash
ls /var/lib/deyroute/backups/auto/
deyroute restore /var/lib/deyroute/backups/auto/deyroute-backup-20260930T115500.123456789Z.tar.gz
```

## ریستور

```bash
deyroute restore /root/deyroute-backup-20260930T120000Z.tar.gz.age
```

در منو: `10) Backup & Restore` ← `2) Restore from a backup`.

۱. فایل رمزگشایی (رمز یک بار پرسیده می‌شود یا از `DEYROUTE_BACKUP_PASSPHRASE` خوانده می‌شود) و بررسی می‌شود. اگر بکاپ معتبر نباشد، هیچ چیزی عوض نمی‌شود.

۲. `config.yaml` داخل بکاپ اعتبارسنجی می‌شود و اگر قدیمی باشد به شکل جدید تبدیل (migrate) می‌شود.

۳. اگر بکاپ مال Hub باشد و IP عمومی این سرور فرق داشته باشد، می‌پرسد آیا Hub به اینجا منتقل شده ([جابه‌جایی Hub](hub-move.md)).

۴. با تایپ `yes` تأیید می‌کنید:

```text
Restoring deyroute-backup-….tar.gz.age (hub ir-1, created 2026-09-30 12:00 with deyroute 1.0.0) replaces this server's /etc/deyroute: configuration, secrets and certificates. The current directory is kept as /etc/deyroute.pre-restore-<time>; every change made after that backup is lost. Tunnels are re-rendered and restarted.
```

۵. مراحل: `Restore backup`، `Save events for import`، `Apply kernel settings`، `Install and start service`. سرویس همه تانل‌ها را از روی تنظیمات بازیابی‌شده دوباره می‌سازد.

`/etc/deyroute` قبلی به اسم `/etc/deyroute.pre-restore-<time>` نگه داشته می‌شود؛ وقتی دیگر لازمش نداشتید پاکش کنید. در اسکریپت `--yes` بدهید (و `DEYROUTE_BACKUP_PASSPHRASE` را تنظیم کنید):

```bash
DEYROUTE_BACKUP_PASSPHRASE='long secret phrase' deyroute restore /root/hub.tar.gz.age --yes
```

بکاپ Node هم به همین شکل روی Node ریستور می‌شود و Node خودش دوباره به Hubش وصل می‌شود. ریستور روی سروری که با `install.sh --no-setup` نصب شده هم کار می‌کند.

## خطاها

| کد | معنی | چه کنیم |
| --- | --- | --- |
| `DEY-S004` | بکاپ رمزگشایی نشد | رمز عبور اشتباه است یا فایل خراب شده |
| `DEY-S005` | فایل بکاپ معتبر deyroute نیست | فایلی را بدهید که `deyroute backup` ساخته |
| `DEY-S008` | رمز عبور لازم است | در ترمینال تایپ کنید، `DEYROUTE_BACKUP_PASSPHRASE` بدهید یا `--no-encrypt` |
| `DEY-C006` / `DEY-C019` | تنظیمات قابل تبدیل نیست / نسخه schema ناشناخته | deyroute را آپدیت کنید (`deyroute update`) یا بکاپ دیگری را امتحان کنید |

## عادت‌های خوب

- بعد از افزودن Node یا تانل و قبل از هر آپدیت، بکاپ بگیرید.
- حداقل دو نسخه بیرون از سرور نگه دارید، و رمز عبور را جای دیگری غیر از خود فایل.
- یک بار ریستور را روی یک سرور یدکی امتحان کنید تا روز مبادا مطمئن باشید کار می‌کند.

</div>
