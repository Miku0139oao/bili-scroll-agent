package app

import qrcode "github.com/skip2/go-qrcode"

func qrPNG(value string) ([]byte, error) {
	return qrcode.Encode(value, qrcode.Medium, 320)
}
