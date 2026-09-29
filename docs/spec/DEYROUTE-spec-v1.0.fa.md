# DEYROUTE — سوپرپرامت پیاده‌سازی Tunnel Manager (v1.0)

Sep 29, 2026 · @J0K3R

## ۰. راهنمای استفاده از این سند (اول این را بخوان)

این سند مشخصات کامل و الزام‌آور پروژه **DEYROUTE Tunnel Manager** است؛ هر جمله‌ای که «باید» دارد اجباری است و فقط مواردی که صریحاً «اختیاری» یا «پیشنهاد» نوشته شده قابل حذف‌اند.

**قواعد اجرا برای برنامه‌نویس:**

1. کل سند را یک‌بار کامل بخوان، بعد از فاز ۰ (بخش ۱۶) شروع کن. هیچ فازی بدون پاس شدن «معیار پذیرش» فاز قبل شروع نمی‌شود.
2. هر نام، مسیر، فرمت، متن منو، کد خطا و دستور CLI که در سند دقیق آمده، عیناً همان را پیاده کن. سلیقه شخصی فقط جایی مجاز است که سند ساکت است.
3. ابهام یا تناقض دیدی، حدس نزن؛ در فایل `QUESTIONS.md` ریشه مخزن ثبت کن و قبل از پیاده‌سازی آن بخش بپرس.
4. هر فاز یک Pull Request و یک Git tag (`v0.<phase>.0`) است با: کد، تست‌های خودکار، مستندات همان فاز، و یک اسکرین‌شات/ویدئوی کوتاه از اجرای معیار پذیرش.
5. نسخه هر بک‌اند بیرونی (Backhaul، Rathole، FRP، Waterwall، Xray، Hysteria2، AmneziaWG، HAProxy، Gost، Chisel) را pin کن و از README همان نسخه پیروی کن. نمونه‌کانفیگ‌های این سند «شکل مورد انتظار» هستند، نه منبع نهایی؛ کلیدها را با مستندات نسخه pin‌شده تطبیق بده و اختلاف را در `docs/backends/<name>.md` ثبت کن.
6. UI ترمینال فقط انگلیسی؛ مستندات کاربر فارسی و انگلیسی؛ کامنت‌های کد و پیام‌های commit انگلیسی.
7. هیچ داده‌ای به هیچ سرور بیرونی فرستاده نمی‌شود (no telemetry). تنها ترافیک بیرونی برنامه: دانلود ریلیز از GitHub یا Mirror تعریف‌شده توسط مالک.
8. کیفیت هدف: چیزی که مالک بدون خواندن هیچ مستندی، فقط با منو، در کمتر از ۵ دقیقه یک تانل کامل با failover بزند.

**واژه‌نامه (در کل سند و کد به همین معنا):**

| واژه | معنی |
| --- | --- |
| Hub | سرور ایران؛ نقطه ورود کاربران نهایی و مغز مدیریت (منو، DB، موتور failover) |
| Node | سرور خارج؛ محل اجرای سرویس VPN واقعی (Xray/Marzban/3x-ui و…) و سمت دیگر تانل |
| Tunnel | یک مسیر منطقی از Hub به یک Node با یک یا چند Port map |
| Port map | نگاشت `listen_port` روی Hub به `target_host:port` روی Node (tcp یا udp) |
| Backend | نرم‌افزار تانل بیرونی (backhaul، rathole، frp …) |
| Transport | یک روش مشخص از یک بک‌اند، مثل `backhaul/wssmux` یا `rathole/noise` |
| Ladder | لیست مرتب ترنسپورت‌های یک تانل؛ failover به ترتیب همین لیست جلو می‌رود |
| Failover | سوییچ خودکار به ترنسپورت بعدی یا Node پشتیبان بعد از خرابی تأییدشده |
| Failback | بازگشت خودکار به ترنسپورت/Node اصلی بعد از سالم ماندن پایدار |
| Probe | آزمون سلامت (TCP/TLS/HTTP) که موتور سلامت هر چند ثانیه اجرا می‌کند |
| Simple mode | حالت پیش‌فرض منو: فقط سؤال‌های ضروری، بقیه خودکار |
| Advanced mode | همه گزینه‌ها باز؛ برای مالک فنی |

## ۱. هدف، تصویر کلی و تجربه کاربری هدف

DEYROUTE یک ابزار مدیریت تانل است که کاربرِ داخل ایران به `IP ایران:پورت` وصل می‌شود و ترافیکش، بدون دست خوردن TLS، از یک تانل مقاوم به فیلترینگ به سرور خارج می‌رسد؛ اگر تانل بسته شد، خودکار به روش بعدی و بعد به سرور خارج بعدی می‌رود.

**مسئله‌ای که حل می‌کند:**

- سرویس‌های ما (ws+tls و tcp+tls) روی سرور خارج اجرا می‌شوند؛ کاربر باید فقط IP ایران را ببیند.
- تانل‌های تک‌روشی با یک تغییر در فیلترینگ می‌میرند؛ ما چند روش می‌خواهیم که خودکار جایگزین هم شوند.
- اسکریپت‌های موجود شکننده‌اند: تداخل پورت، خطای TLS، پیام خطای مبهم، منوی زشت. DEYROUTE باید همه این‌ها را از بین ببرد.

**تجربه کاربری هدف (این سناریو باید عیناً کار کند):**

1. مالک روی سرور ایران می‌زند: `bash <(curl -fsSL https://get.deyroute.example/install.sh)` — دامنه واقعی را مالک می‌دهد.
2. بنر DEYROUTE می‌آید، ویزارد می‌پرسد: نقش؟ → `Hub`. نام؟ → `ir-1`. بقیه (پورت کنترل، کلیدها، فایروال، sysctl) خودکار.
3. منو یک «Join command» نشان می‌دهد. مالک همان یک خط را روی سرور خارج می‌زند؛ Node ظرف کمتر از ۳۰ ثانیه در داشبورد Hub سبز می‌شود.
4. مالک در منوی Hub می‌زند `Add tunnel` (Simple): کدام Node؟ (لیست)، کدام پورت‌ها؟ (مثلاً `443,2053`). تمام. تانل با نردبان پیش‌فرض بالا می‌آید، پورت‌ها چک و باز می‌شوند، TLS تانل ساخته می‌شود.
5. اگر فیلترینگ ترنسپورت فعلی را بست، ظرف حداکثر ۲۰ ثانیه تانل روی ترنسپورت بعدی است و یک خط در Events (و در صورت فعال بودن، تلگرام) ثبت می‌شود. مالک هیچ کاری نمی‌کند.
6. مالک بعداً یک Node دوم را با `Add backup node` به همان تانل وصل می‌کند؛ اگر Node اول از دسترس خارج شد، همان پورت‌ها به Node دوم می‌روند (به شرط یکسان بودن سرویس روی هر دو).

**مخاطب:** مالک سرویس (فنی، اما نمی‌خواهد هر بار کانفیگ بنویسد) و اپراتورهایی که فقط باید بتوانند پورت اضافه کنند یا وضعیت ببینند. به همین دلیل Simple mode پیش‌فرض است و Advanced mode با یک کلید باز می‌شود.

**خارج از محدوده (v1):** پنل وب، مدیریت کاربران VPN، ساخت کانفیگ Xray برای کاربران، بیش از یک Hub در یک نصب (هر Hub مستقل است و می‌تواند به همان Nodeها وصل شود).

## ۲. تصمیم‌های ثابت (قابل مذاکره نیست)

زبان پیاده‌سازی **Go** است (نسخه ۱.۲۲ یا بالاتر)، خروجی یک باینری استاتیک به نام `deyroute` برای `linux/amd64` و `linux/arm64`، به‌همراه یک نصاب Bash خیلی کوچک (`install.sh`) که فقط باینری را می‌آورد و `deyroute setup` را اجرا می‌کند.

**چرا Go و نه Bash خالص:** موتور سلامت/failover یک دیمن دائمی است، منو باید حرفه‌ای و بدون وابستگی باشد، وضعیت باید تراکنشی ذخیره شود و خطاها باید ساخت‌یافته باشند؛ هیچ‌کدام در Bash قابل اعتماد نیستند. با `CGO_ENABLED=0` باینری روی هر توزیع لینوکس بدون glibc خاص کار می‌کند.

**نام‌ها و مسیرها (عیناً):**

| مورد | مقدار |
| --- | --- |
| باینری | `/usr/local/bin/deyroute` + symlink کوتاه `/usr/local/bin/dey` |
| پیکربندی اصلی | `/etc/deyroute/config.yaml` (0600، مالک root) |
| رازها | `/etc/deyroute/secrets/` (0700) — توکن‌ها، کلید Ed25519، کلیدهای TLS |
| کانفیگ رندرشده بک‌اندها | `/etc/deyroute/backends/<backend>/<tunnel_id>/` (هر بار از config.yaml بازتولید می‌شود؛ دستی ویرایش نمی‌شود) |
| باینری بک‌اندها | `/var/lib/deyroute/bin/<backend>/<version>/<backend>` + فایل `.sha256` کنار هر کدام |
| وضعیت و رویدادها | `/var/lib/deyroute/state.db` (bbolt) |
| بکاپ‌ها | `/var/lib/deyroute/backups/deyroute-backup-<UTC-timestamp>.tar.gz.age` |
| لاگ‌ها | `/var/log/deyroute/{deyroute,hub,node,events}.log` و `/var/log/deyroute/tunnels/<tunnel_id>.log` |
| سوکت CLI↔دیمن | `/run/deyroute/daemon.sock` (0600) |
| systemd | `deyroute-hub.service`، `deyroute-node.service`، `deyroute-tun@.service` (instance = `<tunnel_id>`) |
| nftables | جدول `inet deyroute` (فقط این جدول را می‌سازد و مدیریت می‌کند؛ به بقیه قوانین دست نمی‌زند) |
| متغیرهای محیطی | پیشوند `DEYROUTE_` (مثل `DEYROUTE_DEBUG=1`، `DEYROUTE_MIRROR=https://…`) |
| نسخه‌گذاری | SemVer؛ `deyroute version` نسخه، commit، تاریخ بیلد و نسخه Go را چاپ می‌کند |

**پیش‌نیازهای سیستم (نصاب چک می‌کند و با کد خطای مشخص می‌ایستد):** root، systemd (نسخه ۲۴۵+)، کرنل ۵.۴+ (برای BBR)، `iproute2`، یکی از `nftables` یا `iptables`، `ca-certificates`، و برای نصاب `curl` یا `wget`. هیچ وابستگی زمان اجرای دیگری مجاز نیست (نه Python، نه Docker، نه Node).

**ماتریس سیستم‌عامل:**

| توزیع | نسخه‌ها | سطح |
| --- | --- | --- |
| Ubuntu | 22.04, 24.04, 26.04 | Tier 1 — در CI تست می‌شود |
| Debian | 12, 13 | Tier 1 — در CI تست می‌شود |
| Ubuntu | 20.04 | Tier 2 — باید کار کند، تست دستی |
| Debian | 11 | Tier 2 |
| Rocky / AlmaLinux | 8, 9, 10 | Tier 2 (dnf، firewalld تشخیص داده شود) |
| Fedora | دو نسخه آخر | Tier 2 |
| Arch Linux | rolling | Tier 2 (pacman) |

**فرمت داده:** `config.yaml` با schema سخت‌گیر (کلید ناشناخته = خطا) و فیلد `schema_version` با مهاجرت خودکار بین نسخه‌ها. وضعیت لحظه‌ای (سلامت، متریک، رویدادها) در bbolt؛ هرگز در YAML. زمان‌ها در لاگ و DB به UTC (RFC3339)، در UI به زمان محلی سرور.

**زبان و متن:** UI و لاگ انگلیسی؛ همه رشته‌های UI در یک فایل `internal/i18n/en.go` تا بعداً فارسی اضافه شود. بنر ASCII حتماً کلمه `DEYROUTE` را نشان می‌دهد (بخش ۶).

## ۳. معماری

یک باینری، دو نقش: **Hub** (ایران) مغز سیستم است و **Node** (خارج) یک agent سبک که به Hub وصل می‌شود و دستور می‌گیرد؛ Hub هرگز به Node وصل نمی‌شود و SSH هیچ‌جا استفاده نمی‌شود.

&#91;embedded content: معماری Hub/Node · یک ترنسپورت فعال، بقیه گرم\]

کاربر فقط Hub را می‌بیند؛ Nodeها خودشان به Hub وصل می‌شوند و Hub فقط با stop/start unitها بین پله‌ها و Nodeها سوییچ می‌کند.

**Hub (`deyroute-hub.service`، یک پروسه Go):**

1. **Control API:** HTTPS با mTLS روی `control_port` (پیش‌فرض `44433`؛ اگر مشغول بود پورت آزاد بعدی پیشنهاد می‌شود). فقط Nodeهای Join‌شده گواهی معتبر دارند؛ nftables این پورت را فقط برای IP همان Nodeها باز می‌کند.
2. **Node registry:** لیست Nodeها، وضعیت اتصال، نسخه، آخرین heartbeat، تأخیر کنترل.
3. **Renderer + Unit manager:** از `config.yaml` برای هر تانل و هر ترنسپورت، کانفیگ بک‌اند را رندر می‌کند و unit مربوطه را از طریق `systemctl` مدیریت می‌کند (start/stop/restart/enable، خواندن وضعیت با `systemctl show`).
4. **Health engine + Failover engine:** بخش ۹.
5. **Event bus + Notifier:** هر رویداد (up/down/switch/failback/error) در `events.log` و bbolt؛ در صورت فعال بودن، تلگرام.
6. **Local API:** روی سوکت `/run/deyroute/daemon.sock` برای TUI و CLI؛ TUI هیچ منطقی ندارد، فقط از این API می‌خواند و می‌نویسد.

