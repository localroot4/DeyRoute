# آپدیت و حذف

<div dir="rtl">

[English](../en/update-uninstall.md) · [فهرست](index.md)

Hub خودش deyroute را به‌روز نگه می‌دارد (آپدیت خودکار، به‌صورت پیش‌فرض روشن؛ پایین‌تر را ببینید) و شما هم هر وقت بخواهید می‌توانید دستی آپدیت کنید. آپدیت deyroute تانل‌ها را قطع نمی‌کند: برنامه‌های تانل سرویس‌های systemd جدا هستند و وقتی سرویس deyroute ری‌استارت می‌شود دست نمی‌خورند.

## آپدیت deyroute

روی Hub بزنید:

```bash
deyroute update --check            # نسخه جدید هست؟ تغییرات را نشان می‌دهد
deyroute update                    # تغییرات، تأیید، دانلود، بررسی، جایگزینی، ری‌استارت
deyroute update --version 1.2.0 --yes
deyroute update --rollback         # برگشت به باینری قبلی
```

در منو: `11) Update` ← `1) Check for updates`، `2) Apply update`، `5) Roll back`.

کارهایی که `deyroute update` انجام می‌دهد:

۱. بررسی ریلیز جدید و نمایش تغییراتش: بخش‌های `CHANGELOG.md` همان ریلیز که بعد از نسخه شما آمده‌اند (حداکثر ۴۰ خط و بعد آدرس صفحه ریلیز). این فایل مثل خود ریلیز (از طریق Node یا Mirror شما) دانلود و با `SHA256SUMS` امضاشده بررسی می‌شود؛ ریلیزی که این فایل را ندارد فقط آدرس صفحه ریلیز را نشان می‌دهد؛

۲. گرفتن تأیید (`--yes` از آن رد می‌شود؛ بدون ترمینال `--yes` اجباری است)؛

۳. دانلود و بررسی checksum و امضا — اگر Hub به GitHub نرسد از طریق یک Node (اگر هیچ Node آنلاینی نباشد `DEY-N012`) یا از Mirror شما؛

۴. جایگزینی `/usr/local/bin/deyroute`، نگه داشتن نسخه قبلی در `/var/lib/deyroute/bin/deyroute.prev` و ری‌استارت سرویس deyroute؛

۵. Nodeها نسخه Hub را دنبال می‌کنند.

در هر بار فقط یکی از `--check`، `--version` و `--rollback` را بدهید. `--rollback` فایل `deyroute.prev` را برمی‌گرداند (اگر نباشد `DEY-S007`؛ آن‌وقت با `--version V` یک نسخه مشخص نصب کنید). این دستور وقتی سرویس دیگر بالا نمی‌آید (مثلاً یک آپدیت خراب که مدام کرش می‌کند) و روی Node هم کار می‌کند: باینری‌ها همان‌جا جابه‌جا می‌شوند و سرویس ری‌استارت می‌شود.

## آپدیت خودکار

به‌صورت پیش‌فرض روشن است. هر روز ساعت **۴ صبح به وقت سرور** Hub جدیدترین نسخه‌ای را نصب می‌کند که حداقل **۲۴ ساعت** از انتشارش گذشته باشد؛ Nodeها مثل `deyroute update` دنبالش می‌آیند و تانل‌ها قطع نمی‌شوند.

بعد از ری‌استارت، Hub بررسی می‌کند که هر تانلی که قبل از آپدیت UP بود و هر Nodeی که آنلاین بود دوباره کار کند (تا ۱۰ دقیقه فرصت می‌دهد). اگر نشد — یا نسخه جدید اصلاً بالا نیامد — خودش نسخه قبلی را برمی‌گرداند، یک رویداد `DEY-S003` و یک خط در داشبورد ثبت می‌کند و آن نسخه را دیگر هیچ‌وقت خودکار نصب نمی‌کند (با `deyroute update` خودتان هنوز می‌توانید).

```bash
deyroute update auto        # روشن یا خاموش، نسخه بعدی و زمانش، نتیجه آخرین آپدیت
deyroute update auto off    # خاموش کردن
deyroute update auto on     # روشن کردن دوباره
```

```text
── AUTOMATIC UPDATE ─────────────────────────────────────────
  Status     on
  When       every day at 04:00 server time; only releases out for 24 hours
  Running    0.3.0-edge.40
  Next       0.3.0-edge.41 on 2026-10-10 04:00
  Last       updated 0.3.0-edge.39 → 0.3.0-edge.40; 1 tunnels and 1 nodes work (2026-10-09 04:03)
```

تا وقتی روشن است، `deyroute status` نسخه جدید را جزو «نیاز به توجه» نشان نمی‌دهد، چون سر وقتش نصب می‌شود. اگر خودتان `deyroute update --rollback` بزنید، آپدیت خودکار هم نسخه‌ای را که کنار گذاشتید دیگر نصب نمی‌کند.

