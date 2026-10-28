# آپدیت و حذف

<div dir="rtl">

[English](../en/update-uninstall.md) · [فهرست](index.md)

هر وقت خودتان بخواهید deyroute را روی Hub آپدیت می‌کنید و Nodeها دنبالش می‌آیند. Hub می‌تواند روزی یک بار خودش هم آپدیت شود (آپدیت خودکار، به‌صورت پیش‌فرض خاموش؛ پایین‌تر را ببینید). آپدیت deyroute تانل‌ها را قطع نمی‌کند: برنامه‌های تانل سرویس‌های systemd جدا هستند و وقتی سرویس deyroute ری‌استارت می‌شود دست نمی‌خورند.

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

۵. Nodeها نسخه Hub را دنبال می‌کنند (بخش «Nodeها چطور دنبال می‌کنند» را ببینید).

در هر بار فقط یکی از `--check`، `--version` و `--rollback` را بدهید. `--rollback` فایل `deyroute.prev` را برمی‌گرداند (اگر نباشد `DEY-S007`؛ آن‌وقت با `--version V` یک نسخه مشخص نصب کنید). این دستور وقتی سرویس دیگر بالا نمی‌آید (مثلاً یک آپدیت خراب که مدام کرش می‌کند) و روی Node هم کار می‌کند: باینری‌ها همان‌جا جابه‌جا می‌شوند و سرویس ری‌استارت می‌شود.

تا وقتی `config.yaml` تنظیمی دارد که یک نسخه قدیمی‌تر آن را نمی‌شناسد، `--rollback` و `--version` به آن نسخه با `DEY-S011` رد می‌شوند: آن نسخه فایل را قبول نمی‌کند و سرویس بالا نمی‌آید. پیام خطا هر تنظیم را همراه با اولین نسخه‌ای که آن را می‌شناسد نام می‌برد؛ اول آن تنظیم را بردارید و بعد دستور را تکرار کنید. برای `--rollback`، Hub نسخه‌ای را مقایسه می‌کند که موقع نوشتن `deyroute.prev` در آپدیت یا rollback برای همان فایل ثبت کرده است. اگر بعد از آن نصاب یا یک rollback بدون سرویس این فایل را عوض کرده باشد، نسخه‌اش معلوم نیست و تا وقتی چنین تنظیمی هست rollback رد می‌شود.

## Nodeها چطور دنبال می‌کنند

وقتی بعد از یک آپدیت یا rollback، Nodeی با نسخه‌ای غیر از نسخه Hub وصل می‌شود، Hub به آن می‌گوید نسخه Hub را نصب کند. Node فقط وقتی deyroute جدید نصب می‌کند که ریلیزش با کلید ریلیز DEYROUTE که داخل خود Node ساخته شده امضا شده باشد؛ حرف Hub درباره آن کافی نیست:

- Hub فایل‌های امضاشده ریلیز خودش را در `/var/lib/deyroute/bin/<version>/` نگه می‌دارد: `SHA256SUMS`، `SHA256SUMS.minisig`، آرشیو `deyroute_<version>_linux_<arch>.tar.gz` برای هر معماری‌ای که Nodeهایش دارند، و `backends.yaml` همراه امضایش. `deyroute update` و نصاب (با `--local` هم) این فایل‌ها را همان‌جا می‌گذارند. Hubی که آن‌ها را ندارد (قبل از این تغییر نصب شده یا با `--skip-signature`) اولین باری که یک Node لازمشان دارد، مثل خود ریلیز دانلودشان می‌کند: از طریق یک Node یا Mirror شما. Hub موقع بالا آمدن، فایل‌های نسخه‌هایی را که نه اجرا می‌کند و نه rollback به آن‌ها برمی‌گردد پاک می‌کند.
- Node این فایل‌ها را از Hub دانلود می‌کند و خودش بررسی‌شان می‌کند: امضای `SHA256SUMS`، این‌که `SHA256SUMS` آرشیو دقیقاً همان نسخه Hub را فهرست کرده باشد، checksum آرشیو، و این‌که آرشیو فقط یک فایل معمولی `deyroute` داشته باشد (نه link، نه مسیری بیرون از آرشیو، حداکثر ۲۰۰ مگابایت) که روی همین سرور اجرا شود. فقط بعد از این‌ها باینری‌اش را عوض می‌کند (قبلی به‌صورت `deyroute.prev` می‌ماند) و ری‌استارت می‌شود؛ تانل‌ها قطع نمی‌شوند.
- اگر یکی از بررسی‌ها رد شود، Node هیچ چیزی نصب نمی‌کند و با همان نسخه خودش کار می‌کند. در لاگ Hub خط «node could not install the hub's version» با `DEY-S001` و نام همان بررسی می‌آید (مثلاً «SHA256SUMS.minisig is not a valid signature …» یا «… does not match the signed SHA256SUMS»)؛ Hub وقتی Node دوباره وصل شود باز امتحان می‌کند.

