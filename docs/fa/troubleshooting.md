# عیب‌یابی

<div dir="rtl">

[English](../en/troubleshooting.md) · [فهرست](index.md)

## از اینجا شروع کنید

```bash
deyroute status                  # تانل UP است؟ Node آنلاین است؟
deyroute doctor                  # ۱۵ بررسی خودکار + فایل پشتیبانی
deyroute port check 443          # چک چهارمرحله‌ای پورت
deyroute logs main -f            # لاگ زنده تانل، سمت Hub و Node
deyroute events --tunnel main    # در ۲۴ ساعت اخیر چه اتفاقی افتاده
```

همین ابزارها در منو زیر `6) Diagnostics` هستند: `1) Port check`، `2) Tunnel test`، `3) Speed test`، `4) Logs`، `5) Doctor`.

## خواندن یک خطا

هر خطا یک کد ثابت به شکل `DEY-<حرف><سه رقم>` دارد و در منو و خط فرمان یکسان نمایش داده می‌شود:

```text
✖ DEY-P012  Port 443/tcp is already in use
  Why:  nginx (pid 1234) is listening on 0.0.0.0:443
  Fix:  choose another port, or stop that service first
  Log:  /var/log/deyroute/hub.log (search DEY-P012)
```

خط اول می‌گوید چه شد، `Why` می‌گوید چرا، `Fix` می‌گوید چه کنید و `Log` می‌گوید کجا بیشتر بخوانید. خط‌هایی که زیرش با `|` شروع می‌شوند جزئیات هستند (مثلاً چند خط آخر لاگ یک بک‌اند). وقتی کمک می‌خواهید، کل این چند خط را کپی کنید و بفرستید. فهرست کامل کدها با Why و Fix در [ERRORS.md](../ERRORS.md) است.

| حرف | حوزه |
| --- | --- |
| `I` | نصاب، راه‌اندازی، محیط |
| `C` | تنظیمات (`config.yaml`) |
| `N` | Nodeها و کانال کنترل |
| `P` | پورت و فایروال |
| `T` | گواهی‌های TLS |
| `B` | بک‌اندها (برنامه‌های تانل) |
| `F` | Failover |
| `S` | امنیت، بکاپ، آپدیت |
| `X` | داخلی / سیستم |

کد خروج دستورها: `0` موفق، `1` خطای کاربر، `2` خطای سیستم (`DEY-X…`)، `3` تأیید لازم است (وقتی بدون ترمینال اجرا می‌کنید `--yes` بدهید).

## `deyroute doctor`

```bash
deyroute doctor                   # همین سرور
deyroute doctor --node de-1       # این Hub به‌علاوه Node de-1
deyroute doctor --out /tmp/doctor.tar.gz
```

doctor این‌ها را جمع می‌کند: سیستم و نسخه‌ها، وضعیت همه سرویس‌ها، خط‌های آخر همه لاگ‌ها، پورت‌ها و نتیجه چک پورت، جدول فایروال، تنظیمات کرنل، Nodeها و تأخیر کنترل، پروب نردبان، رویدادهای اخیر و انقضای گواهی‌ها. بعد ۱۵ بررسی خودکار انجام می‌دهد و یک خلاصه کوتاه به زبان ساده چاپ می‌کند، مثلاً:

- `Node de-1 is offline on the control channel, but tunnel main still passes traffic: the control network has a problem, the tunnel is healthy` — یعنی فقط کانال کنترل مشکل دارد و تانل سالم است.
- `Tunnel main is DOWN and every transport failed or is quarantined: the IP of node de-1 is probably blocked` — یعنی احتمالاً IP سرور Node بلاک شده.
- `Tunnel main is connected, but the service behind it on node de-1 does not answer on 127.0.0.1:443` — یعنی تانل وصل است ولی سرویس پشتش روی Node جواب نمی‌دهد.
- `Clock of de-1 differs from the hub by …: TLS connections fail with a large difference` — یعنی ساعت سرورها با هم فرق دارد.

کنار هر مورد یک خط `Fix:` هست. doctor یک فایل پشتیبانی هم می‌نویسد: `/root/deyroute-doctor-<UTC>.tar.gz` که **همه رازها از آن حذف شده‌اند** (توکن‌ها، کلیدها و رمزها پوشانده می‌شوند و فایل قبل از نوشته شدن یک بار دیگر بررسی می‌شود). این فایل را همراه خلاصه بفرستید. اگر سرویس اجرا نباشد، doctor باز هم هر چه روی همین سرور بتواند جمع می‌کند و این را اعلام می‌کند.

