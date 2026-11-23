# clipsync

copy บนเครื่องหนึ่ง แล้ววางบนอีกเครื่องได้เลย ใช้ได้ระหว่าง Windows กับ Mac ที่ต่อ Wi-Fi บ้านเดียวกัน

> Sync the clipboard (text and images) between a Windows PC and a Mac on the same Wi-Fi.
> One-time pairing with a 6-digit code, end-to-end encrypted, no server. Written in Go.

- ส่งได้ทั้งข้อความ (ภาษาไทยได้) และรูป เช่น screenshot
- ส่งตรงระหว่างสองเครื่อง เข้ารหัสทุกครั้ง ไม่ผ่านเซิร์ฟเวอร์ของใคร
- ทำงานเงียบ ๆ เป็นไอคอนบนแถบเมนู (Mac) หรือข้างนาฬิกา (Windows) และเปิดเองตอนเปิดเครื่อง
- รหัสผ่านที่ copy จากโปรแกรมเก็บรหัสผ่านจะไม่ถูกส่ง

> **คำเตือน:** โปรเจกต์ส่วนตัว ยังไม่ผ่านการตรวจโดยผู้เชี่ยวชาญด้านความปลอดภัย
> ไลบรารีจับคู่ที่ใช้ (filippo.io/cpace) ยังเป็นรุ่นทดลอง ใช้ตามความเสี่ยงของตัวเอง
> *Personal project, not security-audited; the pairing library is experimental. Use at your own risk.*

## ดาวน์โหลด