**اول Hub را آپدیت کنید.** Node این نسخه فقط از Hubی آپدیت می‌شود که ریلیز امضاشده را در اختیارش بگذارد. Hubی با build قدیمی‌تر این کار را نمی‌کند: Node آپدیتش را با `DEY-S001` («update the hub first») رد می‌کند و با همان نسخه کار می‌کند. اول Hub را آپدیت کنید، بعد Nodeها دنبالش می‌آیند. Nodeی با build قدیمی‌تر مثل قبل از Hub جدید آپدیت می‌شود.

**buildهای توسعه.** Hubی که build توسعه دارد (`deyroute version` عبارت `dev` را نشان می‌دهد) ریلیز امضاشده ندارد، پس Nodeهایش با `DEY-S012` رد می‌کنند: روی Hub یک ریلیز نصب کنید. فقط در آزمایشگاه (محیط تست)، خط `allow_unsigned_updates: true` زیر `node:` در `/etc/deyroute/config.yaml` یک Node به همان Node اجازه می‌دهد باینری Hub را بدون امضا بگیرد (از Hub قدیمی‌تر هم). هیچ‌وقت آن را روی سروری با کاربر واقعی نگذارید: با این تنظیم، هر کسی Hub را در دست داشته باشد دوباره Node را هم در دست دارد. تا وقتی این تنظیم هست، Node نصب ریلیزهای قدیمی‌تر از **0.3.0** را رد می‌کند (`DEY-S011`، همه buildهای `0.3.0-edge.N` قدیمی‌تر از خود Node هم)، چون چنین ریلیزی این کلید را نمی‌شناسد و بالا نمی‌آید.

## آپدیت خودکار

به‌صورت پیش‌فرض خاموش است: deyroute هیچ نسخه‌ای را بدون درخواست شما نصب نمی‌کند. اگر روشنش کنید، هر روز ساعت **۴ صبح به وقت سرور** Hub جدیدترین نسخه‌ای را نصب می‌کند که حداقل **۲۴ ساعت** از انتشارش گذشته باشد؛ Nodeها مثل `deyroute update` دنبالش می‌آیند و تانل‌ها قطع نمی‌شوند.

بعد از ری‌استارت، Hub بررسی می‌کند که هر تانلی که قبل از آپدیت UP بود و هر Nodeی که آنلاین بود دوباره کار کند (تا ۱۰ دقیقه فرصت می‌دهد). اگر نشد — یا نسخه جدید اصلاً بالا نیامد — خودش نسخه قبلی را برمی‌گرداند، یک رویداد `DEY-S003` و یک خط در داشبورد ثبت می‌کند و آن نسخه را دیگر هیچ‌وقت خودکار نصب نمی‌کند (با `deyroute update` خودتان هنوز می‌توانید). نسخه قبلی پیش از آپدیت با همین `config.yaml` کار می‌کرد، پس دوباره آن را می‌خواند. فقط اگر بعد از آپدیت تنظیمی اضافه کرده باشید که نسخه قبلی آن را نمی‌شناسد، فایل را قبول نمی‌کند؛ در این حالت نسخه جدید روشن می‌ماند، رویداد `update_rolled_back` با کد `DEY-S011` ثبت می‌شود (تلگرام آن را همراه رویدادهای `update` می‌فرستد) و داشبورد تا ۷ روز `DEY-S011` را نشان می‌دهد (جزئیات در `deyroute update auto`).

```bash
deyroute update auto        # روشن یا خاموش، نسخه بعدی و زمانش، نتیجه آخرین آپدیت
deyroute update auto on     # روشن کردن
deyroute update auto off    # خاموش کردن دوباره
```

```text
── AUTOMATIC UPDATE ─────────────────────────────────────────
  Status     on
  When       every day at 04:00 server time; only releases out for 24 hours
  Running    0.3.0-edge.40
  Next       0.3.0-edge.41 on 2026-10-10 04:00
  Last       updated 0.3.0-edge.39 → 0.3.0-edge.40; 1 tunnels and 1 nodes work (2026-10-09 04:03)
```

کلید این تنظیم `hub.auto_update` در `config.yaml` است: `update auto on` (بعد از یک بکاپ خودکار) `auto_update: true` را می‌نویسد و `off` کلید را حذف می‌کند؛ پس بکاپ انتخاب شما را نگه می‌دارد و Hubی که از بکاپ ریستور شود همان حالت را دارد. بدون این کلید آپدیت خودکار خاموش است. برای انتخاب در زمان setup، `--auto-update on` یا `--auto-update off` (پیش‌فرض) بدهید:

```bash
deyroute setup --role hub --name ir-1 --yes --auto-update on
```

خروجی `deyroute --json update auto` فیلد `"enabled"` و `"source": "config"` را دارد.

تا وقتی روشن است، نسخه‌های قبل از **0.3.0** این کلید را نمی‌شناسند؛ پس `deyroute update --rollback` و `deyroute update --version V` به چنین نسخه‌ای (همه buildهای `0.3.0-edge.N` هم) با `DEY-S011` رد می‌شوند؛ اول `deyroute update auto off` بزنید. آپدیت به نسخه‌های جدیدتر مشکلی ندارد، و آپدیت خودکاری که ناموفق باشد باز هم به نسخه‌ای برمی‌گردد که از آن آمده بود، چون آن نسخه با همین کلید کار می‌کرد.

**Hubهایی که قبلاً نسخه قدیمی‌تر داشتند:** قبل از این تغییر آپدیت خودکار به‌صورت پیش‌فرض روشن بود و کلیدش در پایگاه‌داده وضعیت (`state.db`) نگه داشته می‌شد که در بکاپ نیست. بعد از آپدیت، آپدیت خودکار **خاموش** است تا خودتان با `deyroute update auto on` روشنش کنید، حتی روی Hubی که قبلاً روشن بود. چنین Hubی یک بار رویداد `auto_update_default_off` ثبت می‌کند و `deyroute status` تا ۷ روز این خط را نشان می‌دهد: «automatic update is now off by default; turn it on with: deyroute update auto on». نصب تازه هیچ‌کدام را نشان نمی‌دهد. deyroute کلید را همچنان جایی هم می‌نویسد که نسخه‌های قدیمی‌تر از آن می‌خوانند (در پایگاه‌داده وضعیت، با هر تغییر رکورد آپدیت خودکار و با `update auto on|off`)؛ پس بعد از `update --rollback` یا `update --version` به یک نسخه قدیمی‌تر، آپدیت خودکار همان حالتی را دارد که این‌جا داشت: خاموش، مگر این‌که روشنش کرده باشید.

تا وقتی روشن است، `deyroute status` نسخه جدید را جزو «نیاز به توجه» نشان نمی‌دهد، چون سر وقتش نصب می‌شود. اگر خودتان `deyroute update --rollback` بزنید، آپدیت خودکار هم نسخه‌ای را که کنار گذاشتید دیگر نصب نمی‌کند.

**راه جایگزین:** اجرای دوباره نصاب هم در جا ارتقا می‌دهد و تنظیمات را نگه می‌دارد. حتی وقتی سرویس خاموش است و روی Node هم کار می‌کند:

```bash
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh)
bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/download/v1.2.0/install.sh) --version 1.2.0
```

Hub و Nodeها باید نسخه major.minor یکسان داشته باشند (صفحه [Join](join.md)، بخش «نسخه ناسازگار»).

## کدام نسخه‌ها در دسترس می‌مانند

- نسخه‌های پایدار (`vX.Y.Z`) هیچ‌وقت حذف نمی‌شوند.
- بیلدهای توسعه (`…-edge.N`) وقتی حذف می‌شوند که ۱۰ بیلد جدیدتر از همان شاخه منتشر شده باشد (`main` و `dev` جدا شمرده می‌شوند)؛ نسخه‌ای که *latest* علامت خورده هیچ‌وقت حذف نمی‌شود. پس Hubی که روی یک بیلد توسعه است تا آن موقع می‌تواند Node وصل کند و نسخه خودش را دوباره نصب کند.
- نسخه‌ای که دیگر منتشرشده نیست نصب نمی‌شود: نصاب خود آن نسخه هم دیگر نیست (`curl` خطای `404` می‌دهد) و نصابی که با `--version` آن نسخه اجرا شود با `DEY-I024` متوقف می‌شود. نسخه‌ای را از [صفحه ریلیزها](https://github.com/localroot4/DeyRoute/releases) انتخاب کنید، یا از نسخه‌ای که خودتان ذخیره کرده‌اید با `--local` نصب کنید (صفحه [نصب](install.md)، بخش «وقتی سرور ایران به GitHub دسترسی ندارد»).

برای اینکه سرورها مدت طولانی روی یک نسخه بمانند، از نسخه پایدار استفاده کنید، یا فایل‌هایش (`install.sh`، `SHA256SUMS`، `SHA256SUMS.minisig` و آرشیوها) را خودتان نگه دارید.

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