**راه جایگزین:** اجرای دوباره نصاب هم در جا ارتقا می‌دهد و تنظیمات را نگه می‌دارد. حتی وقتی سرویس خاموش است و روی Node هم کار می‌کند:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh) --version 1.2.0
```

Hub و Nodeها باید نسخه major.minor یکسان داشته باشند (صفحه [Join](join.md)، بخش «نسخه ناسازگار»).

## آپدیت بک‌اندها

برنامه‌های تانل (Backhaul، Rathole، FRP، Xray، Hysteria2، Waterwall و…) روی نسخه‌های تست‌شده‌ای که در یک manifest امضاشده آمده ثابت شده‌اند. برای آپدیتشان:

```bash
deyroute update backends                 # همه بک‌اندها
deyroute update backends backhaul --yes  # یک بک‌اند
```

در منو: `11) Update` ← `3) Update backends`.

نسخه جدید کنار نسخه قبلی روی Hub و همه Nodeها نصب می‌شود، کانفیگ‌ها دوباره ساخته می‌شوند و ترنسپورت فعالی که بک‌اندش عوض شده ری‌استارت می‌شود. اگر پروبش ظرف ۶۰ ثانیه سبز نشود، خودکار به نسخه قبلی برمی‌گردد (رویداد `backend_update_rolled_back`، کد `DEY-S003`). اگر در این فاصله Failover تانل را به پله یا Node دیگری برده باشد، rollback آن را به همان پله‌ای برمی‌گرداند که قبل از آپدیت داشت. نتیجه یک جدول است:

```text
BACKEND   FROM     TO       RESULT      DETAIL
backhaul  <old>    <new>    updated
rathole   <same>   <same>   unchanged
```

(ستون `RESULT` یکی از `updated`، `rolled_back`، `unchanged` یا `failed` است.)

نسخه ثابت‌شده هر بک‌اند و مستندات اصلی‌اش در صفحه همان بک‌اند آمده: [backhaul](../backends/backhaul.md)، [rathole](../backends/rathole.md)، [frp](../backends/frp.md)، [xray](../backends/xray.md)، [hysteria2](../backends/hysteria2.md)، [waterwall](../backends/waterwall.md)، [wireguard](../backends/wireguard.md)، [direct](../backends/direct.md)، [gost](../backends/gost.md)، [chisel](../backends/chisel.md).

## آپدیت manifest بک‌اندها

```bash
deyroute update manifest
```

در منو: `11) Update` ← `4) Backend manifest`. آخرین manifest امضاشده (نسخه‌ها، آدرس دانلود و sha256 هر بک‌اند) را می‌گیرد و نسخه‌ها را چاپ می‌کند. باینری هیچ بک‌اندی بدون sha256 مطابق نصب نمی‌شود: `DEY-S001` یعنی یک فایل مطابقت نداشت (رد شد و هیچ چیزی عوض نشد) و `DEY-S006` یعنی manifest برای یک بک‌اند checksum ندارد. manifest جدید در `/etc/deyroute/backends.yaml` ذخیره می‌شود (و جای manifest داخل برنامه را می‌گیرد). تانل‌های در حال کار تا وقتی `deyroute update backends` را نزنید با همان نسخه قبلی بک‌اندها کار می‌کنند.

## حذف کامل

```bash
deyroute uninstall
```

در منو: `12) Settings` ← `3) Uninstall`. این سؤال‌ها را می‌پرسد:

```text
Keep the backups in /var/lib/deyroute/backups?
   Type y (yes) or n (no) and press Enter. Enter alone = y (yes).
Also uninstall deyroute from every online node?     (فقط روی Hub)
   Type y (yes) or n (no) and press Enter. Enter alone = n (no).
```

بعد دقیقاً می‌گوید چه چیزهایی پاک می‌شوند و شما `yes` تایپ می‌کنید:

```text
Uninstall removes from this server:
  - every deyroute tunnel unit and the deyroute service (stopped and disabled)
  - the nftables table inet deyroute
  - the kernel settings of deyroute (restored from sysctl-before-deyroute.conf)
  - /etc/deyroute: configuration, secrets and certificates
  - /var/lib/deyroute: state and backend binaries, except the backups in /var/lib/deyroute/backups
  - /var/log/deyroute, the deyroute system user and group, and the deyroute binary
Every tunnel stops.
```

یعنی: همه سرویس‌های تانل و خود سرویس deyroute متوقف و غیرفعال می‌شوند، جدول فایروال `inet deyroute` حذف می‌شود، تنظیمات کرنل به حالت قبل از deyroute برمی‌گردد، و پوشه‌های تنظیمات، وضعیت، لاگ و خود برنامه پاک می‌شوند.

| گزینه | معنی |
| --- | --- |
| `--keep-backups` | پوشه `/var/lib/deyroute/backups` را نگه دار |
| `--nodes` | روی Hub: اول deyroute را از همه Nodeهای آنلاین حذف کن |
| `--yes` | هیچ سؤالی نپرس (برای اسکریپت) |

با `--yes` چیزی پرسیده نمی‌شود، پس بکاپ‌ها **پاک می‌شوند** مگر اینکه `--keep-backups` را هم بدهید:

```bash
deyroute uninstall --keep-backups --yes
```

فقط چیزهای خود DEYROUTE پاک می‌شوند: بقیه جدول‌ها و قوانین فایروال، سرویس VPN شما، بسته‌های دیگر و فایل‌هایتان سر جایشان می‌مانند. اگر یک مرحله شکست بخورد، بقیه مراحل باز هم اجرا می‌شوند، باینری نگه داشته می‌شود و `DEY-I022` را می‌بینید؛ مشکل را رفع کنید و دوباره `deyroute uninstall` بزنید (تکرارش بی‌خطر است).

حذف Node **از روی Hub** (`deyroute node remove <id>`) خود سرور Node را تمیز نمی‌کند؛ برای آن روی خود Node بزنید `deyroute uninstall`.

</div>
