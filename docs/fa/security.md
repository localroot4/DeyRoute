# امنیت

<div dir="rtl">

[English](../en/security.md) · [فهرست](index.md)

قاعده اصلی: هر چیزی که از بیرون به Hub می‌رسد یا ترافیک کاربر روی پورت تانل است، یا یک اتصال mTLS از یک Node شناخته‌شده. هیچ پورت مدیریتی برای عموم باز نیست و هیچ رازی بدون دسترسی 0600 روی دیسک یا داخل لاگ نمی‌آید.

## کانال کنترل (Hub ↔ Nodeها)

- هنگام راه‌اندازی، Hub یک **CA داخلی** (Ed25519) می‌سازد. گواهی Hub و گواهی همه Nodeها (با اعتبار ۱۰ سال) از همین CA صادر می‌شوند.
- Node با **TLS دوطرفه** (mTLS) و فقط TLS 1.3 به پورت کنترل Hub (پیش‌فرض 44433) وصل می‌شود. بدون گواهی صادرشده از این CA، Hub درخواست را رد می‌کند (`DEY-N013`).
- Node فقط به CA‌ای اعتماد می‌کند که اثرانگشتش در لینک Join بود (pinning)؛ از مخزن CA سیستم استفاده نمی‌شود.
- اتصال همیشه از Node به Hub است. Hub هیچ‌وقت وارد Node نمی‌شود و SSH استفاده نمی‌شود.
- Node خودش تصمیمی نمی‌گیرد: فقط زیر `/etc/deyroute/backends/` فایل می‌نویسد، فقط باینری‌های بک‌اند خود deyroute را اجرا می‌کند و فقط از https دانلود می‌کند.

## توکن‌های Join

- ۳۲ بایت تصادفی، **یک‌بارمصرف**، به‌طور پیش‌فرض ۱۵ دقیقه معتبر (`deyroute node join-command --ttl` از 1m تا 24h را می‌پذیرد) و بلافاصله بعد از مصرف پاک می‌شود.
- اگر از یک آدرس در یک ساعت بیش از ۵ تلاش ناموفق شود، آن آدرس یک ساعت بلاک می‌شود (`DEY-N007`).
- تا وقتی یک توکن مصرف‌نشده وجود دارد، پورت کنترل اتصال از هر آدرسی را می‌پذیرد (پنجره Join). بقیه وقت‌ها فقط IP سرورهای Node وصل‌شده را می‌پذیرد، به‌علاوه تعداد کمی اتصال جدید در دقیقه از آدرس‌های دیگر تا Nodeی که IPاش عوض شده بتواند با گواهی خودش دوباره وصل شود.
- `deyroute security audit` درباره توکن‌های Join قدیمی‌تر از ۱۵ دقیقه که هنوز مانده‌اند هشدار می‌دهد.

## فایروال: `table inet deyroute`

DEYROUTE فقط یک جدول nftables می‌سازد و مدیریت می‌کند و هیچ‌وقت به جدول‌ها یا قوانین دیگر دست نمی‌زند. روی Hubی با دو Node و یک تانل روی 443 و 2053 این شکلی است (خط‌های پنجره Join و IPv6 فقط وقتی لازم باشند می‌آیند):

```text
table inet deyroute {
	set nodes {
		type ipv4_addr
		elements = { 1.2.3.4, 9.8.7.6 }
	}

	chain input {
		type filter hook input priority -10; policy accept;
		ct state established,related accept
		iif "lo" accept
		tcp dport 44433 ip saddr @nodes accept
		tcp dport 44433 ct state new limit rate 6/minute accept
		tcp dport 44433 drop
		tcp dport 30000-31999 ip saddr @nodes accept
		udp dport 30000-31999 ip saddr @nodes accept
		tcp dport 30000-31999 drop
		udp dport 30000-31999 drop
		tcp dport { 443, 2053 } accept
	}
}
```