## لاگ‌ها و رویدادها

| فایل | محتوا |
| --- | --- |
| `/var/log/deyroute/hub.log` / `node.log` | سرویس Hub / سرویس Node |
| `/var/log/deyroute/tunnels/<tunnel>.log` | برنامه‌های بک‌اند یک تانل |
| `/var/log/deyroute/events.log` | همه رویدادها، هر کدام یک خط |
| `/var/log/deyroute/deyroute.log` | خطاهای خود دستور `deyroute` |

```bash
deyroute logs main -f            # تانل: خط‌های Hub و Node با پیشوند [hub] / [node]
deyroute logs hub --since 1h
deyroute logs node               # روی Node: سرویس خودش
deyroute events --since 24h --json
```

لاگ‌های خود deyroute به شکل JSON خط‌به‌خط با زمان UTC هستند؛ لاگ‌ها در ۲۰ مگابایت چرخانده می‌شوند و ۵ فایل فشرده نگه داشته می‌شود. برای جزئیات بیشتر یک دستور را با `--debug` اجرا کنید یا سرویس را با `DEYROUTE_DEBUG=1` روشن کنید. رازها هیچ‌وقت در لاگ نمی‌آیند (به شکل `***` چاپ می‌شوند).

رویدادهای مهم: `tunnel_up`، `tunnel_degraded`، `tunnel_down`، `switch_transport`، `switch_node`، `manual_switch`، `failback`، `failback_failed`، `flapping`، `node_online`، `node_offline`، `node_ip_changed`، `service_down`، `backend_crash`، `probe_error`، `rung_skipped`، `rung_restored`، `config_applied`.

## مشکلات رایج

### دستور می‌گوید `DEY-X003 Daemon not running`

سرویسی که منو و دستورها با آن کار می‌کنند روشن نیست.

```bash
systemctl status deyroute-hub        # روی Node: deyroute-node
systemctl start deyroute-hub
journalctl -u deyroute-hub -n 50
```

اگر سرور اصلاً راه‌اندازی نشده باشد، خط Fix همین را می‌گوید: `deyroute setup` را بزنید یا به یک Hub وصل شوید.

### پورت مشغول است — `DEY-P012`

```text
✖ DEY-P012  Port 443/tcp is already in use
  Why:  nginx (pid 1234) is listening on 0.0.0.0:443
```

برنامه دیگری روی Hub این پورت را گرفته. یا پورت دیگری انتخاب کنید (`deyroute port suggest`) یا آن برنامه را خودتان متوقف کنید (`systemctl stop nginx; systemctl disable nginx`). DEYROUTE هیچ‌وقت برنامه‌ای را که مال خودش نیست متوقف نمی‌کند. `DEY-P011` یعنی پورت رزرو است (22، پورت کنترل، 30000 تا 31999).

### فایروال پورت را بسته — `DEY-P013` و `DEY-P014`

`deyroute port check 443` نشان می‌دهد کدام فایروال پورت را بسته و دستور دقیق باز کردنش را می‌دهد (مثلاً `ufw allow 443/tcp`)؛ آن را خودتان اجرا کنید. `DEY-P014` (از Node در دسترس نیست) معمولاً یعنی فایروال پنل ارائه‌دهنده سرور پورت را بسته: آنجا بازش کنید. `deyroute security firewall show` را هم ببینید. `DEY-P031` یعنی DEYROUTE روی این Hub فایروال را مدیریت نمی‌کند (`security.firewall_managed: false`) و باید پورت‌ها را دستی باز کنید.

### UDP بسته است — `DEY-P015` و `DEY-B007`

تست UDP بین Hub و Node شکست خورده، پس ترنسپورت‌هایی که UDP لازم دارند (`hysteria2/udp`، `wireguard/kernel`، `backhaul/udp`، `frp/quic`، `frp/kcp`) برای این Node کنار گذاشته می‌شوند. ترنسپورت‌های TCP کار می‌کنند و لازم نیست کاری بکنید. اگر آن‌ها را می‌خواهید، UDP را بین دو سرور باز کنید (فایروال پنل سرور)؛ پله‌های کنارگذاشته هر ۳۰ دقیقه دوباره تست می‌شوند. `deyroute node test de-1` نتیجه را به شکل `UDP ok` یا `blocked` نشان می‌دهد.

### Node آفلاین است — `DEY-N003`