**Node (`deyroute-node.service`):**

- یک اتصال دائمی خروجی به Control API (HTTP/2 + یک استریم دوطرفه؛ اگر قطع شد با backoff نمایی از ۱ تا ۳۰ ثانیه دوباره وصل می‌شود).
- دستوراتی که از Hub می‌گیرد: `backend.install(name, version, sha256)`, `backend.render(files)`, `unit.start/stop/restart/status`, `probe.tcp/tls/http(target)`, `port.check_from_outside(hub_ip, port)`, `fetch.proxy(url)` (بخش ۵: دانلود از GitHub به‌نیابت از Hub), `sysinfo`, `metrics`.
- هر ۵ ثانیه heartbeat می‌فرستد: CPU، RAM، وضعیت unitها، خطای آخر.
- Node هیچ تصمیمی نمی‌گیرد؛ فقط اجرا و گزارش.

**Data plane (خودِ تانل):**

- برای هر تانل فقط **یک ترنسپورت فعال** است (چون پورت‌های کاربر را فقط یک پروسه می‌تواند bind کند). بقیه ترنسپورت‌های نردبان «گرم» هستند: باینری نصب، کانفیگ رندر، unit ساخته ولی stopped. سوییچ = stop فعلی + start بعدی (بخش ۹).
- بک‌اندهای **معکوس** (Backhaul، Rathole، FRP، Waterwall-reverse، Chisel، Gost-rtcp): سرور بک‌اند روی Hub اجرا می‌شود و پورت‌های کاربر را bind می‌کند؛ کلاینت بک‌اند روی Node به Hub وصل می‌شود و به سرویس محلی می‌رساند.
- بک‌اندهای **مستقیم** (Xray-Reality relay، Hysteria2، WireGuard/AmneziaWG، Direct/HAProxy): Hub خودش به Node وصل می‌شود (Hub → Node). سمت Node یک inbound/سرور بک‌اند اجرا می‌شود.
- هر بک‌اند این دو حالت را پشت اینترفیس `Backend` (بخش ۷) پنهان می‌کند؛ موتور failover تفاوتی بین آن‌ها نمی‌بیند.
- ترافیک کاربر (TLS خودش) هیچ‌جا باز نمی‌شود؛ تانل فقط بایت‌های TCP/UDP را رد می‌کند. SNI، گواهی و مسیر ws همان است که روی Node تنظیم شده.

**Join (یک‌بار برای هر Node):**

1. Hub در منو `Generate join command` می‌سازد: `bash <(curl -fsSL <installer>) join 'dey://<token>@<hub_ip>:<control_port>#<hub_ca_sha256>'` — توکن یک‌بارمصرف، ۳۲ بایت تصادفی، انقضا ۱۵ دقیقه.
2. Node نصب می‌شود، جفت‌کلید Ed25519 می‌سازد، CSR می‌فرستد (با توکن)، Hub با CA داخلی خودش گواهی ۱۰ ساله صادر می‌کند، fingerprint CA را Node از همان لینک join تأیید می‌کند (pinning؛ هیچ اعتمادی به CA سیستم).
3. Hub IP عمومی Node را در مجموعه nftables `@nodes` می‌گذارد (control\_port + پورت‌های کنترل بک‌اندها فقط از این IPها).
4. Node در داشبورد Hub با نام و پرچم/کشور (از GeoIP آفلاین اختیاری، یا فقط IP) ظاهر می‌شود.

**Node پشتیبان:** هر تانل یک لیست مرتب `nodes: [primary, backup…]` دارد. نردبان ترنسپورت برای هر Node جداگانه رندر و گرم می‌شود، تا سوییچ سرور هم مثل سوییچ ترنسپورت فقط stop/start باشد.

**جداسازی خرابی:** کرش یک بک‌اند هیچ‌وقت Hub را نمی‌اندازد (پروسه‌های جدا، unitهای جدا با `Restart=always`). کرش Hub تانل فعال را نمی‌اندازد (بک‌اند مستقل ادامه می‌دهد؛ فقط failover تا برگشت Hub متوقف است). هر دو سرویس با `WatchdogSec` و restart خودکار.

## ۴. مدل داده و فایل پیکربندی

تنها منبع حقیقت `/etc/deyroute/config.yaml` است؛ همه کانفیگ‌های بک‌اند از آن رندر می‌شوند و هر تغییر منو یعنی تغییر این فایل به‌صورت اتمیک (نوشتن در فایل موقت + `rename`) و سپس `apply`.

**نمونه کامل config.yaml روی Hub (همه کلیدها همین‌ها هستند؛ کلید اضافه = خطا):**

```yaml
schema_version: 1
role: hub                      # hub | node
hub:
  name: ir-1
  control_port: 44433
  public_ip: 5.6.7.8          # auto-detected, editable
  domain: ""                   # optional; enables ACME for tunnel TLS
  ui_mode: simple              # simple | advanced
  notify:
    telegram:
      enabled: false
      bot_token_file: /etc/deyroute/secrets/telegram.token
      chat_id: ""
      events: [down, switch, failback, node_offline]
nodes:
  - id: de-1                   # slug, unique, immutable after join
    name: "Germany 1"
    public_ip: 1.2.3.4
    cert_fingerprint: "sha256:..."
    tags: [primary]
  - id: nl-1
    name: "Netherlands 1"
    public_ip: 9.8.7.6
    cert_fingerprint: "sha256:..."
tunnels:
  - id: main                   # slug, unique
    name: "Main 443/2053"
    enabled: true
    nodes: [de-1, nl-1]        # ordered: first = primary, rest = backups
    ports:
      - listen: 443
        proto: tcp
        target: 127.0.0.1:443  # on the node
      - listen: 2053
        proto: tcp
        target: 127.0.0.1:2053
    ladder: default            # name of a ladder profile (below) or inline list
    failover:
      policy: transport_then_node   # transport_only | transport_then_node | node_only
      probe_interval_s: 5
      probe_timeout_s: 3
      fail_threshold: 3
      recover_threshold: 6
      failback: true
      failback_after_s: 300
      max_switches_per_hour: 6
    tls:
      mode: auto               # auto (self-signed+pin) | acme | custom
ladders:
  default:
    - backhaul/wssmux
    - backhaul/tcpmux
    - rathole/noise
    - frp/wss
    - xray/reality
    - hysteria2/udp
    - waterwall/reverse-reality
    - direct/haproxy
tuning:
  sysctl_profile: balanced     # off | balanced | aggressive
  bbr: true
security:
  firewall_managed: true       # deyroute manages table inet deyroute
  restrict_control_to_nodes: true
```

**config.yaml روی Node (کوتاه):**

```yaml
schema_version: 1
role: node
node:
  id: de-1
  hub_addr: 5.6.7.8:44433
  hub_ca_fingerprint: "sha256:..."
  cert_file: /etc/deyroute/secrets/node.crt
  key_file: /etc/deyroute/secrets/node.key
```

**قواعد اعتبارسنجی (خطاهای کاربرپسند با کد DEY-Cxxx):**

- `id`ها فقط `[a-z0-9-]{2,32}`؛ یکتا؛ بعد از ساخت تغییر نمی‌کنند (`name` قابل تغییر است).
- `listen` بین 1 و 65535؛ در یک Hub هیچ دو تانلی یک `listen/proto` مشترک ندارند؛ `listen` با `control_port` و پورت‌های کنترل بک‌اندها هم برخورد نکند.
- `target` باید `host:port` معتبر باشد؛ پیش‌فرض `127.0.0.1:<listen>`.
- هر تانل حداقل یک Node و حداقل یک ترنسپورت در نردبان دارد؛ ترنسپورت ناشناخته = خطا با لیست ترنسپورت‌های معتبر.
- `hysteria2/udp` و `wireguard/*` فقط اگر Node و Hub هر دو UDP را در تست دسترسی پاس کرده باشند در نردبان فعال می‌شوند؛ وگرنه با هشدار skip می‌شوند (نه خطا).

**حالت لحظه‌ای در bbolt (`state.db`)، هرگز در YAML:**

| bucket | محتوا |
| --- | --- |
| `nodes/<id>` | online/offline، آخرین heartbeat، نسخه agent، تأخیر کنترل ms، CPU/RAM |
| `tunnels/<id>` | `active_node`، `active_transport`، state machine، شمارنده fail/recover، زمان آخرین سوییچ، تعداد سوییچ در ساعت |
| `probes/<tunnel>/<node>/<transport>` | آخرین ۱۲۰ نتیجه پروب (زمان، موفق/ناموفق، RTT) برای نمودار ۱۰ دقیقه‌ای در داشبورد |
| `events` | ring buffer ۵۰۰۰ رویداد آخر: زمان UTC، سطح، تانل، پیام، کد خطا |
| `metrics/<tunnel>` | بایت ورودی/خروجی و اتصال‌های فعال (اگر بک‌اند گزارش دهد؛ وگرنه از `ss` شمارش شود) |

**رازها:** `secrets/ca.key`, `ca.crt`, `hub.key`, `hub.crt`, `node.key`, `node.crt`, `join-tokens.json` (توکن‌های یک‌بارمصرف با انقضا)، `backend-tokens/<tunnel>.token` (توکن اشتراکی هر تانل، ۳۲ بایت، هر تانل جدا)، `tls/<tunnel>/` (گواهی تانل). همه 0600، مالک root، و هرگز در لاگ چاپ نمی‌شوند (فیلد‌های حساس با `***` جایگزین می‌شوند).

## ۵. نصب تک‌خطی، Join، آپدیت، حذف، بکاپ

نصب همیشه یک خط است و دوباره زدن همان خط یعنی «تعمیر یا ارتقا»، هیچ‌وقت نصب دوباره از صفر.

**install.sh (حداکثر ۲۵۰ خط، `set -Eeuo pipefail`، فقط این کارها):**

1. چک root، معماری (`uname -m` → amd64/arm64)، وجود systemd، وجود `curl` یا `wget`؛ هر کدام نبود با کد `DEY-I0xx` و راه‌حل یک‌خطی می‌ایستد.
2. منبع دانلود به این ترتیب: `--mirror URL` یا `DEYROUTE_MIRROR` → آدرس پیش‌فرض بیلد (`RELEASE_BASE`، مثلاً CDN مالک) → GitHub Releases. هر منبع ۳ بار با backoff تلاش می‌شود، بعد منبع بعدی.
3. دانلود `deyroute_<ver>_linux_<arch>.tar.gz` + `SHA256SUMS` + `SHA256SUMS.minisig`؛ تأیید checksum اجباری، تأیید امضا (minisign با کلید عمومی داخل نصاب) اجباری مگر `--skip-signature`.
4. نصب اتمیک باینری (`install -m 0755` به فایل موقت، سپس `mv`)، ساخت مسیرهای بخش ۲ با دسترسی درست.
5. اجرای `deyroute setup` (ویزارد) یا حالت غیرتعاملی: `install.sh --role hub --name ir-1 --yes` / `install.sh join 'dey://…'`. فلگ‌ها: `--version`, `--mirror`, `--local /path/file.tar.gz` (آفلاین)، `--no-setup`, `--skip-signature`.
6. پروکسی محیط (`https_proxy`) را محترم می‌شمارد و هرگز چیزی جز باینری DEYROUTE دانلود نمی‌کند.

**setup (ویزارد داخل باینری):** نقش → نام → تشخیص IP عمومی (از `ip route get 1.1.1.1` و در صورت نیاز از Node بعد از Join تأیید می‌شود) → ساخت CA و گواهی Hub → انتخاب `control_port` (پیشنهاد آزاد) → ساخت جدول nftables → پیشنهاد اعمال sysctl profile (پیش‌فرض بله) → نصب و enable سرویس → نمایش Join command. کل ویزارد Hub حداکثر ۵ سؤال دارد.

**GitHub در ایران (مسئله واقعی، راه‌حل اجباری):**

- Hub معمولاً به GitHub دسترسی ندارد یا کند است. بعد از Join اولین Node، **همه دانلودهای Hub از طریق Node انجام می‌شود:** Hub دستور `fetch.proxy(url, sha256)` می‌فرستد، Node فایل را از GitHub می‌گیرد، checksum را چک می‌کند و روی همان استریم mTLS به Hub می‌دهد. این شامل باینری بک‌اندها، نسخه جدید `deyroute` و manifest است.
- برای خودِ نصاب (قبل از هر Node) دو راه پشتیبانی می‌شود: Mirror مالک (پیش‌فرض بیلد) و `--local`. مستند فارسی این را در صفحه اول توضیح می‌دهد.
- Nodeها همیشه باینری `deyroute` را از Hub می‌گیرند (`GET /v1/assets/deyroute/<arch>`) تا نسخه همه یکی بماند. اختلاف نسخه major.minor بین Hub و Node = هشدار در داشبورد + پیشنهاد آپدیت؛ Node با نسخه ناسازگار دستور اجرا نمی‌کند.

**Manifest بک‌اندها (`backends.yaml` داخل باینری + قابل بازنویسی در `/etc/deyroute/backends.yaml`):** برای هر بک‌اند: نسخه pin‌شده، الگوی URL برای هر معماری، sha256 برای هر معماری، نسخه قالب کانفیگ. `deyroute update manifest` نسخه جدید manifest امضاشده را می‌گیرد.

**آپدیت:**