- پورت کنترل و پورت‌های کنترل بک‌اندها (30000 تا 31999) فقط برای Nodeها باز است؛ پورت‌های تانل برای همه.
- جدول `inet deyroute` روی خود Node فقط قوانین NAT ترنسپورت‌هایش را دارد.

```bash
deyroute security firewall show      # جدول و فایروال‌های پیداشده (ufw، firewalld و…)
deyroute security firewall apply     # همین حالا بساز و اعمال کن
deyroute security firewall disable   # جدول را حذف کن؛ از آن به بعد deyroute فقط دستور پیشنهاد می‌دهد
```

با `security.firewall_managed: false` در `config.yaml`، DEYROUTE چیزی اعمال نمی‌کند و فقط دستورهایی را که خودتان باید اجرا کنید چاپ می‌کند (`DEY-P031`). فایروال بیرونی (ufw، firewalld، پنل سرور) هیچ‌وقت بدون تأیید شما تغییر نمی‌کند؛ `deyroute port check` دستور دقیق را نشان می‌دهد و `deyroute port check <port> --open` (یا `Open it in the firewall` در منو) آن را فقط بعد از اینکه `yes` را تایپ کنید اجرا می‌کند. Hub هیچ‌وقت متنی را که دریافت می‌کند اجرا نمی‌کند: فایروال را دوباره چک می‌کند، دستور را از روی فایروال پیداشده و شماره و پروتکل پورت می‌سازد و اگر با دستوری که تأیید کردید فرق داشته باشد اجرا نمی‌کند (`DEY-P032`). این کار با `security.firewall_managed: false` هم انجام می‌شود، چون آن تنظیم فقط به جدول `inet deyroute` مربوط است. به فایروال پنل ارائه‌دهنده هیچ‌وقت دست زده نمی‌شود.

## رازها

- پوشه `/etc/deyroute/secrets/` دسترسی 0700 دارد و هر فایل داخلش 0600 و مالک root است: کلید CA، کلیدهای Hub/Node، توکن‌های Join، یک توکن ۳۲ بایتی برای هر تانل و کلیدهای TLS تانل‌ها. `config.yaml` هم 0600 است.
- بک‌اندها با کاربر `deyroute` اجرا می‌شوند و فقط نسخه‌ای را که لازم دارند زیر `/etc/deyroute/backends/` می‌گیرند (0640 root:deyroute).
- توکن‌ها، کلیدها و رمزها هیچ‌وقت در لاگ، پیام خطا یا خروجی doctor نمی‌آیند و با `***` جایگزین می‌شوند. فایل پشتیبانی doctor قبل از نوشته شدن یک بار دیگر بررسی می‌شود و اگر هنوز رازی در آن پیدا شود، اصلاً نوشته نمی‌شود (`DEY-X060`).
- منو و دستورها از طریق `/run/deyroute/daemon.sock` (0600) با سرویس حرف می‌زنند: فقط root می‌تواند از آن‌ها استفاده کند.
- فایل رازی که دسترسی اشتباه دارد با `DEY-S002` گزارش می‌شود؛ با `chmod 600 <file> && chown root:root <file>` درستش کنید.

## عوض کردن توکن‌ها و CA

```bash
deyroute security rotate-tokens                      # همه تانل‌ها
deyroute security rotate-tokens --tunnel main --yes  # یک تانل
deyroute security rotate-ca                          # Advanced
```

- `rotate-tokens` توکن مخفی تانل(ها) را عوض می‌کند. توکن قبلی فوراً از کار می‌افتد و همه ترنسپورت‌های تانل با توکن جدید روی Hub و Nodeها ری‌استارت می‌شوند (قطعی کوتاه). باید `yes` تایپ کنید.
- `rotate-ca` خود CA داخلی و گواهی Hub را عوض می‌کند و گواهی همه Nodeهای **آنلاین** را دوباره صادر می‌کند. Nodeهای آفلاین دیگر نمی‌توانند وصل شوند و باید دوباره Join کنند؛ Join commandهایی که قبلاً ساخته شده‌اند هم از کار می‌افتند.

در منو: `8) Security` ← `1) Rotate tokens`.

