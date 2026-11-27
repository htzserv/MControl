# MControl v1.8.1.1 — راهنمای نصب و استفاده

پنل مرکزی مدیریت سرورها، تانل‌ها، مانیتورینگ، دامنه‌ها و اپلیکیشن اندروید (با طراحی **MDesign**).

---

## دستورات سریع نصب

### ۱. نصب تعاملی از GitHub (سرور خارج)
```bash
bash <(curl -fsSL https://github.com/htzserv/MControl/releases/latest/download/install.sh)
```

### ۲. نصب تعاملی از میرور ایران (ParsPack)
```bash
bash <(curl -fsSL https://c107328.parspack.net/c107328/MControl/install.sh) --mirror
```

### ۳. نصب نسخه Beta از GitHub یا میرور ایران
```bash
# از GitHub (beta-latest)
bash <(curl -fsSL https://github.com/htzserv/MControl/releases/download/beta-latest/install.sh) --beta

# از میرور ایران (beta-latest)
bash <(curl -fsSL https://c107328.parspack.net/c107328/MControl/beta-latest/install.sh) --mirror --beta
```

---

## گزینه‌های خط فرمان اسکریپت نصب (`install.sh`)

| گزینه / آرگومان | توضیح |
| :--- | :--- |
| `--mirror` / `iran` | دریافت فایل‌ها از میرور ایران (ParsPack) |
| `--direct` / `foreign` / `github` | دریافت مستقیم از GitHub Releases |
| `--stable` / `stable` | نصب نسخه پایدار (فقط پنل) |
| `--full` / `full-stable` | نصب نسخه پایدار همراه با باندل موتورها (`Engines Bundle`) |
| `--beta` / `beta` | نصب نسخه بتا (`beta-latest` · فقط پنل) |
| `--full-beta` / `beta-full` | نصب نسخه بتا همراه با باندل موتورها |
| `--bundle` / `engines` | دریافت و نصب باندل موتورها بدون نصب مجدد پنل |
| `--wipe` / `wipe` | پاک‌سازی کامل نصب قبلی (سرویس، باینری‌ها، اسکریپت‌ها و دیتابیس) قبل از نصب |
| `[port]` | تعیین پورت دلخواه پنل (در صورت عدم تعیین، پورت تصادفی در بازه `20000–54999` انتخاب می‌شود) |

### مثال‌های نصب غیرتعاملی با پورت مشخص
```bash
# نصب Stable روی پورت 2053 از GitHub
bash <(curl -fsSL https://github.com/htzserv/MControl/releases/latest/download/install.sh) --direct --stable 2053

# نصب Full Beta روی پورت 8443 از میرور ایران
bash <(curl -fsSL https://c107328.parspack.net/c107328/MControl/beta-latest/install.sh) --mirror --full-beta 8443

# نصب تمیز با پاک‌سازی کامل قبلی
bash <(curl -fsSL https://github.com/htzserv/MControl/releases/latest/download/install.sh) --wipe --beta
```

---

## منوی ترمینال نصب‌کننده (`install.sh`) و منوی اصلی (`mcontrol`)

در حالت تعاملی `install.sh` (بدون نیاز به توکن)، منوی زیر نمایش داده می‌شود:
1. **Install:** نصب MControl، ساخت سرویس `systemd` و ورود به منوی `mcontrol`
2. **Update:** دریافت و اعمال نسخه جدید و ری‌استارت سرویس
3. **Uninstall:** حذف کامل سرویس، باینری‌ها، اسکریپت‌ها و دیتابیس/داده‌ها
4. **Show Info:** نمایش نسخه، وضعیت سرویس، IPv4، IPv6، پورت و آدرس دسترسی
5. **Reset Username / Password:** ریست نام کاربری و رمز عبور پنل و ابطال نشست‌های قبلی
6. **Reset Port:** تغییر پورت پنل در دیتابیس و سرویس `systemd`
7. **Check IP & Status:** بررسی دستی آی‌پی‌ها، پورت و وضعیت در لحظه

پس از نصب، با اجرای دستور زیر در ترمینال وارد منوی مدیریت می‌شوید:
```bash
mcontrol
```

---

## بررسی وضعیت سرویس پس از نصب
```bash
systemctl status mcontrol.service --no-pager
journalctl -u mcontrol.service -n 40 --no-pager
```