- `deyroute update` (یا منو): چک ریلیز، نمایش changelog، دانلود و تأیید، جایگزینی باینری، restart سرویس Hub/Node. **تانل‌ها نباید قطع شوند**: unitهای بک‌اند مستقل‌اند و لمس نمی‌شوند. باینری قبلی در `/var/lib/deyroute/bin/deyroute.prev`؛ `deyroute update --rollback`.
- `deyroute update backends [name]`: نسخه جدید کنار قبلی نصب می‌شود (Hub و همه Nodeها)، کانفیگ‌ها با قالب جدید رندر و validate می‌شوند، سپس با تأیید کاربر تانل فعال restart می‌شود. اگر پروب ظرف ۶۰ ثانیه سبز نشد، خودکار به نسخه قبلی برمی‌گردد و رویداد `backend_update_rolled_back` ثبت می‌شود.
- آپدیت هیچ‌وقت بی‌سؤال اجرا نمی‌شود؛ فقط «چک کردن» خودکار روزانه (اختیاری، پیش‌فرض خاموش).

**حذف (`deyroute uninstall`):** تأیید با تایپ `yes`؛ stop/disable همه unitها، حذف جدول `inet deyroute`، برگرداندن sysctl از بکاپ `sysctl-before-deyroute.conf`، حذف `/etc/deyroute` و `/var/lib/deyroute` (با سؤال «بکاپ‌ها نگه داشته شوند؟»)، حذف باینری. روی Hub می‌پرسد «Nodeها هم حذف شوند؟» و در صورت بله دستور uninstall به همه Nodeهای آنلاین می‌فرستد. باینری بک‌اندها هم پاک می‌شود.

**بکاپ/ریستور:**

- `deyroute backup [--out FILE] [--no-encrypt]`: `/etc/deyroute` کامل + export رویدادها؛ رمزگذاری با `age` (passphrase) پیش‌فرض.
- قبل از هر `apply` یک بکاپ خودکار در `backups/auto/` (۲۰ تای آخر نگه داشته می‌شود).
- `deyroute restore FILE`: schema را validate می‌کند، در صورت نیاز migrate، کانفیگ‌ها را دوباره رندر و اعمال می‌کند. چون CA در بکاپ است، Nodeها به Hub بازیابی‌شده اعتماد می‌کنند.
- جابه‌جایی Hub به سرور جدید: `deyroute backup` → روی سرور جدید `install.sh --no-setup` → `deyroute restore` → از Hub قدیم `deyroute hub announce-move <new_ip:port>` (به Nodeها آدرس جدید را می‌دهد) یا روی هر Node `deyroute node set-hub <ip:port>`.

## ۶. رابط کاربری (TUI)

با زدن `deyroute` یا `dey` بدون آرگومان، منوی تمام‌صفحه با Bubble Tea باز می‌شود؛ اپراتور باید بتواند فقط با اعداد و Enter همه کار را انجام دهد، و اگر ترمینال UTF-8/رنگ ندارد، همان منو با ASCII ساده و بدون رنگ کار کند.

**بنر (بالای هر صفحه، دقیقاً کلمه DEYROUTE با فونت ASCII بزرگ):**

```text
 ██████╗ ███████╗██╗   ██╗██████╗  ██████╗ ██╗   ██╗████████╗███████╗
 ██╔══██╗██╔════╝╚██╗ ██╔╝██╔══██╗██╔═══██╗██║   ██║╚══██╔══╝██╔════╝
 ██║  ██║█████╗   ╚████╔╝ ██████╔╝██║   ██║██║   ██║   ██║   █████╗
 ██║  ██║██╔══╝    ╚██╔╝  ██╔══██╗██║   ██║██║   ██║   ██║   ██╔══╝
 ██████╔╝███████╗   ██║   ██║  ██║╚██████╔╝╚██████╔╝   ██║   ███████╗
 ╚═════╝ ╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝   ╚══════╝
 DEYROUTE Tunnel Manager  v1.0.0  ·  Hub: ir-1 (5.6.7.8)  ·  Mode: Simple  ·  2 nodes  ·  1 tunnel UP
```

در ترمینال بدون UTF-8، بنر نسخه ASCII (کاراکترهای `#`) با همان متن زیرش نشان داده می‌شود.

**داشبورد (صفحه اول، هر ۲ ثانیه تازه می‌شود):**

```text
 TUNNELS
  #  NAME            NODE (active)   TRANSPORT          STATE   RTT    UP-TIME    PORTS
  1  Main 443/2053   de-1 Germany 1  backhaul/wssmux    ● UP    41ms   3d 04:12   443,2053
  2  Games UDP       nl-1 Nether. 1  hysteria2/udp      ◐ DEGR  188ms  00:03:10   27015/udp
 NODES
  de-1  Germany 1      1.2.3.4   ● online   ctl 39ms   v1.0.0   cpu 3%  ram 121MB
  nl-1  Netherlands 1  9.8.7.6   ● online   ctl 44ms   v1.0.0   cpu 1%  ram  98MB
 LAST EVENTS
  12:41:03  main   switch   backhaul/tcpmux → backhaul/wssmux (failback, primary healthy 5m)
  12:35:58  main   down     backhaul/tcpmux probe failed 3x (timeout)
```

رنگ حالت‌ها: UP سبز، DEGRADED زرد، SWITCHING آبی، DOWN قرمز، DISABLED خاکستری. هرگز فقط با رنگ معنی منتقل نشود؛ همیشه کلمه هم باشد.

**منوی اصلی (شماره‌ها ثابت هستند؛ Advanced فقط آیتم‌های ستاره‌دار را اضافه می‌کند):**

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

**ویزارد Add tunnel در Simple mode (حداکثر ۳ سؤال):**

1. Which node? → لیست Nodeهای آنلاین با شماره (اگر فقط یکی هست، خودکار انتخاب و فقط اعلام می‌شود).
2. Ports? → ورودی آزاد مثل `443,2053,8443` یا `443/tcp,27015/udp` یا بازه `2000-2010`. هر پورت همان لحظه چک می‌شود؛ پورت مشغول با نام پروسه نشان داده می‌شود و پیشنهاد «kill/change/skip» می‌آید.
3. Confirm → خلاصه (node، پورت‌ها، نردبان پیش‌فرض، backup: none). Enter = ساخت.

بعد از ساخت، یک صفحه پیشرفت با مراحل (install backend on hub ✔، install on node ✔، render ✔، firewall ✔، start ✔، probe ✔) و در انتها «Tunnel main is UP via backhaul/wssmux (41ms)». اگر مرحله‌ای شکست خورد همان‌جا کد خطا + راه‌حل + دکمه Retry.

**Add tunnel در Advanced mode:** همان ویزارد + انتخاب/ترتیب نردبان (لیست با جابه‌جایی)، target سفارشی هر پورت، انتخاب Node پشتیبان، thresholdها، TLS mode.

**Add backup node (به هر تانل):** انتخاب تانل → انتخاب Node → سیستم روی Node پشتیبان نردبان را گرم می‌کند و گزارش می‌دهد «backup nl-1 ready (warm)». هشدار ثابت: «Backup only works if the same service runs on both nodes.»

**Ports → Check port:** ورودی پورت → خروجی چهار خط: local bind (free/used by X)، firewall (open/closed)، reachable from node de-1 (yes/no، RTT)، reachable via tunnel (yes/no). این صفحه مهم‌ترین ابزار عیب‌یابی است.

**قواعد UI:**

- هر عمل مخرب (delete، uninstall، rotate tokens) نیاز به تایپ `yes` دارد و قبلش دقیقاً می‌گوید چه چیزی از بین می‌رود.
- هر خطا در UI: یک خط قرمز با کد `DEY-Exxx`، یک خط «Why»، یک خط «Fix»، و مسیر لاگ. هرگز stack trace در UI.
- کلیدها: اعداد + Enter برای انتخاب؛ `q`/Esc برگشت؛ `r` refresh؛ `?` راهنمای همان صفحه. بدون نیاز به موس.
- عرض ترمینال کمتر از ۱۰۰ ستون: جدول‌ها ستون‌های کم‌اهمیت (RTT، UP-TIME) را حذف می‌کنند، نه اینکه بشکنند.
- همه رشته‌ها در `i18n/en.go`؛ متن hard-code در کد منو ممنوع.
- TUI هیچ منطق مدیریتی ندارد؛ هر عمل = یک فراخوانی Local API که همان را CLI (بخش ۱۴) هم انجام می‌دهد.

## ۷. بک‌اندها: اینترفیس پلاگین و مشخصات هر بک‌اند

هر بک‌اند یک پکیج Go در `internal/backend/<name>` است که اینترفیس زیر را پیاده می‌کند و در `init()` خودش را ثبت می‌کند؛ موتور failover، UI و CLI فقط با این اینترفیس کار می‌کنند و هیچ `if backend == "backhaul"` جایی خارج از پکیج خودش مجاز نیست.

```go
package backend

type Side int      // SideHub, SideNode
type Direction int // Reverse = node dials hub; Forward = hub dials node

type Transport struct {
    Backend     string   // "backhaul"
    Name        string   // "wssmux"  → full id "backhaul/wssmux"
    Direction   Direction
    Protos      []string // "tcp", "udp"
    NeedsUDP    bool     // requires UDP reachability between hub and node
    NeedsTLS    bool     // consumes tunnel TLS cert/key from internal CA or ACME
    Stealth     int      // 1..5, used for default ladder ordering and UI hints
}

type RenderInput struct {
    Tunnel      config.Tunnel
    Node        config.Node
    Hub         config.HubInfo
    Transport   Transport
    ControlPort int            // allocated by hub per (tunnel, node, transport)
    Secrets     Secrets        // shared token, TLS paths, keypairs
    Paths       Paths          // binary path, config dir, log file
}

type Rendered struct {
    Files map[string][]byte   // relative to config dir
    Unit  UnitSpec            // ExecStart, WorkingDirectory, Env, extra caps
    Binds []PortUse           // ports this side will bind (conflict check + firewall)
}

type Backend interface {
    Name() string
    Transports() []Transport
    Manifest() ManifestEntry                                   // version, urls, sha256 per arch
    Validate(in RenderInput) error                              // static checks, DEY-Bxxx errors
    Render(in RenderInput, side Side) (Rendered, error)
    Probe(ctx context.Context, in RenderInput) (ProbeResult, error) // optional extra check
}

func Register(b Backend)
func Lookup(id string) (Backend, Transport, error)   // "backhaul/wssmux"
```

**قواعد مشترک همه بک‌اندها:**

- باینری از manifest دانلود و با sha256 تأیید می‌شود؛ در `/var/lib/deyroute/bin/<b>/<ver>/` ذخیره و هرگز overwrite نمی‌شود.
- هر (تانل، Node، ترنسپورت) یک پورت کنترل اختصاصی از بازه `30000-31999` می‌گیرد (پایدار، در state ذخیره، در nftables فقط برای IP همان Node باز).
- توکن/رمز هر تانل جداست (`backend-tokens/<tunnel>.token`، ۳۲ بایت)؛ هر Node پشتیبان همان توکن را می‌گیرد.
- unit از قالب واحد `deyroute-tun@.service` ساخته می‌شود؛ stdout/stderr به `/var/log/deyroute/tunnels/<tunnel>.log` (با `StandardOutput=append:`). هاردنینگ در بخش ۱۱.
- بک‌اند به‌عنوان کاربر سیستمی `deyroute` اجرا می‌شود با `AmbientCapabilities=CAP_NET_BIND_SERVICE`؛ فقط WireGuard/AmneziaWG با root و `CAP_NET_ADMIN`.
- بعد از start، بک‌اند حداکثر ۱۵ ثانیه فرصت دارد پروب را پاس کند؛ وگرنه `DEY-B0xx` با ۴۰ خط آخر لاگ همان بک‌اند.
- نمونه‌کانفیگ‌های زیر «شکل مورد انتظار» هستند؛ نام دقیق کلیدها را با README نسخه pin‌شده تطبیق بده.

**جدول مقایسه (برای انتخاب و ترتیب نردبان):**

| Transport id | جهت | پروتکل | stealth (۱–۵) | سرعت | RAM تقریبی | فاز |
| --- | --- | --- | --- | --- | --- | --- |
| backhaul/wssmux, wsmux, tcpmux, tcp, ws, wss, udp | Reverse | tcp, udp | wss:4 · tcpmux:2 | عالی | 20–50MB | 2 |
| rathole/noise, tls, tcp | Reverse | tcp, udp | noise:3 | عالی | 5–15MB | 2 |
| frp/wss, websocket, quic, kcp, tcp | Reverse | tcp, udp | wss:4 · quic:3 | خوب | 20–40MB | 2 |
| xray/reality | Forward | tcp, udp | 5 | خوب | 30–60MB | 6 |
| hysteria2/udp | Forward | tcp, udp (نیاز به UDP) | 3 | عالی روی لینک با loss | 30–50MB | 6 |
| waterwall/reverse-reality | Reverse | tcp | 5 | خوب | 10–30MB | 6 |
| wireguard/kernel, awg/userspace | Forward | tcp, udp (نیاز به UDP) | kernel:1 · awg:3 | عالی | 5–30MB | 7 |
| direct/native | Forward | tcp, udp | 1 | عالی | داخل deyroute | 2 |
| direct/haproxy | Forward | tcp | 1 | عالی | 10MB | 7 (اختیاری) |
| gost/relay-wss, chisel/wss | Reverse | tcp, udp | 3 | خوب | 20–40MB | 7 (اختیاری) |

**۷.۱ Backhaul (Go، `Musixal/Backhaul`) — بک‌اند اصلی**

- Hub = `[server]`، Node = `[client]`. ترنسپورت‌ها: `tcp`, `tcpmux`, `ws`, `wss`, `wsmux`, `wssmux`, `udp`.
- Hub (نمونه):