- اگر تانل هنوز ترافیک را رد می‌کند، فقط کانال کنترل قطع است: تانل سالم است و سوییچی انجام نمی‌شود.
- روی Node: `systemctl status deyroute-node` و `deyroute logs node`.
- Node باید به `HUB_IP:44433` برسد؛ فایروال پنل سرور ایران را چک کنید.
- اگر سرور Node خاموش است یا IP آن بلاک شده، تانل به Node پشتیبان می‌رود — اگر ندارید، اضافه کنید ([Node پشتیبان](backup-node.md)).
- `DEY-N004`: نسخه‌ها فرق دارند؛ صفحه [Join](join.md) بخش «نسخه ناسازگار».

### تانل DOWN است — `DEY-F001`

همه ترنسپورت‌ها روی همه Nodeها شکست خورده‌اند. نردبان هر ۳۰ ثانیه (کم‌کم تا هر ۵ دقیقه) از پله ۱ دوباره امتحان می‌شود. این‌ها را چک کنید:

۱. `deyroute node test de-1` — اصلاً به Node دسترسی هست؟

۲. `deyroute tunnel show main` — کدام پله‌ها skip یا قرنطینه شده‌اند؟

۳. `deyroute logs main` — خطاهای بک‌اندها.

۴. سرویس روی Node روشن است؟ (`DEY-F005`)

اگر همه پله‌ها timeout می‌شوند، احتمالاً IP سرور Node از سمت Hub بلاک شده: به Node یک IP جدید بدهید یا یک Node پشتیبان اضافه کنید.

### سرویس روی Node خوابیده — `DEY-F005`

تانل کار می‌کند ولی پشتش روی Node چیزی جواب نمی‌دهد (مثلاً Xray متوقف شده یا روی پورت دیگری گوش می‌دهد). سرویس را روشن کنید (`systemctl restart xray`) و مطمئن شوید روی مقصد تانل گوش می‌دهد (پیش‌فرض `127.0.0.1:443`). اگر Node پشتیبان داشته باشید، تانل در این مدت به آن می‌رود.

### تانل UP است ولی کاربران وصل نمی‌شوند

- بررسی‌های تانل فقط از روی Hub و Node انجام می‌شوند و فیلترینگ **داخل ایران** را نمی‌بینند. اگر کاربران به `HUB_IP:443` نمی‌رسند، ممکن است IP یا پورت Hub برایشان فیلتر شده باشد: پورت دیگری امتحان کنید (مثلاً 2053 یا 8443) یا Hub را جابه‌جا کنید ([جابه‌جایی Hub](hub-move.md)).
- کانفیگ کلاینت را چک کنید: فقط آدرس به IP سرور ایران عوض می‌شود؛ پورت، UUID، SNI، host و path همان تنظیمات Node می‌مانند.
- `deyroute diag probe main --all-ports` همه پورت‌های تانل را پروب می‌کند.

### سوییچ‌های پشت سر هم — `DEY-F002`

بعد از ۶ سوییچ خودکار در یک ساعت، Failover وضعیت فعلی را نگه می‌دارد. شبکه ناپایدار است: `deyroute events --tunnel main` را بخوانید، یک ترنسپورت را ثابت کنید (`deyroute tunnel switch main --transport <id>`) یا Failover را موقتاً متوقف کنید (`deyroute tunnel pause main`).

### بک‌اند روشن نمی‌شود — `DEY-B003` و `DEY-B004`

`DEY-B003` خط‌های آخر لاگ بک‌اند را نشان می‌دهد؛ `DEY-B004` یعنی روشن شده ولی در مهلت ۱۵ ثانیه ترافیکی از تانل رد نشده. `deyroute logs main` را بخوانید، بعد `deyroute tunnel restart main` بزنید یا به پله دیگری سوییچ کنید. `DEY-B001`: باینری دانلود نشد — یک Node باید آنلاین باشد (Hub از طریق آن دانلود می‌کند)، یا `DEYROUTE_MIRROR` را تنظیم کنید.

### اختلاف ساعت

اگر ساعت یک سرور خیلی جلو یا عقب باشد، TLS خطا می‌دهد. همگام‌سازی ساعت را روی هر دو سرور روشن کنید: `timedatectl set-ntp true`.

## رایج‌ترین کدها

