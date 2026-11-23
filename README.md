# clipsync

copy บนเครื่องหนึ่ง แล้ววางบนอีกเครื่องได้เลย ใช้ได้ทั้ง Windows และ Mac ผ่าน Wi-Fi บ้านเดียวกัน

> Sync the clipboard (text and images) between a Windows PC and a Mac on the same Wi-Fi.
> One-time pairing with a 6-digit code, end-to-end encrypted, no server. Written in Go.

> **คำเตือน:** โปรเจกต์ส่วนตัว ยังไม่ผ่านการตรวจโดยผู้เชี่ยวชาญด้านความปลอดภัย
> ไลบรารีจับคู่ที่ใช้ (filippo.io/cpace) ยังเป็นรุ่นทดลอง ใช้ตามความเสี่ยงของตัวเอง
> *Personal project, not security-audited; the pairing library is experimental. Use at your own risk.*

- ส่งได้ทั้งข้อความ (ภาษาไทยได้) และรูป เช่น screenshot
- ข้อมูลเข้ารหัสทุกครั้ง และส่งตรงระหว่างสองเครื่อง ไม่ผ่านเซิร์ฟเวอร์ใคร
- รหัสผ่านที่ copy จากโปรแกรมเก็บรหัสผ่านจะไม่ถูกส่ง

## ไฟล์โปรแกรม (โฟลเดอร์ `dist/`)

| เครื่อง | ไฟล์ |
|---|---|
| Mac (ชิป Apple) | `clipsync-mac` |
| Windows ทั่วไป (Intel/AMD) | `clipsync-windows-amd64.exe` |
| Windows ชิป ARM (Snapdragon) | `clipsync-windows-arm64.exe` |

## ตั้งค่าครั้งแรก (ทำครั้งเดียว)

1. บน Mac: เปิด Terminal แล้วพิมพ์
   `~/Documents/side-project/clipboard/dist/clipsync-mac`
2. เลือก `1` จะได้รหัส 6 หลัก
3. บน Windows: ดับเบิลคลิกไฟล์ `.exe`
   - ถ้าขึ้น "Windows protected your PC" ให้กด More info แล้วกด Run anyway
   - ถ้าขึ้นหน้าต่าง Firewall ให้ติ๊ก Private แล้วกด Allow
   - ถ้า Wi-Fi บ้านตั้งเป็น Public network ให้ติ๊ก Public ด้วย
4. บน Windows: เลือก `2` แล้วพิมพ์รหัส 6 หลัก
5. ขึ้นคำว่า `Connected` ทั้งสองเครื่อง แปลว่าใช้ได้แล้ว

## ใช้งานทุกวัน

- เปิดโปรแกรมไว้ทั้งสองเครื่อง และเปิดหน้าต่างไว้ตลอด (ย่อหน้าต่างได้)
- copy ตามปกติ แล้วไปวางที่อีกเครื่องได้เลย
- ถ้าเครื่องใดปิดหรือหลุด Wi-Fi จะต่อกลับให้เอง

## คำสั่งอื่น

- `clipsync status`: ดูว่าจับคู่กับเครื่องไหนอยู่
- `clipsync unpair`: ลบการจับคู่ ไว้จับคู่ใหม่
- `clipsync -v`: แสดง log ละเอียดตอนหาสาเหตุปัญหา
- `clipsync pair 123456 -peer 192.168.1.20`: จับคู่โดยระบุ IP เอง ในกรณีที่หาอีกเครื่องไม่เจอ

## ถ้าหาอีกเครื่องไม่เจอ

- เช็กว่าสองเครื่องต่อ Wi-Fi วงเดียวกัน
- บน Windows ต้องอนุญาต clipsync ใน Firewall (Windows Security, Firewall, Allow an app)
- บน Mac ถ้าเคยกดไม่อนุญาต ให้เปิด Local Network ให้ Terminal (System Settings, Privacy & Security, Local Network)
- ถ้า IP ของอีกเครื่องเปลี่ยนแล้วต่อไม่ติด ให้สั่ง `clipsync -peer IP-ใหม่`
- ใช้พอร์ต TCP 47800 และ UDP 47801

## ข้อจำกัดตอนนี้

- ถ้าสิ่งที่ copy มีทั้งข้อความและรูป (เช่น ช่องตาราง Excel) จะส่งเฉพาะข้อความ
- copy ไฟล์ (ใน Finder หรือ Explorer) จะไม่ถูกส่ง
- รูปใหญ่สุด 40 MB หรือ 40 ล้านพิกเซล (รูป 8K ยังส่งได้)
- สองเครื่องต้องใช้ clipsync รุ่นเดียวกัน ถ้าไม่ตรงกันจะขึ้นเตือนให้อัปเดตทั้งคู่
- copy ตอนอีกเครื่องหลุด จะส่งให้ตอนต่อกลับได้ ถ้าไม่เกิน 2 นาที
- ถ้า copy ทั้งสองเครื่องในวินาทีเดียวกัน สองเครื่องอาจได้ของสลับกัน ให้ copy ใหม่อีกครั้ง
- ระหว่างจับคู่ ใส่รหัสผิดได้ 3 ครั้ง จากนั้นต้องขอรหัสใหม่

## สำหรับนักพัฒนา

```sh
go test -race ./...
GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/clipsync-mac .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/clipsync-windows-amd64.exe .
GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/clipsync-windows-arm64.exe .
# ทดสอบกับ clipboard จริงบน Mac (จะทับของที่ copy ไว้)
CLIPSYNC_REAL_CLIPBOARD=1 go test -run TestRealMacClipboard ./internal/node/
```

ไฟล์ตั้งค่าและกุญแจจับคู่อยู่ที่
- Mac: `~/Library/Application Support/clipsync/config.json`
- Windows: `%AppData%\clipsync\config.json`

โครงสร้างโค้ด
- `main.go`: คำสั่งต่าง ๆ และขั้นตอนจับคู่
- `internal/wire`: จับคู่ด้วยรหัส (CPace) และการเชื่อมต่อแบบเข้ารหัส (X25519 + AES-GCM)
- `internal/node`: ดู clipboard, ส่ง/รับ, ต่อใหม่อัตโนมัติ, กันส่งวนซ้ำ
- `internal/lan`: หาอีกเครื่องใน Wi-Fi ด้วย UDP broadcast
- `internal/clip`: อ่าน/เขียน clipboard ของแต่ละระบบ, แปลงรูป bitmap ของ Windows (`dib.go`)

## License

MIT — ดู [LICENSE](LICENSE)