```toml
[server]
bind_addr = "0.0.0.0:30001"
transport = "wssmux"
token = "<tunnel-token>"
keepalive_period = 75
nodelay = true
heartbeat = 20
channel_size = 2048
mux_con = 8
mux_version = 1
mux_framesize = 32768
mux_recievebuffer = 4194304
mux_streambuffer = 65536
sniffer = false
web_port = 0
tls_cert = "/etc/deyroute/secrets/tls/main/cert.pem"
tls_key  = "/etc/deyroute/secrets/tls/main/key.pem"
log_level = "info"
ports = ["443=127.0.0.1:443", "2053=127.0.0.1:2053"]
```

- Node (نمونه):

```toml
[client]
remote_addr = "5.6.7.8:30001"
transport = "wssmux"
token = "<tunnel-token>"
connection_pool = 8
aggressive_pool = false
keepalive_period = 75
dial_timeout = 10
nodelay = true
retry_interval = 3
mux_version = 1
mux_framesize = 32768
mux_recievebuffer = 4194304
mux_streambuffer = 65536
sniffer = false
web_port = 0
log_level = "info"
```

- نکته‌ها: `heartbeat` کوتاه (۲۰ ثانیه) برای تشخیص سریع؛ `connection_pool` بر اساس تعداد پورت‌ها (پیش‌فرض ۸، در Advanced قابل تغییر)؛ برای `udp` یک instance جدا با پورت کنترل جدا. اگر کلاینت wss گواهی را verify نمی‌کند، رمزنگاری همچنان برقرار است و احراز هویت با token است؛ این را در `docs/backends/backhaul.md` مستند کن. `web_port` فقط روی `127.0.0.1` و فقط اگر مالک آمار بخواهد.

**۷.۲ Rathole (Rust، `rapiz1/rathole`) — سبک‌ترین**

- Hub = server، Node = client. ترنسپورت‌ها: `tcp`, `tls`, `noise`. پیش‌فرض `noise` (کلید با `rathole --genkey`؛ نیاز به گواهی ندارد).

```toml
# hub
[server]
bind_addr = "0.0.0.0:30002"
default_token = "<tunnel-token>"
heartbeat_interval = 20
[server.transport]
type = "noise"
[server.transport.noise]
pattern = "Noise_NK_25519_ChaChaPoly_BLAKE2s"
local_private_key = "<hub-noise-private>"
[server.services.tcp-443]
type = "tcp"
bind_addr = "0.0.0.0:443"

# node
[client]
remote_addr = "5.6.7.8:30002"
default_token = "<tunnel-token>"
retry_interval = 3
[client.transport]
type = "noise"
[client.transport.noise]
remote_public_key = "<hub-noise-public>"
[client.services.tcp-443]
local_addr = "127.0.0.1:443"
```

- برای `tls` سرور PKCS#12 می‌خواهد؛ Go خودش از cert/key ما فایل `.p12` بسازد (بدون وابستگی به openssl). یک سرویس به‌ازای هر Port map (`tcp-<port>` / `udp-<port>`).

**۷.۳ FRP (Go، `fatedier/frp`)**

- Hub = `frps`، Node = `frpc`. `transport.protocol` در frpc: `tcp`, `kcp`, `quic`, `websocket`, `wss`. TLS اجباری (`transport.tls.force = true` روی frps) و frpc با `transport.tls.trustedCaFile` به CA داخلی ما pin می‌شود.

```toml
# frps.toml (hub)
bindPort = 30003
quicBindPort = 30003
auth.method = "token"
auth.token = "<tunnel-token>"
transport.tls.force = true
transport.tls.certFile = "/etc/deyroute/secrets/tls/main/cert.pem"
transport.tls.keyFile = "/etc/deyroute/secrets/tls/main/key.pem"
transport.maxPoolCount = 32
transport.tcpMux = true
allowPorts = [{ start = 443, end = 443 }, { start = 2053, end = 2053 }]

# frpc.toml (node)
serverAddr = "5.6.7.8"
serverPort = 30003
auth.method = "token"
auth.token = "<tunnel-token>"
loginFailExit = false
transport.protocol = "wss"
transport.tls.enable = true
transport.tls.trustedCaFile = "/etc/deyroute/secrets/ca.crt"
transport.poolCount = 8
transport.tcpMux = true
[[proxies]]
name = "tcp-443"
type = "tcp"
localIP = "127.0.0.1"
localPort = 443
remotePort = 443
```

**۷.۴ Waterwall (C، `radkesvat/WaterWall`) — مقاوم‌ترین در برابر DPI**

- ترنسپورت `reverse-reality`: روی Hub یک شنونده پورت کنترل با `RealityServer` (یک SNI واقعی به‌عنوان decoy) که پشت آن `ReverseServer` نشسته و پورت‌های کاربر را می‌گیرد؛ روی Node `ReverseClient` → `RealityClient` → `TcpConnector` به Hub، و مقصد نهایی `TcpConnector` به `127.0.0.1:<port>`.
- قالب کانفیگ را **دقیقاً از نمونه رسمی «reverse reality» مخزن در نسخه pin‌شده** بگیر و فقط این‌ها را جایگزین کن: IP/پورت Hub، پورت‌های کاربر، `password`، `sni` (decoy). `core.json` با `ram-profile: server` و تعداد thread = min(4, CPU).
- decoy SNI: لیست پیش‌فرض ۳ دامنه TLS1.3 که از ایران در دسترس‌اند (مالک لیست را می‌دهد)؛ قبل از فعال‌سازی، Hub دسترسی به decoy را تست می‌کند و اولین سالم را انتخاب می‌کند.
- Waterwall خطاهایش را خوب توضیح نمی‌دهد: قبل از start، JSON را validate کن، در اولین اجرا `log-level: debug` بگذار و ۴۰ خط آخر لاگ را در خطای `DEY-B04x` نشان بده. `WorkingDirectory` باید مسیر `core.json` باشد.

**۷.۵ Xray-Reality relay (Go، `XTLS/Xray-core`) — Forward، شبیه TLS واقعی**

- Hub: یک `dokodemo-door` inbound به‌ازای هر پورت (`listen 0.0.0.0`, `port`, `network: tcp` یا `tcp,udp`, `settings.address = target host`, `settings.port = target port`) → outbound `vless` به `node_ip:<ctl>` با `flow: xtls-rprx-vision`, `security: reality` (`serverName` = decoy, `fingerprint: chrome`, `publicKey`, `shortId`).
- Node: inbound `vless` روی `0.0.0.0:<ctl>` با `decryption: none`, یک client با UUID تانل و `flow: xtls-rprx-vision`, `realitySettings: {dest: "<decoy>:443", serverNames: [decoy], privateKey, shortIds}` → outbound `freedom`.
- **الزام امنیتی:** routing روی Node فقط مقصدهای `127.0.0.0/8` و targetهای تعریف‌شده را به `freedom` بدهد و بقیه را `block` کند؛ وگرنه Node یک پروکسی باز می‌شود.
- کلیدها با `xray x25519` هنگام ساخت تانل؛ Mux خاموش (با vision سازگار نیست). UDP از طریق dokodemo با `network: udp` پشتیبانی می‌شود.

**۷.۶ Hysteria2 (Go، `apernet/hysteria`) — Forward، QUIC، فقط با UDP باز**

- Node = server: `listen: :<ctl>`، TLS با گواهی self-signed تانل، `auth.password`، `obfs.salamander` (رمز جدا)، `masquerade` اختیاری.
- Hub = client: `server: node_ip:<ctl>` (یا با port hopping `node_ip:20000-20999` که روی Node با nftables DNAT به `<ctl>` می‌رسد)، `tls.pinSHA256` گواهی Node، `bandwidth: {up, down}` (مالک در Advanced می‌دهد؛ پیش‌فرض ۱۰۰/۱۰۰ Mbps)، `tcpForwarding: [{listen: 0.0.0.0:443, remote: 127.0.0.1:443}]`, `udpForwarding` برای udp، `fastOpen: true`.
- در نردبان فقط وقتی فعال است که پروب UDP (بخش ۱۰) پاس شده باشد؛ اگر بعداً UDP بسته شد، پروب معمولی آن را می‌اندازد و failover طبیعی رخ می‌دهد.

**۷.۷ WireGuard / AmneziaWG — Forward، L3**

- دو ترنسپورت: `wireguard/kernel` (سریع‌ترین، بدون obfuscation، نیاز به ماژول کرنل که در همه توزیع‌های Tier 1 هست) و `awg/userspace` (باینری `amneziawg-go` + پارامترهای Jc/Jmin/Jmax/S1/S2/H1–H4 تصادفی‌شده هنگام ساخت؛ بدون ماژول کرنل).
- شبکه `10.77.<n>.0/30` به‌ازای هر تانل (Hub `.1`، Node `.2`)، `PersistentKeepalive = 25`، Node روی UDP `<ctl>` گوش می‌دهد.
- forward پورت‌ها روی Hub با nftables در جدول `inet deyroute`: `prerouting dnat` هر `listen` به `10.77.n.2:<target port>` و `postrouting masquerade` روی اینترفیس `dey-<tunnel>`؛ `net.ipv4.ip_forward=1`. سرویس روی Node IP کلاینت را نمی‌بیند (IP Hub را می‌بیند) — در UI اعلام شود.
- اختیاری فاز ۷: پوشش `udp2raw`/`phantun` وقتی UDP throttle می‌شود.

**۷.۸ Direct**

- `direct/native`: رله TCP/UDP داخل خودِ `deyroute` (پروسه `deyroute relay --tunnel <id>` در unit جدا) با `io.Copy`/splice، بدون هیچ وابستگی؛ Hub → `node_ip:<target>`. سریع‌ترین و ساده‌ترین، بدون پوشش؛ آخرین پله نردبان.
- `direct/haproxy` (اختیاری، فاز ۷): همان با HAProxy `mode tcp`، `option tcp-check`، و `send-proxy` وقتی سرویس Node `acceptProxyProtocol` دارد تا IP واقعی کاربر حفظ شود.

**۷.۹ Gost و Chisel (اختیاری، فاز ۷)**

- Gost v3: Hub `gost -L relay+wss://:<ctl>?bind=true` با TLS؛ Node `gost -L rtcp://:443/127.0.0.1:443 -F relay+wss://user:pass@hub:<ctl>`.
- Chisel: Hub `chisel server --reverse --port <ctl> --tls-key … --tls-cert … --auth user:pass`; Node `chisel client --auth user:pass https://hub:<ctl> R:0.0.0.0:443:127.0.0.1:443`.
- هر دو فقط اگر مالک بخواهد فعال می‌شوند؛ در نردبان پیش‌فرض نیستند.

## ۸. نردبان ترنسپورت پیش‌فرض و منطق انتخاب

نردبان پیش‌فرض `default` از سریع‌ترینِ پنهان به پنهان‌ترینِ کند می‌رود و با `direct/native` تمام می‌شود؛ مالک در Advanced می‌تواند ترتیب را عوض کند، پله‌ای را حذف کند یا نردبان جدید بسازد و به تانل بدهد.

**ترتیب پیش‌فرض و دلیل هر پله:**

1. `backhaul/wssmux` — TLS + WebSocket + mux: شبیه HTTPS، اتصال‌های کم، سریع. اگر `domain` تنظیم شده باشد گواهی ACME واقعی دارد و ظاهرش کامل‌تر است.
2. `backhaul/tcpmux` — بدون TLS ولی mux؛ کمترین overhead، برای وقتی TLS-fingerprint تانل مشکل‌ساز شده.
3. `rathole/noise` — پروتکل و باینری متفاوت (Rust)، رمزنگاری Noise؛ اگر فیلترینگ الگوی Backhaul را شناخت، این جایگزین ارزان است.
4. `frp/wss` — بک‌اند سوم با الگوی ترافیکی متفاوت.
5. `xray/reality` — Forward، ظاهر TLS 1.3 کاملاً واقعی با decoy؛ پنهان‌ترین گزینه بدون UDP.
6. `hysteria2/udp` — QUIC؛ فقط اگر UDP باز است. روی لینک‌های پرافت بهترین throughput.
7. `waterwall/reverse-reality` — Reverse با ظاهر Reality؛ آخرین خط دفاع پنهان.
8. `direct/native` — بدون پوشش؛ فقط برای اینکه سرویس نخوابد.

**قواعد انتخاب و گرم‌سازی:**

- هنگام ساخت تانل، همه پله‌های نردبان روی Hub و روی همه Nodeهای آن تانل «گرم» می‌شوند (باینری، کانفیگ، unit). پله‌ای که Validate آن شکست بخورد (مثلاً UDP بسته) با هشدار زرد از نردبان **همان تانل** کنار گذاشته می‌شود؛ هر ۳۰ دقیقه دوباره تست و در صورت پاس شدن برمی‌گردد.
- در Simple mode مالک نردبان را نمی‌بیند؛ فقط در داشبورد می‌بیند تانل روی کدام پله است.
- `Switch transport` دستی در منو همیشه مجاز است (برای تست)؛ سوییچ دستی، شمارنده «switches per hour» را مصرف نمی‌کند.
- Failback همیشه به **پله ۱** برمی‌گردد، نه به پله قبلی (بخش ۹).
- یک تانل UDP-only (مثل بازی) نردبان پیش‌فرض خودش را دارد: `backhaul/udp` → `hysteria2/udp` → `wireguard/kernel` → `direct/native`. Renderer پله‌هایی که پروتکل تانل را پشتیبانی نمی‌کنند خودکار حذف می‌کند.

**راهنمای UI برای مالک (در صفحه Failover → Ladder نشان داده شود):**

| اگر … | پیشنهاد |
| --- | --- |
| سرعت مهم‌تر از پنهان بودن است | `tcpmux` را اول بگذار |
| فیلترینگ TLS تانل را می‌شناسد | `xray/reality` را بالا بیاور |
| UDP در دیتاسنتر ایران باز است | `hysteria2/udp` را دوم بگذار |
| Node فقط سرویس‌های TCP دارد | پله‌های udp را حذف کن |