| کد | معنی | چه کنیم |
| --- | --- | --- |
| `DEY-I001` | با root اجرا نشده | با root اجرا کنید |
| `DEY-I004` | دانلود از همه منبع‌ها ناموفق | `--mirror URL`، `DEYROUTE_MIRROR` یا `--local FILE` ([نصب](install.md)) |
| `DEY-I006` | امضا نامعتبر | فقط فایل‌های رسمی ریلیز |
| `DEY-I013` | سرور قبلاً راه‌اندازی شده | از منو استفاده کنید، یا قبل از راه‌اندازی دوباره `deyroute uninstall` |
| `DEY-I014` | یک مرحله راه‌اندازی شکست خورد | خط‌های زیر خطا را بخوانید، مشکل را رفع کنید و نصاب را دوباره بزنید |
| `DEY-I021` | IP پیداشده عمومی نیست | IP عمومی واقعی را بدهید (`hub.public_ip`، بعد `deyroute config apply`) |
| `DEY-C001` | کلید ناشناخته در `config.yaml` | حذفش کنید؛ `deyroute config validate` |
| `DEY-C003` | دو تانل یک پورت دارند | پورت یکی را عوض کنید |
| `DEY-C014` | `config.yaml` خوانده نمی‌شود | آخرین نسخه سالم را از `/var/lib/deyroute/backups/auto/` ریستور کنید |
| `DEY-C021` | تانل ناشناخته | شناسه‌ها با `deyroute tunnel list` |
| `DEY-N001` | توکن Join نامعتبر یا منقضی | Join command تازه |
| `DEY-N002` | اثرانگشت CA نمی‌خواند | Join command را دوباره کپی کنید، ویرایشش نکنید |
| `DEY-N003` | Node آفلاین | بالا را ببینید |
| `DEY-N004` | نسخه‌های ناسازگار | یک نسخه روی Hub و Node |
| `DEY-N009` | Node به Hub نمی‌رسد | سرویس Hub و فایروال پنل سرور |
| `DEY-N012` | هیچ Node آنلاینی برای دانلود نیست | یک Node را آنلاین کنید یا `DEYROUTE_MIRROR` بدهید |
| `DEY-P011` | پورت رزرو است | پورت دیگر (`deyroute port suggest`) |
| `DEY-P012` | پورت مشغول است | پورت دیگر، یا آن برنامه را متوقف کنید |
| `DEY-P013` / `P014` | فایروال پورت را بسته | بازش کنید (دستور داخل خطا / پنل سرور) |
| `DEY-P015` | UDP بسته است | کاری لازم نیست؛ پله‌های UDP کنار گذاشته می‌شوند |
| `DEY-T001` / `T006` | گواهی منقضی شده / نزدیک انقضاست | `deyroute security tls renew` |
| `DEY-T003` | ACME شکست خورد | دامنه DNS-only باشد و پورت 80 آزاد؛ TLS به `auto` برگشته |
| `DEY-B003` | unit بک‌اند روشن نشد | خط‌های لاگش را ببینید، `deyroute tunnel restart <tunnel>` |
| `DEY-B004` | بعد از روشن شدن ترافیکی رد نشد | `deyroute logs <tunnel>`، پله بعدی را امتحان کنید |
| `DEY-B006` / `B007` | پله برای این تانل skip شد | هر ۳۰ دقیقه دوباره تست می‌شود؛ [پرسش‌های فیلترینگ](faq-filtering.md) |
| `DEY-B042` | هیچ decoy SNI در دسترس نیست | `hub.decoy_snis` را تنظیم کنید ([پرسش‌های فیلترینگ](faq-filtering.md)) |
| `DEY-F001` | همه کاندیدها شکست خوردند | بخش «تانل DOWN است» |
| `DEY-F002` | سقف نوسان | بالا را ببینید |
| `DEY-F003` | برگشت (failback) ناموفق | کاری لازم نیست؛ بعداً دوباره امتحان می‌شود |
| `DEY-F005` | سرویس روی Node خوابیده | سرویس را روشن کنید |
| `DEY-S004` | بکاپ رمزگشایی نشد | رمز عبور اشتباه است |
| `DEY-S008` | رمز عبور بکاپ لازم است | تایپش کنید یا `DEYROUTE_BACKUP_PASSPHRASE` بدهید |
| `DEY-X003` | سرویس اجرا نیست | `systemctl start deyroute-hub` (یا `deyroute-node`) |
| `DEY-X008` | هنوز پیاده نشده | این بیلد هنوز این قابلیت را ندارد؛ `CHANGELOG.md` را ببینید |
| `DEY-X009` | دستور مال نقش دیگر است | مثلاً دستورهای تانل روی Hub، و `node set-hub` روی Node |
| `DEY-X000` | خطای غیرمنتظره | `deyroute doctor` و فرستادن فایلش |

</div>