## گواهی‌های TLS تانل

بعضی ترنسپورت‌ها (مثلاً `backhaul/wssmux` و `frp/*`) خود تانل را داخل TLS می‌گذارند. این گواهی مال خود تانل است، نه سرویس شما؛ TLS کاربران شما دست‌نخورده رد می‌شود.

| حالت (`tunnels[].tls.mode`) | چه می‌شود |
| --- | --- |
| `auto` (پیش‌فرض) | برای هر تانل یک گواهی از CA داخلی، با اعتبار ۳ سال، که ۳۰ روز مانده به انقضا خودکار تمدید می‌شود؛ Node آن را با CA پین‌شده بررسی می‌کند |
| `acme` | گواهی Let's Encrypt برای `hub.domain` (دامنه باید DNS-only باشد، بدون پراکسی Cloudflare؛ HTTP-01 روی پورت 80 یا DNS-01 با توکن Cloudflare). اگر شکست بخورد، تانل به `auto` برمی‌گردد (رویداد `acme_failed`، کد `DEY-T003`) و قطع نمی‌شود |
| `custom` | فایل گواهی و کلید خودتان، که قبل از استفاده بررسی می‌شوند |

```bash
deyroute security tls show              # انقضا و اثرانگشت همه گواهی‌ها
deyroute security tls show --tunnel main
deyroute security tls renew --tunnel main
```

داشبورد ۱۴ روز مانده به انقضای یک گواهی هشدار می‌دهد. حالت TLS هر تانل در حالت Advanced تنظیم می‌شود (`2) Tunnels` ← `2) Edit tunnel`) یا با `deyroute tunnel edit main --tls-mode auto|acme|custom` (برای `custom` گزینه‌های `--tls-cert` و `--tls-key` هم لازم است).

### گواهی Let's Encrypt (حالت `acme`)

1. یک دامنه را به Hub اشاره دهید: رکورد `A` (و اگر Hub آی‌پی نسخه ۶ دارد، `AAAA`) به‌صورت **DNS only**، یعنی بدون پراکسی Cloudflare (ابر نارنجی خاموش).
2. آن را دامنه Hub کنید. از این به بعد همه گواهی‌های تانل این نام را هم دارند.

   ```bash
   deyroute security tls domain vpn.example.com
   deyroute security tls domain --clear       # حذف دوباره دامنه
   ```

   در منو: `8) Security` ← `2) TLS certificates` ← `2) Domain (for ACME)` (برای حذف، `-` تایپ کنید). تا وقتی تانلی در حالت `acme` است، دامنه حذف نمی‌شود.
3. تانل را عوض کنید: `deyroute tunnel edit main --tls-mode acme` (در منو: حالت Advanced، `2) Tunnels` ← `2) Edit tunnel`).
4. گواهی را همین حالا بگیرید: `deyroute security tls renew --tunnel main` (در منو: `8) Security` ← `3) Renew TLS certificate`)، یا بگذارید تمدید روزانه این کار را بکند.

Let's Encrypt دامنه را با **HTTP-01 روی پورت 80** خود Hub بررسی می‌کند. اگر پورت 80 مشغول است، به‌جایش از **DNS-01 از طریق Cloudflare** استفاده کنید (Advanced): در Cloudflare یک API token با دسترسی `Zone:DNS:Edit` برای zone همان دامنه بسازید، آن را در یک فایل بگذارید و مسیر فایل را به deyroute بدهید:

```bash
deyroute security tls acme --cloudflare-token-file /root/cloudflare.token
deyroute security tls acme --email owner@example.com      # اختیاری: ایمیل هشدار انقضا
deyroute security tls acme --cloudflare-token-file ''      # برگشت به HTTP-01
```