## ۹. موتور سلامت و Failover

هدف عددی: از لحظه‌ای که فیلترینگ ترنسپورت فعال را می‌بندد تا وقتی تانل روی پله بعدی UP است حداکثر **۳۵ ثانیه** (p95) بگذرد و قطعی محسوس برای کاربر هنگام سوییچ حداکثر **۳ ثانیه** باشد.

&#91;embedded content: ماشین حالت هر تانل · ۷ حالت\]

هر تانل دقیقاً یکی از این حالت‌ها را دارد و هر انتقال با رویداد و دلیل پروب ثبت می‌شود؛ جزئیات هر انتقال در ادامه.

**پروب‌ها (هر تانل، فقط ترنسپورت فعال):**

| پروب | کجا اجرا می‌شود | چه می‌کند | معنی شکست |
| --- | --- | --- | --- |
| `path` (اصلی) | Hub | به `127.0.0.1:<listen>` خودش وصل می‌شود، از داخل تانل به target روی Node می‌رسد؛ حالت `auto`: TLS ClientHello می‌فرستد و هر پاسخی (ServerHello یا alert) = موفق؛ اگر سرویس TLS نیست، «هر بایت یا بستن تمیز» = موفق. RTT ثبت می‌شود | مسیر تانل خراب است |
| `node_service` | Node | `probe.tcp 127.0.0.1:<target>` | سرویس روی Node خوابیده؛ سوییچ ترنسپورت بی‌فایده است |
| `control` | Hub | heartbeat اتصال کنترل Node | Node یا شبکه‌اش از دسترس خارج شده |
| `backend` (اختیاری) | Hub | آمار داخلی بک‌اند اگر بدهد (مثلاً web\_port) | فقط اطلاعات؛ تصمیم‌ساز نیست |

نوع پروب هر Port map: `auto | tcp | tls | http` (Advanced). پروب فقط روی **اولین** پورت TCP هر تانل اجرا می‌شود مگر مالک پورت پروب را عوض کند؛ پروب همه پورت‌ها هر ۶۰ ثانیه فقط برای گزارش.

**پیش‌فرض‌ها (قابل تغییر در Advanced):** `probe_interval_s: 5`, `probe_timeout_s: 3`, `fail_threshold: 3`, `recover_threshold: 6`, `failback: true`, `failback_after_s: 300`, `max_switches_per_hour: 6`, `quarantine_s: 600`.

**ماشین حالت هر تانل (یک goroutine به ازای هر تانل، مدل actor، هر انتقال در bbolt ذخیره):**

- `INIT → STARTING`: unitها به ترتیب «سمت سرورِ بک‌اند اول، سمت کلاینت بعد» start می‌شوند (Reverse: اول Hub بعد Node؛ Forward: اول Node بعد Hub).
- `STARTING → UP`: اولین پروب موفق ظرف ۱۵ ثانیه. وگرنه `→ SWITCHING`.
- `UP → DEGRADED`: ۱ یا ۲ شکست متوالی، یا RTT بیش از ۳ برابر میانه ۱۰ دقیقه گذشته برای ۶۰ ثانیه. فقط زرد می‌شود، کاری نمی‌کند.
- `DEGRADED → UP`: یک پروب موفق. `DEGRADED → SWITCHING`: رسیدن به `fail_threshold` شکست متوالی.
- `SWITCHING`: کاندید بعدی طبق policy انتخاب می‌شود → stop unit فعال (Hub و Node) → start کاندید → حداکثر ۱۵ ثانیه انتظار پروب → موفق: `UP` + رویداد `switch`؛ ناموفق: کاندید `quarantine` می‌شود و کاندید بعدی. اگر هیچ کاندیدی نماند: `DOWN`.
- `DOWN`: هر ۳۰ ثانیه (با backoff تا ۵ دقیقه) کل نردبان از پله ۱ دوباره تلاش می‌شود. `direct/native` هیچ‌وقت quarantine نمی‌شود.
- `PAUSED`: مالک failover را متوقف کرده (تعمیرات)؛ پروب ادامه دارد، سوییچ نه.

**انتخاب کاندید (policy):**

- `transport_then_node` (پیش‌فرض): پله بعدیِ نردبان روی همان Node؛ وقتی همه پله‌های آن Node در این چرخه امتحان شدند → Node بعدی از پله ۱. Node بعدی نبود → `DOWN`.
- `transport_only`: فقط پله‌ها، Node ثابت.
- `node_only`: فقط Node بعدی با همان پله (وقتی ترنسپورت خوب است و مشکل از سرور خارج است).
- قبل از سوییچ ترنسپورت، اگر `node_service` می‌گوید سرویس روی Node خوابیده: ترنسپورت عوض **نمی‌شود**؛ اگر Node پشتیبان هست مستقیم به آن می‌رود، وگرنه `DEGRADED (service down)` با رویداد مخصوص می‌ماند.
- اگر `control` قطع است ولی `path` سالم است: هیچ سوییچی انجام نمی‌شود (تانل کار می‌کند؛ فقط هشدار `node_offline`).

**Failback:**

- فاز ۵ (اجباری): وقتی تانل روی پله/Node غیراصلی `failback_after_s` ثانیه پایدار UP بود، به پله ۱ روی Node اصلی سوییچ می‌کند (قطعی ≤ ۳ ثانیه). اگر ظرف ۱۵ ثانیه پروب پاس نشد، فوراً به پله قبلی برمی‌گردد و `failback_after_s` برای این تانل دو برابر می‌شود (سقف ۲۴ ساعت، در state ذخیره، با موفقیت failback ریست).
- فاز ۸ (بهبود): **Canary**. برای پله ۱ یک unit دوم `deyroute-tun@<tunnel>.canary` رندر می‌شود که فقط پورت کنترل + یک پورت loopback (`127.0.0.1:<canary>` → echo داخلی `deyroute-node`) دارد و هیچ پورت کاربری bind نمی‌کند. هنگام failover فقط این canary روشن می‌ماند؛ وقتی پروب canary ۶ بار متوالی پاس شد failback واقعی انجام می‌شود. نتیجه: هیچ failback کورکورانه‌ای.

**ضد نوسان:** بیش از `max_switches_per_hour` سوییچ خودکار → تانل روی وضعیت فعلی می‌ماند (اگر UP) یا به `direct/native` می‌رود و رویداد `flapping` می‌فرستد؛ سوییچ دستی همیشه مجاز است. quarantine هر کاندید شکست‌خورده از ۱۰ دقیقه شروع و تا ۱ ساعت دو برابر می‌شود.

**Failover سرور (Node پشتیبان):** سوییچ Node یعنی همان پورت‌ها به Node بعدی می‌روند؛ همه نشست‌های کاربر قطع و دوباره برقرار می‌شوند؛ این طبیعی است. سرویس روی Node پشتیبان باید همان کاربران/کانفیگ را داشته باشد (مسئولیت مالک؛ UI هشدار ثابت می‌دهد). Node وقتی «خراب» است که: `control` بیش از ۳۰ ثانیه قطع **و** `path` شکست‌خورده، یا `node_service` بیش از threshold خوابیده، یا همه پله‌هایش سوخته‌اند.

**رویدادها (نام‌ها ثابت):** `tunnel_up`, `tunnel_degraded`, `tunnel_down`, `switch_transport`, `switch_node`, `failback`, `failback_failed`, `flapping`, `node_online`, `node_offline`, `service_down`, `backend_crash`, `probe_error`, `update_applied`, `update_rolled_back`. هر رویداد: زمان UTC، تانل، Node، ترنسپورت قبلی/جدید، دلیل (متن پروب)، کد خطا. تلگرام: یک پیام به ازای هر رویداد، rate-limit ۱ پیام/۶۰ ثانیه برای هر (تانل، نوع).

**بازیابی بعد از restart Hub:** state از bbolt خوانده، وضعیت واقعی unitها با `systemctl show` تطبیق داده می‌شود (reconcile)؛ اگر unit فعال با state نمی‌خواند، state اصلاح می‌شود، نه اینکه تانل سالم restart شود.

**دستورهای دستی:** `pause`/`resume` failover، `switch transport <id>`، `switch node <id>`، `reset` (برو پله ۱ همین حالا)، `test ladder` (همه پله‌ها را یکی‌یکی ۲۰ ثانیه امتحان کن و گزارش RTT/موفقیت بده — فقط وقتی مالک خواست، چون قطعی دارد).

## ۱۰. پورت‌ها، فایروال و TLS خودِ تانل

هیچ پورتی بدون چک چهارمرحله‌ای (bind محلی، فایروال، دسترسی از Node، دسترسی از داخل تانل) به تانل اضافه نمی‌شود و هیچ خطای TLS نباید به کاربر برسد، چون TLS کاربر را تانل لمس نمی‌کند و TLS تانل را خودمان می‌سازیم.

**Port checker (کتابخانه `internal/ports`، بدون اجرای shell خارجی جز `ss` به‌عنوان fallback):**

1. **Bind محلی:** با `net.Listen` روی `0.0.0.0:<port>` و `[::]:<port>` (tcp و udp جداگانه). مشغول بود → از `/proc/net/tcp*`+`/proc/<pid>/fd` (یا `ss -Hlntup`) نام و PID پروسه پیدا می‌شود و نمایش: `443/tcp is used by nginx (pid 1234)`. گزینه‌ها: `Change port`, `Skip`, `Stop that service (only if it is a deyroute unit)`. سرویس غیر‌deyroute هرگز خودکار kill نمی‌شود.
2. **فایروال:** تشخیص ترتیبی: `nftables` (جدول‌های دیگر و `inet deyroute`)، `ufw status`, `firewalld` (`firewall-cmd --state`), iptables خام. اگر فایروال خارجی پورت را می‌بندد، **پیشنهاد** باز کردن با دستور دقیق نمایش داده می‌شود و فقط با تأیید اجرا می‌شود (`ufw allow 443/tcp`، `firewall-cmd --permanent --add-port=443/tcp && --reload`). جدول `inet deyroute` همیشه توسط ما مدیریت می‌شود: `accept` برای listen portها، `accept` برای control portها فقط از `@nodes`.
3. **دسترسی از بیرون:** به Node دستور `port.check_from_outside(hub_public_ip, port)` داده می‌شود؛ نتیجه با RTT. (نشان می‌دهد پورت از اینترنت باز است؛ فیلترینگ داخل ایران را نمی‌سنجد و این در UI گفته می‌شود.)
4. **دسترسی از داخل تانل:** همان پروب `path` بخش ۹.

**قواعد پورت:**

- پیشنهاد آزاد: اول از لیست سازگار با Cloudflare (`443, 2053, 2083, 2087, 2096, 8443`)، بعد `1024–65535` تصادفی؛ هرگز `22`، `control_port`، بازه کنترل بک‌اندها (`30000-31999`) و پورت‌های در حال استفاده.
- ورودی پورت در UI: `443`, `443/udp`, `443,2053`, `2000-2010`, `443:8443` (listen:target). تا ۶۴ Port map در هر تانل؛ بیشتر = خطا با پیشنهاد استفاده از بازه.
- تغییر پورت روی تانل فعال = رندر دوباره + restart ترنسپورت فعال (قطعی ≤ ۳ ثانیه) و اعلام قبل از تأیید.
- IPv6: اگر Hub IPv6 عمومی دارد، listen روی هر دو؛ Node می‌تواند فقط IPv4 باشد.

**پروب UDP (برای پله‌های نیازمند UDP):** Hub یک بسته UDP با nonce به `deyroute-node` روی `<ctl>/udp` می‌فرستد و echo می‌گیرد؛ ۳ تلاش، timeout ۲ ثانیه. نتیجه در state و هر ۳۰ دقیقه تکرار.

**TLS خودِ تانل — سه حالت (`tunnels[].tls.mode`):**

| حالت | چه می‌شود | کی |
| --- | --- | --- |
| `auto` (پیش‌فرض) | CA داخلی (Ed25519) در setup ساخته می‌شود؛ برای هر تانل گواهی ECDSA P-256 با SAN = IP Hub و (اگر باشد) `domain`، اعتبار ۳ سال، تمدید خودکار ۳۰ روز مانده به انقضا؛ Node به CA pin است (fingerprint در join) | همیشه کار می‌کند، بدون دامنه |
| `acme` | اگر `hub.domain` تنظیم و DNS به IP Hub اشاره کند (DNS-only، بدون پروکسی Cloudflare): Let's Encrypt با HTTP-01 روی پورت ۸۰ (اگر آزاد) یا DNS-01 با توکن Cloudflare (Advanced)؛ تمدید خودکار؛ گواهی به بک‌اندهایی که TLS می‌خواهند داده می‌شود و بعد از تمدید، ترنسپورت فعال با اعلان restart می‌شود | ظاهر HTTPS واقعی برای wss/tls |
| `custom` | مالک مسیر cert/key می‌دهد؛ validate می‌شود (زنجیره، انقضا، تطبیق کلید) | وقتی گواهی از جای دیگر است |

قواعد TLS: فقط TLS 1.3 (و 1.2 فقط اگر بک‌اندی اجبار کند)، کلید خصوصی 0600، هر تانل گواهی جدا، `deyroute security tls show` انقضا و fingerprint را نشان می‌دهد، ۱۴ روز مانده به انقضا هشدار زرد در داشبورد. ACME که شکست بخورد، خودکار به `auto` برمی‌گردد و رویداد `acme_failed` می‌دهد؛ تانل نمی‌خوابد.