โหลดรุ่นล่าสุดที่หน้า **[Releases](https://github.com/tobsatop/clipsync/releases/latest)**

| เครื่อง | ไฟล์ |
|---|---|
| Windows ทั่วไป (Intel/AMD) | `clipsync-windows-amd64.exe` |
| Windows ชิป ARM (เช่น Snapdragon) | `clipsync-windows-arm64.exe` |
| Mac ชิป Apple (M1 ขึ้นไป) | `clipsync-mac.zip` |

ไม่แน่ใจว่า Windows เป็นแบบไหน ให้เลือก `amd64` ซึ่งใช้กับคอมส่วนใหญ่

## ติดตั้งบน Mac

1. โหลด `clipsync-mac.zip` แล้วดับเบิลคลิกเพื่อแตกไฟล์ จะได้ `clipsync.app`
2. ลาก `clipsync.app` ไปไว้ในโฟลเดอร์ **Applications**
3. ดับเบิลคลิกเปิด ครั้งแรก macOS จะขึ้นว่าตรวจสอบผู้พัฒนาไม่ได้ เพราะแอปไม่ได้ซื้อใบรับรองจาก Apple
   - ไปที่ System Settings แล้วเข้า Privacy & Security
   - เลื่อนลงไปจะเห็นข้อความเรื่อง clipsync ให้กด **Open Anyway** แล้วใส่รหัสผ่านเครื่อง
   - หรือถ้าใช้ Terminal เป็น: `xattr -dr com.apple.quarantine /Applications/clipsync.app`
4. ถ้าขึ้นกล่องถามเรื่อง **Local Network** ให้กด **Allow** (ต้องใช้เพื่อคุยกับอีกเครื่อง)
5. ไอคอนรูป clipboard จะขึ้นที่แถบเมนูด้านบน และตั้งให้เปิดเองตอนล็อกอินแล้ว

## ติดตั้งบน Windows

1. โหลดไฟล์ `.exe` ที่ตรงกับเครื่อง
2. ถ้าเบราว์เซอร์เตือนว่าไฟล์ไม่ค่อยมีคนโหลด ให้กด **Keep**
3. ดับเบิลคลิกไฟล์ ถ้าขึ้น "Windows protected your PC" ให้กด **More info** แล้วกด **Run anyway**
4. ถ้าขึ้นหน้าต่าง Firewall ให้ติ๊ก **Private** แล้วกด **Allow**
   - ถ้า Wi-Fi บ้านตั้งเป็น Public network ให้ติ๊ก **Public** ด้วย
5. จะขึ้นกล่องบอกว่า clipsync ทำงานแล้ว ไอคอนสีฟ้าอยู่ข้างนาฬิกาขวาล่าง (ถ้าไม่เห็นให้กด ^)

โปรแกรมจะก๊อปตัวเองไปไว้ที่ `%LocalAppData%\Programs\clipsync` และตั้งให้เปิดเองตอนล็อกอิน ไฟล์ที่โหลดมาลบทิ้งได้

## จับคู่สองเครื่อง (ทำครั้งเดียว)

ครั้งแรกที่เปิด clipsync จะถามให้จับคู่เอง หรือเลือก **Pair with another computer...** จากเมนูของไอคอนก็ได้

1. เครื่องหนึ่งเลือก **แสดงรหัส** แล้วจะได้รหัส 6 หลัก
   - Mac: กดปุ่ม **Show a code**
   - Windows: จะเปิดหน้าต่างจับคู่ พิมพ์ `1`
2. อีกเครื่องเลือก **พิมพ์รหัส** แล้วใส่รหัส 6 หลักนั้น
   - Mac: กดปุ่ม **Type a code**
   - Windows: พิมพ์ `2` แล้วพิมพ์รหัส (พิมพ์ทั้งที่แป้นยังเป็นภาษาไทยก็ได้)
3. ขึ้น **Paired with ...** แปลว่าเสร็จ ไอคอนจะเปลี่ยนเป็นแบบทึบ (Mac) หรือสีฟ้า (Windows) เมื่อเชื่อมต่อกันแล้ว

ใส่รหัสผิดได้ 3 ครั้ง จากนั้นต้องขอรหัสใหม่

## ใช้งานทุกวัน

- ไม่ต้องทำอะไร copy ตามปกติ แล้วไปวางที่อีกเครื่องได้เลย
- เปิดเครื่องแล้ว clipsync ทำงานเองทั้งสองเครื่อง
- ถ้าเครื่องใดปิดหรือหลุด Wi-Fi จะต่อกลับให้เอง ของที่ copy ระหว่างหลุดจะส่งให้ตอนต่อกลับ ถ้าไม่เกิน 2 นาที

คลิกไอคอนจะเห็นเมนู:

| เมนู | ทำอะไร |
|---|---|
| Connected to ... / Waiting for ... | สถานะการเชื่อมต่อ |
| Last: ... | สิ่งที่ส่งหรือรับล่าสุด |
| Pause syncing | หยุดส่งและรับชั่วคราว |
| Start at login | เปิดเองตอนล็อกอิน (เปิดไว้เป็นค่าเริ่มต้น) |
| Pair with another computer... | จับคู่ใหม่ |
| Open log | ดูบันทึกการทำงาน ไว้หาสาเหตุเวลามีปัญหา |
| Quit clipsync | ปิดโปรแกรม |

## อัปเดตเป็นรุ่นใหม่

สองเครื่องต้องใช้รุ่นเดียวกัน ถ้าไม่ตรงกัน log จะเตือนให้อัปเดตทั้งคู่ ไม่ต้องจับคู่ใหม่

- **Mac:** เลือก Quit clipsync จากเมนู แล้วเอา `clipsync.app` ตัวใหม่ไปวางทับใน Applications แล้วเปิด
- **Windows:** เลือก Quit clipsync จากเมนู แล้วดับเบิลคลิกไฟล์ `.exe` ตัวใหม่ โปรแกรมจะติดตั้งทับตัวเดิมเอง

## ถอนการติดตั้ง

- **Mac:** เอาติ๊ก Start at login ออก เลือก Quit clipsync แล้วลบ `clipsync.app` และโฟลเดอร์ `~/Library/Application Support/clipsync`
- **Windows:** เอาติ๊ก Start at login ออก เลือก Quit clipsync แล้วลบโฟลเดอร์ `%LocalAppData%\Programs\clipsync` และ `%AppData%\clipsync`

## ถ้ามีปัญหา

**หาอีกเครื่องไม่เจอ หรือไอคอนค้างที่ Waiting for ...**
- เช็กว่าสองเครื่องต่อ Wi-Fi วงเดียวกัน
- Windows: อนุญาต clipsync ใน Firewall (Windows Security, Firewall & network protection, Allow an app through firewall)
- Mac: เปิด Local Network ให้ clipsync (System Settings, Privacy & Security, Local Network)
- ถ้า IP ของอีกเครื่องเปลี่ยนแล้วต่อไม่ติด ให้ปิด clipsync แล้วเปิดใหม่ด้วยคำสั่ง `clipsync -peer IP-ของอีกเครื่อง`
- เปิด **Open log** ในเมนูเพื่อดูว่าติดตรงไหน
- clipsync ใช้พอร์ต TCP 47800 และ UDP 47801

**ขึ้นว่า clipsync is already running**
- มีตัวที่ทำงานอยู่แล้ว ดูที่ไอคอนบนแถบเมนูหรือข้างนาฬิกา

## ข้อจำกัด

- ถ้าสิ่งที่ copy มีทั้งข้อความและรูป (เช่น ช่องตาราง Excel) จะส่งเฉพาะข้อความ
- copy ไฟล์ (ใน Finder หรือ Explorer) จะไม่ถูกส่ง
- รูปใหญ่สุด 40 MB หรือ 40 ล้านพิกเซล (รูป 8K ยังส่งได้)
- ถ้า copy ทั้งสองเครื่องในวินาทีเดียวกัน สองเครื่องอาจได้ของสลับกัน ให้ copy ใหม่อีกครั้ง
- ใช้กับคอมสองเครื่องเท่านั้น และต้องอยู่ในเครือข่ายเดียวกัน
- แอป Mac รองรับเฉพาะชิป Apple

## ทำงานอย่างไร

- **หากัน:** แต่ละเครื่องประกาศตัวใน Wi-Fi ด้วย UDP broadcast ทุก 3 วินาที
- **จับคู่:** รหัส 6 หลักใช้กับ CPace ซึ่งเป็น PAKE: คนดักฟังเครือข่ายเดารหัสไม่ได้ และเครื่องปลอมเดาได้แค่ครั้งละหนึ่งรหัส ผลที่ได้คือกุญแจลับที่สองเครื่องเก็บไว้
- **เชื่อมต่อ:** ทุกครั้งที่ต่อจะสร้างกุญแจใหม่จากกุญแจลับผสม X25519 แล้วเข้ารหัสด้วย AES-256-GCM แยกกุญแจแต่ละทิศทาง ถึงกุญแจลับหลุดในอนาคต ข้อมูลที่เคยส่งก็ยังถอดไม่ได้
- **กุญแจเก็บที่:** Mac `~/Library/Application Support/clipsync/config.json`, Windows `%AppData%\clipsync\config.json` อ่านได้เฉพาะผู้ใช้คนนั้น

## สำหรับนักพัฒนา

```sh
go test -race ./...
./scripts/build.sh        # dist/clipsync.app, dist/clipsync-windows-{amd64,arm64}.exe
# ทดสอบกับ clipboard จริงบน Mac (จะเก็บและคืนของที่ copy ไว้ให้)
CLIPSYNC_REAL_CLIPBOARD=1 go test -run TestRealMac ./internal/node/
```

**ออกรุ่นใหม่:** แก้ `version` ใน `main.go` แล้ว commit จากนั้น

```sh
git tag v0.3.1 && git push origin v0.3.1
```

GitHub Actions (`.github/workflows/release.yml`) จะทดสอบ build และสร้างหน้า Release พร้อมไฟล์ให้เอง

**คำสั่งเพิ่มเติม** (Terminal บน Mac ใช้ `/Applications/clipsync.app/Contents/MacOS/clipsync`)

| คำสั่ง | ทำอะไร |
|---|---|
| `clipsync` | เปิดเป็นไอคอน (แบบดับเบิลคลิก) |
| `clipsync run` | sync ในหน้าต่างนี้พร้อม log สด (ต้องปิดตัวที่เป็นไอคอนก่อน) |
| `clipsync status` | ดูว่าจับคู่กับเครื่องไหนอยู่ |
| `clipsync pair` / `clipsync pair 123456` | จับคู่แบบแสดงรหัส / พิมพ์รหัส |
| `clipsync unpair` | ลบการจับคู่ |
| `-peer IP` | ระบุ IP ของอีกเครื่องเอง |
| `-v` | log ละเอียด |

**โครงสร้างโค้ด**
- `main.go`, `pairing.go`: คำสั่งต่าง ๆ และขั้นตอนจับคู่
- `tray*.go`, `lock_*.go`: ไอคอน เมนู เปิดเองตอนล็อกอิน (LaunchAgent บน Mac, Run key บน Windows) และกันเปิดซ้อน
- `internal/wire`: จับคู่ด้วยรหัส (CPace) และการเชื่อมต่อแบบเข้ารหัส (X25519 + AES-GCM)
- `internal/node`: ดู clipboard, ส่ง/รับ, ต่อใหม่อัตโนมัติ, กันส่งวนซ้ำ
- `internal/lan`: หาอีกเครื่องใน Wi-Fi ด้วย UDP broadcast
- `internal/clip`: อ่าน/เขียน clipboard ของแต่ละระบบ, แปลงรูป bitmap ของ Windows (`dib.go`)
- `internal/icons`, `tools/genicons`: วาดไอคอน และฝังไอคอนลงไฟล์ .exe

## License

MIT — ดู [LICENSE](LICENSE)