در منو (Advanced): `8) Security` ← `2) TLS certificates` ← `3) ACME e-mail` و `4) Cloudflare token (DNS-01)`. خود توکن هیچ‌وقت در منو یا خط فرمان تایپ نمی‌شود: deyroute آن را از فایل شما در `/etc/deyroute/secrets/cloudflare.token` (با دسترسی 0600) کپی می‌کند و بعد از آن می‌توانید فایل خودتان را پاک کنید. در `config.yaml` فقط مسیر همین فایل (`hub.acme.cloudflare_token_file`) می‌آید و توکن هیچ‌وقت در لاگ‌ها دیده نمی‌شود. صفحه `TLS certificates` دامنه و روش بررسی فعلی را نشان می‌دهد.

اگر Let's Encrypt شکست بخورد (`DEY-T003`، `DEY-T004`)، تانل با گواهی داخلی خودش کار می‌کند (رویداد `acme_failed`) و تمدید روزانه دوباره امتحان می‌کند.

## Audit

```bash
deyroute security audit
```

پورت‌های عمومی باز، دسترسی فایل‌ها، انقضای گواهی‌ها، Nodeهایی که نسخه قدیمی دارند و توکن‌های Join باقی‌مانده را فهرست می‌کند. بعد از هر تغییر امنیتی اجرایش کنید. در منو: `8) Security` ← `5) Audit`.

## پروسه‌های تانل

- هر ترنسپورت یک سرویس systemd جداست که با کاربر `deyroute` اجرا می‌شود، فقط اجازه گرفتن پورت‌های زیر ۱۰۲۴ را دارد (`CAP_NET_BIND_SERVICE`) و در یک محیط محدود اجرا می‌شود (`NoNewPrivileges`، `ProtectSystem=strict`، `ProtectHome`، `PrivateTmp`، فیلتر فراخوانی‌های سیستمی و…). فقط ترنسپورت‌های WireGuard با root و `CAP_NET_ADMIN` اجرا می‌شوند.
- کرش یک بک‌اند هیچ‌وقت Hub را از کار نمی‌اندازد و systemd آن را دوباره بالا می‌آورد.

## Node هیچ‌وقت پراکسی باز نیست

ترنسپورت‌هایی که Hub به Node وصل می‌شود (`xray/reality`، `hysteria2/udp`، `direct/*` و…) فقط اجازه اتصال به `127.0.0.0/8` و مقصدهای تعریف‌شده در تانل را می‌دهند و بقیه بسته است (routing در Xray، فهرست مجاز رله، ACL در Hysteria2). هیچ‌کس نمی‌تواند از طریق تانل با Node شما به آدرس‌های دیگر برسد.

## دانلودها و بدون telemetry

- ریلیزها امضا شده‌اند: `SHA256SUMS` با minisign؛ نصاب و خود deyroute امضا و checksum را بررسی می‌کنند.
- باینری بک‌اندها فقط وقتی نصب می‌شوند که sha256 آن‌ها با manifest امضاشده بخواند (`DEY-S001`، `DEY-S006`). هیچ‌وقت اسکریپت شخص ثالثی مستقیم در shell اجرا نمی‌شود.
- **بدون telemetry.** DEYROUTE هیچ داده‌ای درباره شما یا کاربرانتان به هیچ جا نمی‌فرستد. تنها ترافیک بیرونی: دانلود ریلیزهای DEYROUTE و باینری بک‌اندها از GitHub یا Mirror شما (اگر Hub به GitHub نرسد، از طریق یک Node)، و پیام‌های تلگرام اگر اعلان را خودتان روشن کنید (Hub خودش می‌فرستد، یا اگر api.telegram.org بسته باشد از طریق یک Node).

## چک‌لیست

- [ ] روی Hub به‌جز SSH فقط پورت‌های تانل به اینترنت باز است (پورت‌های کنترل فقط برای Nodeهای شما).
- [ ] `deyroute security audit` هیچ مشکلی گزارش نمی‌کند.
- [ ] یک بکاپ رمزدار و تازه بیرون از سرور دارید.
- [ ] Nodeها هم‌نسخه Hub هستند (`deyroute node list`).
- [ ] همگام‌سازی ساعت روی همه سرورها روشن است (`timedatectl set-ntp true`).

</div>