**TLS کاربر (سرویس واقعی):** تانل هرگز آن را terminate نمی‌کند. برای Port map با `proto: tcp` بایت‌ها خام رد می‌شوند؛ SNI/ALPN/ws-path همان است که روی Node تنظیم شده. اگر مالک PROXY protocol بخواهد (IP واقعی کاربر در پنل)، فقط `direct/haproxy` و Xray با `acceptProxyProtocol` این را می‌دهند؛ در UI کنار هر ترنسپورت `client IP: preserved / masked` نشان داده شود.

## ۱۱. امنیت

اصل: هر چیزی که از بیرون به Hub می‌رسد باید یا ترافیک کاربر روی پورت تانل باشد یا اتصال mTLS از یک Node شناخته‌شده؛ هیچ پورت مدیریتی برای عموم باز نیست و هیچ رازی روی دیسک بدون 0600 و در لاگ‌ها نیست.

**احراز هویت و کانال کنترل:**

- CA داخلی Ed25519 در setup؛ گواهی Hub و هر Node از همین CA. Node فقط با client cert می‌تواند به Control API وصل شود (mTLS، TLS 1.3، ALPN `deyroute/1`).
- Join token: ۳۲ بایت از `crypto/rand`، base64url، یک‌بارمصرف، انقضا ۱۵ دقیقه، حداکثر ۵ تلاش ناموفق در ساعت از یک IP (بعدش IP ۱ ساعت بلاک). توکن مصرف‌شده فوراً از `join-tokens.json` حذف می‌شود.
- Node CA را از fingerprint داخل لینک join تأیید می‌کند (pinning)؛ TLS سیستم اعتماد نمی‌شود.
- `deyroute security rotate-tokens`: توکن بک‌اند یک تانل یا همه تانل‌ها عوض و با یک restart هماهنگ روی Hub و Nodeها اعمال می‌شود. `rotate-ca` (Advanced) گواهی همه Nodeها را دوباره صادر می‌کند در حالی که آنلاین‌اند؛ Node آفلاین بعداً با join دوباره.

**فایروال (جدول `inet deyroute`، فقط این جدول):**

```text
table inet deyroute {
  set nodes { type ipv4_addr; }           # public IPs of joined nodes
  chain input { type filter hook input priority -10;
    tcp dport 44433 ip saddr @nodes accept      # control API
    tcp dport 44433 drop
    tcp dport 30000-31999 ip saddr @nodes accept # backend control ports
    udp dport 30000-31999 ip saddr @nodes accept
    tcp dport 30000-31999 drop
    udp dport 30000-31999 drop
    tcp dport { 443, 2053 } accept              # active tunnel listen ports (rendered)
  }
}
```

- اگر مالک `security.firewall_managed: false` بگذارد، فقط پیشنهاد دستورات نمایش داده می‌شود.
- اگر IP یک Node عوض شد، Node با گواهی معتبرش از IP جدید وصل می‌شود؛ Hub بعد از تأیید گواهی، IP جدید را در `@nodes` می‌گذارد (بدون دخالت مالک) و رویداد `node_ip_changed`.

**هاردنینگ systemd (قالب `deyroute-tun@.service`؛ به‌جز WireGuard که root است):**

```ini
[Service]
User=deyroute
Group=deyroute
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
SystemCallFilter=@system-service
SystemCallArchitectures=native
ReadWritePaths=/var/log/deyroute
LimitNOFILE=1048576
Restart=always
RestartSec=2
StartLimitIntervalSec=0
StandardOutput=append:/var/log/deyroute/tunnels/%i.log
StandardError=append:/var/log/deyroute/tunnels/%i.log
```

هر گزینه هاردنینگ که بک‌اندی را می‌شکند (مثلاً `MemoryDenyWriteExecute` برای باینری‌های خاص) فقط در `UnitSpec` همان بک‌اند با دلیل مستند حذف می‌شود، نه در قالب مشترک. `deyroute-hub.service` با root (نیاز به nftables و systemctl) ولی با `ProtectHome`, `PrivateTmp`, `NoNewPrivileges`.

**باینری و زنجیره تأمین:**

- ریلیزها با `goreleaser`، `SHA256SUMS` + امضای minisign؛ کلید عمومی minisign داخل نصاب و داخل باینری (برای self-update) hard-code.
- باینری بک‌اندها: sha256 داخل manifest امضاشده؛ دانلود بدون تطبیق = رد و `DEY-S0xx`. هیچ‌وقت `curl | sh` بک‌اند بیرونی.
- `go.sum` قفل، `govulncheck` در CI، وابستگی‌ها حداقل.

**رازها و لاگ:** همه فایل‌های `secrets/` 0600 و مالک root؛ توکن‌ها و کلیدها در هیچ لاگی، خطایی یا خروجی doctor نمی‌آیند (فیلتر مرکزی در `internal/log` که الگوهای token/key/password را `***` می‌کند). سوکت Local API 0600؛ فقط root می‌تواند منو را باز کند (v1؛ نقش اپراتور با دسترسی محدود = بخش ۱۹).

**Node به‌عنوان پروکسی باز نشود:** هر بک‌اند Forward روی Node فقط به مقصدهای `127.0.0.0/8` و targetهای تعریف‌شده در تانل اجازه اتصال می‌دهد (Xray routing block، relay داخلی allow-list، Hysteria2 `acl`). تست اجباری در بخش ۱۷.

**بعد از هر تغییر امنیتی:** `deyroute security audit` لیست را چاپ می‌کند: پورت‌های باز عمومی، دسترسی فایل‌ها، انقضای گواهی‌ها، Nodeهایی که نسخه قدیمی دارند، توکن‌های join منقضی‌نشده.

## ۱۲. بهینه‌سازی کرنل و بودجه منابع

sysctl فقط با تأیید مالک، فقط در فایل `/etc/sysctl.d/99-deyroute.conf`، با بکاپ مقادیر قبلی در `/var/lib/deyroute/sysctl-before-deyroute.conf` و قابل برگشت با یک دستور.

**پروفایل `balanced` (پیش‌فرض، Hub و Node):**

```text
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535
net.core.netdev_max_backlog = 32768
net.ipv4.ip_local_port_range = 10240 65535
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_tw_reuse = 1
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_intvl = 30
net.ipv4.tcp_keepalive_probes = 5
net.ipv4.tcp_fastopen = 3
net.ipv4.tcp_mtu_probing = 1
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216
net.ipv4.udp_rmem_min = 16384
net.ipv4.udp_wmem_min = 16384
fs.file-max = 2097152
net.ipv4.ip_forward = 1              # only when a wireguard/awg transport exists
```

`aggressive` همان با بافرهای ۶۴MB و `tcp_notsent_lowat`; فقط برای سرورهای با RAM ≥ 4GB پیشنهاد می‌شود. `off` هیچ تغییری نمی‌دهد. BBR اگر ماژول نبود (`tcp_available_congestion_control`)، هشدار و skip؛ نه خطا. `ulimit` سرویس‌ها از unit (`LimitNOFILE=1048576`) می‌آید، نه از `limits.conf`.

**بودجه منابع (معیار پذیرش، روی سرور ۱ vCPU / ۱GB):**

| مورد | حد |
| --- | --- |
| RAM `deyroute-hub` در حالت idle با ۳ تانل و ۲ Node | ≤ 60MB RSS |
| RAM `deyroute-node` idle | ≤ 35MB RSS |
| CPU Hub idle (پروب هر ۵ ثانیه) | ≤ 1% میانگین ۵ دقیقه |
| RAM یک ترنسپورت گرم (stopped) | 0 (پروسه‌ای در حال اجرا نیست) |
| RAM ترنسپورت فعال | طبق جدول بخش ۷؛ Backhaul با ۵۰۰ اتصال همزمان ≤ 150MB |
| حجم لاگ | rotate در 20MB، نگه‌داری ۵ فایل، فشرده |
| bbolt | ≤ 50MB؛ رویدادها ring buffer، پروب‌ها ۱۲۰ نمونه در هر کلید |
| اندازه باینری `deyroute` | ≤ 30MB (بدون UPX) |

**قواعد کارایی:** رله داخلی از `io.Copy` با `splice` (Go خودش انجام می‌دهد) و بافر ۳۲KB؛ هیچ polling سنگین‌تر از یک `systemctl show` هر ۵ ثانیه برای هر unit فعال؛ پروب‌ها همزمان با worker pool اندازه ۸؛ `GOGC=100`، `GOMEMLIMIT` برابر ۱۲۸MB برای Hub. بدون CGO، بدون goroutine leak (تست با `goleak`).

## ۱۳. خطاها، لاگ و دستور doctor

هر خطا یک کد ثابت `DEY-<حرف><سه رقم>` دارد و همیشه با سه خط نمایش داده می‌شود: چه شد، چرا، چطور درست کنیم؛ مالک باید بتواند همان سه خط را کپی کند و بفرستد تا مشکل بدون سؤال اضافه حل شود.

**فرمت خطا (UI و CLI یکسان):**

```text
✖ DEY-P012  Port 443/tcp is already in use
  Why:  nginx (pid 1234) is listening on 0.0.0.0:443
  Fix:  choose another port, or stop nginx: systemctl stop nginx
  Log:  /var/log/deyroute/hub.log (search DEY-P012)
```

**دسته‌های کد (هر دسته در `internal/errors/codes.go` با پیام، Why و Fix ثابت؛ افزودن کد جدید بدون این سه فیلد در CI رد می‌شود):**

| پیشوند | حوزه | نمونه‌ها |
| --- | --- | --- |
| `DEY-I0xx` | نصاب/محیط | I001 not root · I002 no systemd · I003 unsupported arch · I004 download failed (all sources) · I005 checksum mismatch · I006 signature invalid |
| `DEY-C0xx` | کانفیگ | C001 unknown key · C002 duplicate id · C003 duplicate listen port · C004 invalid target · C005 unknown transport · C006 schema migration failed |
| `DEY-N0xx` | Node/کانال کنترل | N001 join token invalid/expired · N002 CA fingerprint mismatch · N003 node offline · N004 version incompatible · N005 command timeout |
| `DEY-P0xx` | پورت/فایروال | P010 port invalid · P012 port in use · P013 firewall blocks port · P014 not reachable from node · P015 udp blocked |
| `DEY-T0xx` | TLS | T001 cert expired · T002 key mismatch · T003 acme challenge failed · T004 domain does not resolve to hub |
| `DEY-B0xx` | بک‌اند | B001 binary download failed · B002 render failed · B003 unit failed to start (با ۴۰ خط لاگ) · B004 probe timeout after start · B040-B049 خطاهای خاص Waterwall |
| `DEY-F0xx` | failover | F001 all candidates exhausted · F002 flapping limit reached · F003 failback failed |
| `DEY-S0xx` | امنیت/آپدیت | S001 manifest signature invalid · S002 secrets permission wrong · S003 update rollback performed |
| `DEY-X0xx` | داخلی | X001 state db corrupt (با بازیابی خودکار از بکاپ) · X002 systemctl not found |

**لاگ:**

- فرمت JSON خطی (`slog`) با فیلدهای ثابت: `ts`, `level`, `component`, `tunnel`, `node`, `transport`, `code`, `msg`, `err`. فایل‌ها در بخش ۲؛ rotation داخلی (20MB × 5، gzip).
- `DEYROUTE_DEBUG=1` یا `deyroute --debug`: سطح debug + خروجی به stderr هم.
- لاگ بک‌اندها جدا در `tunnels/<id>.log`؛ `deyroute logs <tunnel> -f` هم‌زمان لاگ Hub و Node (Node لاگش را روی کانال کنترل استریم می‌کند) را با پیشوند `[hub]`/`[node]` نشان می‌دهد.
- فیلتر مرکزی رازها (بخش ۱۱) روی همه خروجی‌ها.

**`deyroute doctor` (مهم‌ترین ابزار پشتیبانی):**

1. جمع‌آوری: OS/kernel/arch، نسخه deyroute و بک‌اندها، وضعیت همه unitها (`systemctl status` خلاصه)، ۲۰۰ خط آخر هر لاگ، جدول پورت‌ها و نتیجه چک چهارمرحله‌ای هر Port map، خروجی `nft list table inet deyroute`، sysctl فعلی، وضعیت Nodeها و RTT کنترل، نتیجه پروب همه پله‌های نردبان (فقط پله فعال واقعاً تست می‌شود؛ بقیه فقط validate)، ۵۰ رویداد آخر، انقضای گواهی‌ها، فضای دیسک و RAM.
2. تحلیل خودکار: ۱۵ قانون ساده (مثلاً «node offline + path ok → شبکه کنترل مشکل دارد، تانل سالم است»، «همه پله‌ها timeout → احتمالاً IP Node بلاک شده») و چاپ نتیجه به زبان ساده.
3. خروجی: خلاصه ۲۰ خطی رنگی در ترمینال + فایل `/root/deyroute-doctor-<UTC>.tar.gz` (رازها حذف‌شده) که مالک می‌فرستد.
4. `deyroute doctor --node de-1` همین را از Node می‌گیرد.

**خطاهای غیرمنتظره (panic):** recover در بالاترین سطح، ثبت stack در `deyroute.log`، نمایش `DEY-X000 unexpected error, see log`، و سرویس با systemd دوباره بالا می‌آید؛ هیچ‌وقت stack trace در UI.

## ۱۴. مرجع کامل CLI غیرتعاملی

هر کاری که منو می‌کند باید با یک دستور CLI هم بشود (cobra)، با خروجی انسانی پیش‌فرض و `--json` برای اسکریپت؛ کد خروج `0` موفق، `1` خطای کاربر (DEY-C/P/T…)، `2` خطای سیستم (DEY-X)، `3` نیاز به تأیید که با `--yes` داده نشده.

```text
deyroute                                  # TUI
deyroute setup [--role hub|node] [--name N] [--control-port P] [--yes]
deyroute join 'dey://TOKEN@HUB_IP:PORT#FP' [--name N]
deyroute status [--json] [--watch]        # dashboard text
deyroute version

deyroute node join-command [--ttl 15m]    # prints one-line join command
deyroute node list [--json]
deyroute node rename <id> <name>
deyroute node remove <id> [--yes]         # stops its tunnels, revokes cert, drops firewall entry
deyroute node test <id>                   # control RTT, udp probe, sysinfo
deyroute node set-hub <ip:port>           # on node: point to moved hub

deyroute tunnel add --node <id> --ports 443,2053[,27015/udp] [--name N] [--ladder default] [--backup <id>...] [--yes]
deyroute tunnel list [--json]
deyroute tunnel show <id>                 # ports, ladder, active transport, probe history
deyroute tunnel edit <id> [--name] [--ladder] [--policy] [--probe-port]
deyroute tunnel enable|disable|restart|delete <id> [--yes]
deyroute tunnel switch <id> --transport backhaul/tcpmux | --node nl-1
deyroute tunnel reset <id>                # back to rung 1 now
deyroute tunnel pause|resume <id>         # failover on/off
deyroute tunnel test-ladder <id> [--yes]  # tries every rung, 20s each (causes downtime)
deyroute tunnel backup add|remove <id> --node <nid>

deyroute port add <tunnel> 8443[/tcp] [--target 127.0.0.1:8443]
deyroute port remove <tunnel> 8443[/tcp]
deyroute port check 443[/tcp] [--node <id>]   # 4-stage check
deyroute port suggest [--count 3]

deyroute ladder list|show <name>|create <name> --rungs a,b,c|set <name> --rungs ...|delete <name>

deyroute diag speed <tunnel> [--seconds 10]   # iperf3-like test through tunnel using built-in generator
deyroute diag probe <tunnel> [--all-ports]
deyroute logs [<tunnel>|hub|node] [-f] [--since 1h]
deyroute events [--tunnel <id>] [--since 24h] [--json]
deyroute doctor [--node <id>] [--out FILE]

deyroute optimize apply --profile balanced|aggressive|off
deyroute optimize revert

deyroute security rotate-tokens [--tunnel <id>] [--yes]
deyroute security rotate-ca [--yes]
deyroute security tls show|renew [--tunnel <id>]
deyroute security firewall show|apply|disable
deyroute security audit

deyroute notify telegram set --token-file F --chat-id C
deyroute notify telegram test
deyroute notify telegram off

deyroute backup [--out FILE] [--no-encrypt]
deyroute restore FILE [--yes]

deyroute update [--check] [--version V] [--rollback] [--yes]
deyroute update backends [name] [--yes]
deyroute update manifest

deyroute config show|validate|edit|apply     # edit opens $EDITOR, validates, applies atomically
deyroute settings ui-mode simple|advanced
deyroute uninstall [--keep-backups] [--nodes] [--yes]
deyroute completion bash|zsh|fish
```

**قواعد CLI:**

- هر دستور مخرب بدون `--yes` تأیید می‌گیرد؛ در حالت non-TTY بدون `--yes` با کد `3` می‌ایستد.
- `--json` خروجی پایدار با schema مستند در `docs/cli-json.md`؛ نسخه schema داخل خروجی (`"schema": 1`).
- همه دستورها از Local API استفاده می‌کنند؛ اگر دیمن بالا نیست، پیام `DEY-X003 daemon not running: systemctl start deyroute-hub`.
- `deyroute config edit` بعد از ذخیره، validate می‌کند و در صورت خطا فایل را برنمی‌گرداند بلکه دوباره ویرایشگر را با پیام خطا در بالای فایل (کامنت) باز می‌کند.
- `deyroute --help` و `deyroute <cmd> --help` کامل و با مثال.

## ۱۵. ساختار مخزن، کتابخانه‌ها، استاندارد کد، بیلد و انتشار

یک مخزن Git به نام `deyroute`، یک ماژول Go، بدون CGO، بدون وابستگی خارج از لیست زیر مگر با تأیید مالک.

**ساختار مخزن (عیناً):**

```text
deyroute/
├── cmd/deyroute/main.go            # entrypoint: cobra root, TUI when no args
├── internal/
│   ├── api/          # Control API (hub side) + client (node side); protobuf-free JSON over HTTP/2
│   ├── backend/      # interface + registry; one sub-package per backend
│   │   ├── backhaul/ rathole/ frp/ waterwall/ xray/ hysteria2/ wireguard/ direct/ gost/ chisel/
│   ├── cli/          # cobra commands (thin; call daemon via local API)
│   ├── config/       # schema, validation, migrations, atomic write
│   ├── daemon/       # hub & node long-running services, reconcile loop
│   ├── doctor/
│   ├── errors/       # DEY codes with Why/Fix
│   ├── failover/     # state machine, candidate selection, quarantine, failback
│   ├── firewall/     # nftables/ufw/firewalld/iptables detection + inet deyroute table
│   ├── health/       # probes, worker pool, RTT history
│   ├── i18n/
│   ├── install/      # binary/backend fetch, verify, mirror, fetch-via-node
│   ├── log/          # slog setup, rotation, secret redaction
│   ├── notify/       # telegram
│   ├── ports/        # bind check, process lookup, suggestions
│   ├── state/        # bbolt wrappers
│   ├── sysctl/
│   ├── systemd/      # unit templates, systemctl wrapper
│   ├── tlsutil/      # internal CA, cert issue/renew, ACME (lego)
│   └── tui/          # bubbletea models & views
├── installer/install.sh
├── deploy/systemd/*.service
├── docs/{fa,en}/…  docs/backends/<name>.md  docs/cli-json.md  docs/ERRORS.md
├── test/integration/   # docker-compose hub+node+blocker, scenario scripts
├── .goreleaser.yaml  Makefile  .golangci.yml  QUESTIONS.md  CHANGELOG.md
```

**کتابخانه‌های مجاز (نسخه‌ها را pin کن):**

| نیاز | کتابخانه |
| --- | --- |
| CLI | `spf13/cobra` |
| TUI | `charmbracelet/bubbletea` + `lipgloss` + `bubbles` |
| YAML | `gopkg.in/yaml.v3` (با `KnownFields(true)`) |
| State | `go.etcd.io/bbolt` |
| TLS/کلید | `crypto/*` استاندارد، `golang.org/x/crypto` |
| ACME | `go-acme/lego` |
| minisign | `aead.dev/minisign` |
| رمز بکاپ | `filippo.io/age` |
| nftables | `google/nftables` (fallback: اجرای `nft -f -`) |
| HTTP/2 | `net/http` + `golang.org/x/net/http2` |
| تست | `testing` + `stretchr/testify`، `go.uber.org/goleak` |
| لاگ | `log/slog` استاندارد + rotation داخلی ساده |

**استاندارد کد:**

- `gofmt` + `golangci-lint` (errcheck, govet, staticcheck, gosec, revive) بدون هشدار؛ CI رد می‌کند.
- هر خطا با `errors.Join`/wrap و کد DEY؛ هیچ `panic` خارج از init.
- هیچ `exec.Command` جز: `systemctl`, `nft` (fallback), `ss` (fallback), `ip` (wireguard), `xray x25519`, `rathole --genkey`. لیست در `internal/exec/allowlist.go`؛ تست CI مطمئن می‌شود چیز دیگری اجرا نمی‌شود.
- Contextها همه‌جا با timeout؛ هیچ goroutine بدون owner.
- پوشش تست واحد ≥ 70٪ برای `config`, `failover`, `ports`, `errors`, `backend/*` (رندر با golden files).
- Commit: Conventional Commits؛ هر فاز یک PR؛ `CHANGELOG.md` نگه داشته می‌شود.

**بیلد و انتشار:**

- `make build` → `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=… -X main.commit=… -X main.date=…"`.
- `goreleaser` برای `linux/amd64` و `linux/arm64`؛ خروجی: tar.gz، `SHA256SUMS`، `SHA256SUMS.minisig`، `install.sh`، `backends.yaml` (امضاشده).
- CI (GitHub Actions یا معادل): lint → unit → build → integration (docker) روی Ubuntu 22.04/24.04/26.04 و Debian 12/13 → release روی tag.
- ریلیز اولیه `v1.0.0` فقط بعد از پاس شدن همه سناریوهای بخش ۱۷.

## ۱۶. فازهای پیاده‌سازی و معیار پذیرش هر فاز

نه فاز، به ترتیب، هر کدام یک PR و یک tag؛ هیچ فازی بدون پاس شدن معیار پذیرش فاز قبل شروع نمی‌شود و هر معیار باید با یک اسکریپت یا تست قابل تکرار اثبات شود، نه با ادعا.

**فاز ۰ — اسکلت (tag `v0.0.1`)**

- ساختار مخزن بخش ۱۵، Makefile، goreleaser، CI با lint/test/build، `QUESTIONS.md`.
- `deyroute version`، بنر DEYROUTE، منوی خالی با آیتم‌های بخش ۶ که فقط «not implemented yet» می‌گویند.
- پذیرش: بیلد استاتیک amd64/arm64 در CI سبز؛ باینری روی Ubuntu 24.04 و Debian 12 اجرا و منو باز می‌شود.

**فاز ۱ — نصاب، setup، config، خطاها (`v0.1.0`)**

- `install.sh` کامل (بخش ۵)، `deyroute setup` برای Hub و Node، `config.yaml` با schema و validation، سیستم خطای DEY با Why/Fix، لاگ JSON + rotation + redaction، `deyroute config show|validate|edit|apply`.
- سرویس‌های `deyroute-hub` و `deyroute-node` با Local API روی سوکت (هنوز بدون تانل).
- پذیرش: نصب تک‌خطی روی VM تازه هر توزیع Tier 1 در کمتر از ۶۰ ثانیه؛ اجرای دوباره نصاب = ارتقا بدون تغییر config؛ کانفیگ با کلید ناشناخته `DEY-C001` می‌دهد؛ هیچ رازی در لاگ (تست grep خودکار).

**فاز ۲ — Join، کانال کنترل، اولین تانل با Backhaul (`v0.2.0`)**

- CA داخلی، mTLS، join token، Control API با دستورات بخش ۳، heartbeat، nftables `@nodes`.
- اینترفیس Backend + registry، بک‌اند `backhaul` (همه ترنسپورت‌ها) و `direct/native`، دانلود/تأیید باینری (+ `fetch.proxy` از Node)، رندر، unit template با هاردنینگ، `tunnel add` در CLI با یک ترنسپورت ثابت.
- پذیرش: Join در < ۳۰ ثانیه؛ `deyroute tunnel add --node de-1 --ports 443` تانل UP می‌کند و یک کلاینت واقعی VLESS+ws+tls از پشت Hub وصل می‌شود و ۱۰۰MB دانلود می‌کند؛ kill کردن پروسه بک‌اند = بالا آمدن خودکار در < ۵ ثانیه؛ Hub بدون دسترسی GitHub (شبیه‌سازی با DNS blackhole) باینری Backhaul را از طریق Node می‌گیرد.

**فاز ۳ — TUI کامل + Port checker + TLS auto (`v0.3.0`)**

- داشبورد زنده، همه منوها و ویزاردهای بخش ۶ در Simple و Advanced، port checker چهارمرحله‌ای، پیشنهاد پورت، فایروال خارجی (ufw/firewalld) با تأیید، TLS `auto` با تمدید.
- پذیرش: یک نفر که سند را نخوانده، فقط با منو، در < ۵ دقیقه Node را join و تانل ۴۴۳ را بالا می‌آورد (تست کاربر واقعی، ضبط شود)؛ پورت مشغول با نام پروسه نمایش داده می‌شود؛ ترمینال ۸۰ ستون و بدون UTF-8 منو را خراب نمی‌کند.

**فاز ۴ — Rathole و FRP + نردبان + سوییچ دستی (`v0.4.0`)**

- بک‌اندهای `rathole` (noise/tls/tcp) و `frp` (wss/websocket/quic/kcp/tcp)، پروفایل نردبان، گرم‌سازی همه پله‌ها، `tunnel switch --transport`, `tunnel test-ladder`، PKCS#12 داخلی برای rathole/tls.
- پذیرش: سوییچ دستی بین ۳ بک‌اند با قطعی ≤ ۳ ثانیه (اندازه‌گیری با کلاینت که هر ۲۰۰ms درخواست می‌زند)؛ golden tests رندر برای هر ترنسپورت؛ `test-ladder` گزارش RTT هر پله می‌دهد.

**فاز ۵ — موتور سلامت و Failover خودکار (`v0.5.0`)**

- پروب‌های بخش ۹، ماشین حالت، policyها، quarantine، ضد نوسان، failback کور با backoff، Node پشتیبان (`backup add`)، رویدادها، reconcile بعد از restart، تلگرام.
- پذیرش (در docker-compose با کانتینر «blocker» که با nftables پورت کنترل را DROP می‌کند): بلاک شدن پله فعال → UP روی پله بعدی در ≤ ۳۵ ثانیه (۱۰ بار متوالی، p95)؛ رفع بلاک → failback در ≤ `failback_after_s`+۲۰ ثانیه؛ خواباندن سرویس روی Node اصلی → سوییچ به Node پشتیبان بدون سوییچ ترنسپورت؛ ۷ بلاک متوالی در یک ساعت → `flapping` و ماندن روی `direct/native`؛ restart Hub وسط failover → state درست بازیابی می‌شود و تانل restart نمی‌شود؛ پیام تلگرام برای هر رویداد.

**فاز ۶ — بک‌اندهای پنهان: Xray-Reality، Hysteria2، Waterwall (`v0.6.0`)**

- سه بک‌اند طبق بخش ۷، پروب UDP، decoy SNI با تست دسترسی، routing allow-list روی Node.
- پذیرش: هر سه در نردبان کار می‌کنند و در تست failover فاز ۵ شرکت می‌کنند؛ تست «Node پروکسی باز نیست» (اتصال به `8.8.8.8:53` از طریق تانل باید block شود)؛ Hysteria2 با UDP بسته، خودکار از نردبان skip می‌شود و رویداد زرد می‌دهد.

**فاز ۷ — WireGuard/AmneziaWG، Gost، Chisel، HAProxy، diag speed (`v0.7.0`)**

- بک‌اندهای اختیاری، DNAT/masquerade در جدول `inet deyroute`، `diag speed` با مولد داخلی.
- پذیرش: WireGuard kernel و AWG userspace هر دو پورت TCP و UDP را رد می‌کنند؛ حذف تانل، قوانین nftables و اینترفیس را کامل پاک می‌کند (بدون باقی‌مانده، تست با `nft list ruleset` قبل/بعد).

**فاز ۸ — آپدیت، بکاپ/ریستور، doctor، canary failback، امنیت نهایی (`v0.8.0`)**

- self-update با rollback، آپدیت بک‌اندها با rollback خودکار، backup/restore رمزدار، `doctor` با ۱۵ قانون، canary failback، `security audit`, `rotate-tokens/ca`، `uninstall` کامل.
- پذیرش: آپدیت Hub بدون افت پکت روی تانل فعال (ping پیوسته)؛ restore روی سرور جدید و اتصال Nodeها بدون join دوباره؛ `uninstall` سیستم را به وضعیت قبل (sysctl، nftables، فایل‌ها) برمی‌گرداند؛ doctor روی سناریوی «Node آفلاین» تشخیص درست می‌دهد.

**فاز ۹ — مستندات، تست نهایی، ریلیز `v1.0.0`**

- مستند فارسی و انگلیسی (نصب، سناریوهای رایج، عیب‌یابی با کدها، FAQ فیلترینگ)، ویدئوی ۵ دقیقه‌ای، ماتریس تست بخش ۱۷ کامل سبز، بازبینی امنیتی چک‌لیست بخش ۱۸.

**برآورد زمان (یک برنامه‌نویس Go میان‌رده، تمام‌وقت):** فاز ۰–۱: ۱ هفته · فاز ۲: ۱.۵ هفته · فاز ۳: ۱.۵ هفته · فاز ۴: ۱ هفته · فاز ۵: ۲ هفته · فاز ۶: ۱.۵ هفته · فاز ۷: ۱ هفته · فاز ۸: ۱.۵ هفته · فاز ۹: ۱ هفته؛ جمع حدود ۱۲ هفته. فازهای ۷ و بخشی از ۸ قابل موازی‌سازی با نفر دوم.

## ۱۷. ماتریس تست و سناریوهای اجباری

هر سناریو یک اسکریپت در `test/integration/scenarios/<id>.sh` است که در docker-compose (کانتینرهای `hub`، `node1`، `node2`، `blocker`، `client`) اجرا می‌شود و با کد خروج نتیجه می‌دهد؛ ریلیز `v1.0.0` فقط با همه سبزها.

**محیط تست:** `hub` و `node*` با systemd داخل کانتینر (image با `systemd` فعال) یا VM (Vagrant/libvirt) برای Tier 1؛ `blocker` یک کانتینر با `NET_ADMIN` روی همان شبکه که با nftables پورت/IP مشخص را DROP می‌کند؛ `client` یک Xray واقعی با کانفیگ VLESS+ws+tls که به `hub:443` وصل می‌شود و با `curl --proxy` هر ۲۰۰ms یک درخواست می‌زند و قطعی را می‌شمارد.

| # | سناریو | انتظار |
| --- | --- | --- |
| S01 | نصب تازه روی هر توزیع Tier 1 | < 60s، exit 0، سرویس enable |
| S02 | نصب دوباره روی سیستم نصب‌شده | ارتقا/تعمیر؛ config دست‌نخورده |
| S03 | Join با توکن منقضی / fingerprint غلط | `DEY-N001` / `DEY-N002`، هیچ چیزی روی Hub ثبت نمی‌شود |
| S04 | تانل 443 با Backhaul + کلاینت واقعی | 100MB دانلود، بدون خطای TLS، IP کاربر = IP Hub |
| S05 | پورت 443 قبلاً توسط nginx گرفته | `DEY-P012` با نام nginx، پیشنهاد پورت آزاد |
| S06 | Hub بدون دسترسی GitHub | باینری‌ها از طریق Node می‌آیند، تانل UP |
| S07 | kill -9 پروسه بک‌اند روی Hub / Node | برگشت < 5s، رویداد `backend_crash` |
| S08 | بلاک پورت کنترل پله فعال (blocker) | UP روی پله بعدی ≤ 35s (p95 از ۱۰ اجرا)، قطعی کلاینت ≤ 3s در لحظه سوییچ |
| S09 | رفع بلاک | failback ≤ `failback_after_s`+20s، رویداد `failback` |
| S10 | failback شکست بخورد (بلاک دوباره در لحظه failback) | برگشت فوری به پله قبلی، دو برابر شدن تأخیر |
| S11 | خواباندن سرویس Xray روی Node اصلی | سوییچ به Node پشتیبان، بدون سوییچ ترنسپورت، رویداد `service_down` |
| S12 | قطع کامل شبکه Node اصلی | Node پشتیبان ≤ 45s |
| S13 | ۷ بلاک متوالی در ۱ ساعت | `flapping`، ماندن روی `direct/native`، پیام تلگرام |
| S14 | restart `deyroute-hub` وسط SWITCHING | state بازیابی، تانل سالم restart نمی‌شود |
| S15 | reboot کامل Hub و Node | همه تانل‌ها خودکار UP، ترتیب start درست |
| S16 | UDP بسته بین Hub و Node | `hysteria2/udp` و `wireguard/*` از نردبان skip، هشدار زرد |
| S17 | تلاش اتصال به `8.8.8.8:53` از داخل هر بک‌اند Forward | block می‌شود (Node پروکسی باز نیست) |
| S18 | هر پله نردبان به‌تنهایی (`test-ladder`) | همه پله‌ها پاس، RTT گزارش |
| S19 | آپدیت `deyroute` | ۰ پکت از دست‌رفته روی تانل فعال |
| S20 | آپدیت بک‌اند با باینری خراب (sha256 غلط) | رد، `DEY-S001`، بدون تغییر |
| S21 | آپدیت بک‌اند که پروب پاس نمی‌کند | rollback خودکار ≤ 60s |
| S22 | backup → restore روی Hub جدید | Nodeها بدون join دوباره وصل می‌شوند |
| S23 | uninstall | `nft list ruleset`، sysctl، فایل‌ها = قبل از نصب |
| S24 | ترمینال 80 ستون، `TERM=dumb`, بدون UTF-8 | منو قابل استفاده، بنر ASCII |
| S25 | ۵۰۰ اتصال همزمان کلاینت روی Backhaul | RAM بک‌اند ≤ 150MB، بدون خطا |
| S26 | grep رازها در همه لاگ‌ها و خروجی doctor | هیچ توکن/کلیدی پیدا نمی‌شود |
| S27 | تغییر IP عمومی Node | اتصال از IP جدید، `@nodes` به‌روز، رویداد `node_ip_changed` |
| S28 | Node با نسخه ناسازگار | هشدار، دستور اجرا نمی‌شود، پیشنهاد آپدیت |
| S29 | تانل با ۶۴ Port map و بازه پورت | رندر درست، هر پورت پروب دوره‌ای |
| S30 | ACME با دامنه واقعی (تست دستی، staging LE) | گواهی صادر، تمدید شبیه‌سازی‌شده، fallback به auto وقتی شکست |

**تست واحد اجباری:** validation کانفیگ (همه کدهای C)، انتخاب کاندید failover (جدول تصمیم کامل policy × وضعیت)، quarantine و backoff (با clock ساختگی)، رندر هر ترنسپورت (golden files)، parser ورودی پورت، redaction لاگ، محاسبه پورت آزاد. `go test -race` بدون خطا.

**تست دستی قبل از ریلیز:** روی یک Hub واقعی در ایران و دو Node واقعی، ۷۲ ساعت اجرا با کلاینت‌های واقعی؛ گزارش رویدادها و منابع پیوست PR فاز ۹.

## ۱۸. چک‌لیست تحویل نهایی

تحویل `v1.0.0` یعنی همه این‌ها تیک خورده و برای هر کدام شاهد (لینک CI، فایل، ویدئو) پیوست شده است.

- [ ] ۳۰ سناریوی بخش ۱۷ سبز، لاگ CI پیوست
- [ ] پوشش تست واحد ≥ ۷۰٪ در پکیج‌های اعلام‌شده، `go test -race` بدون خطا
- [ ] نصب تک‌خطی روی Ubuntu 22.04/24.04/26.04 و Debian 12/13 ویدئو شده
- [ ] تست کاربر واقعی (کسی که سند را نخوانده) در < ۵ دقیقه به تانل UP رسید — ویدئو
- [ ] بودجه منابع بخش ۱۲ اندازه‌گیری و در `docs/en/benchmarks.md` ثبت شده
- [ ] failover ≤ ۳۵s (p95) و قطعی سوییچ ≤ ۳s با اعداد واقعی ثبت شده
- [ ] هیچ رازی در لاگ/doctor (S26)؛ همه فایل‌های secrets 0600؛ `security audit` تمیز
- [ ] Node پروکسی باز نیست (S17) برای هر بک‌اند Forward
- [ ] `uninstall` سیستم را کامل برمی‌گرداند (S23)
- [ ] همه کدهای DEY در `docs/ERRORS.md` با Why/Fix؛ CI کد بدون Why/Fix را رد می‌کند
- [ ] `docs/fa/` و `docs/en/`: نصب، Join، اولین تانل، Node پشتیبان، عیب‌یابی با کدها، FAQ فیلترینگ، جابه‌جایی Hub، بکاپ
- [ ] `docs/backends/<name>.md` برای هر بک‌اند با نسخه pin، لینک README، اختلاف کلیدها با این سند
- [ ] ریلیز امضاشده (minisign)، `SHA256SUMS`، `install.sh` و `backends.yaml` روی Mirror مالک و GitHub
- [ ] `CHANGELOG.md` کامل؛ `QUESTIONS.md` همه پاسخ‌ها را دارد
- [ ] بازبینی امنیتی مستقل (نفر دوم یا ابزار) روی Control API، join، فایروال و رندر بک‌اندها
- [ ] تست ۷۲ ساعته روی سرورهای واقعی با گزارش رویدادها و منابع

## ۱۹. تصمیم‌های قابل تغییر توسط مالک و سؤالات باز

این موارد پیش‌فرض دارند و با همان پیش‌فرض پیاده می‌شوند، ولی مالک می‌تواند قبل از فاز مربوطه تغییرشان دهد؛ برنامه‌نویس هر تغییر را در `QUESTIONS.md` ثبت می‌کند.

| مورد | پیش‌فرض این سند | جایگزین ممکن | تا کدام فاز باید تصمیم گرفته شود |
| --- | --- | --- | --- |
| آدرس نصاب و Mirror (`RELEASE_BASE`) | مالک می‌دهد | GitHub خام | فاز ۱ |
| دامنه روی IP Hub برای ACME | ندارد (`auto` self-signed) | دامنه DNS-only | فاز ۳ |
| لیست decoy SNI برای Reality/Waterwall | ۳ دامنه که مالک می‌دهد | تشخیص خودکار از لیست داخلی | فاز ۶ |
| نردبان پیش‌فرض (ترتیب بخش ۸) | همان | جابه‌جایی بر اساس تجربه واقعی مالک | فاز ۴ |
| بازه پورت کنترل بک‌اندها | `30000-31999` | بازه دیگر | فاز ۲ |
| `control_port` Hub | `44433` | هر پورت آزاد | فاز ۲ |
| اعلان تلگرام | خاموش | روشن با bot token | فاز ۵ |
| نقش اپراتور با دسترسی محدود (فقط مشاهده/افزودن پورت) | v1 ندارد (فقط root) | کاربر لینوکس `deyroute-ops` با API محدود | بعد از v1 |
| SNI-mux روی ۴۴۳ (ترافیک کاربر و کنترل تانل روی یک پورت با تفکیک SNI) | ندارد | HAProxy/داخلی با تفکیک SNI | بعد از v1 |
| Web dashboard فقط‌خواندنی | ندارد | صفحه HTML روی 127.0.0.1 با SSH tunnel | بعد از v1 |
| چند Hub با یک config مشترک | ندارد (هر Hub مستقل) | همگام‌سازی config بین Hubها | بعد از v1 |
| udp2raw/phantun برای پوشش UDP | اختیاری فاز ۷ | حذف کامل | فاز ۷ |
| زبان فارسی در TUI | ندارد (رشته‌ها در i18n آماده) | فعال‌سازی با `settings language fa` | بعد از v1 |

**سؤالات باز برای مالک (جواب قبل از فاز ۲):**

1. سرویس روی Nodeها روی `127.0.0.1` گوش می‌دهد یا `0.0.0.0`؟ (target پیش‌فرض `127.0.0.1:<listen>` است.)
2. آیا Nodeهای پشتیبان همیشه همان پنل/کاربران را دارند (Marzban node یا mirror دستی)؟ اگر نه، failover سرور فقط با هشدار فعال می‌شود.
3. آیا IP واقعی کاربر در پنل لازم است؟ اگر بله، PROXY protocol روی سرویس Node فعال است؟
4. حداکثر تعداد Node و تانل مورد انتظار در ۱۲ ماه آینده (برای اندازه bbolt و worker pool)؟
5. معماری سرورها همه amd64 است یا arm64 هم هست؟
