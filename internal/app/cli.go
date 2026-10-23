package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/security"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/term"
)

func CreateAdmin(
	ctx context.Context,
	a *App,
	username, displayName, password string,
) (string, string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = username
	}
	if !validUsername(username) {
		return "", "", errors.New("invalid username")
	}
	if _, err := a.userByUsername(ctx, username); err == nil {
		return "", "", errors.New("username already exists")
	}
	if password == "" {
		var err error
		password, err = promptPassword("Password (12+ chars): ")
		if err != nil {
			return "", "", err
		}
		confirmation, err := promptPassword("Confirm password: ")
		if err != nil {
			return "", "", err
		}
		if password != confirmation {
			return "", "", errors.New("passwords do not match")
		}
	}
	passwordHash, err := security.HashPassword(password)
	if err != nil {
		return "", "", err
	}
	secret, err := security.NewTOTPSecret()
	if err != nil {
		return "", "", err
	}
	encrypted, err := a.Cipher.EncryptBytes([]byte(secret), "totp:"+username)
	if err != nil {
		return "", "", err
	}
	now := store.Now()
	err = a.Store.WithTx(ctx, func(tx *sql.Tx) error {
		userID := model.NewID()
		if _, err := tx.Exec(`
			INSERT INTO users(id,username,display_name,role,password_hash,totp_secret_enc,
				totp_confirmed,disabled,created_at)
			VALUES(?,?,?,'admin',?,?,1,0,?)`,
			userID, username, displayName, passwordHash, encrypted, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO user_configs(user_id,payload,updated_at) VALUES(?,?,?)`,
			userID, store.JSON(model.DefaultMemberConfig()), now); err != nil {
			return err
		}
		_, err := tx.Exec(`
			INSERT INTO schedules(user_id,enabled,local_time,timezone,updated_at)
			VALUES(?,0,'20:00','Asia/Shanghai',?)`, userID, now)
		return err
	})
	if err != nil {
		return "", "", err
	}
	return secret, security.TOTPURI(secret, username, "Bili Scroll Agent"), nil
}

func ResetTOTP(ctx context.Context, a *App, username string) (string, string, error) {
	user, err := a.userByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		return "", "", errors.New("user not found")
	}
	secret, err := security.NewTOTPSecret()
	if err != nil {
		return "", "", err
	}
	encrypted, err := a.Cipher.EncryptBytes([]byte(secret), "totp:"+user.Username)
	if err != nil {
		return "", "", err
	}
	now := store.Now()
	err = a.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE users SET totp_secret_enc=?,totp_confirmed=1 WHERE id=?`,
			encrypted, user.ID); err != nil {
			return err
		}
		_, err := tx.Exec(`
			UPDATE auth_sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL`,
			now, user.ID)
		return err
	})
	if err != nil {
		return "", "", err
	}
	return secret, security.TOTPURI(secret, user.Username, "Bili Scroll Agent"), nil
}

func PrintTOTPSetup(username, secret, uri string) {
	fmt.Println()
	fmt.Println("Scan this QR code with your authenticator app:")
	printTerminalQR(uri)
	fmt.Println("Issuer:  Bili Scroll Agent")
	fmt.Println("Account:", username)
	fmt.Println("Secret: ", secret)
	fmt.Println("URI:    ", uri)
	fmt.Println("Then enter the current six-digit code on the login page.")
}

func Backup(ctx context.Context, a *App) (string, error) {
	targetDir := filepath.Join(a.Settings.DataDir, "backups")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(targetDir, "bili-scroll-"+time.Now().UTC().Format("20060102-150405")+".db")
	quoted, _ := json.Marshal(target)
	if _, err := a.Store.DB.ExecContext(ctx, "VACUUM INTO "+string(quoted)); err != nil {
		return "", err
	}
	return target, nil
}

func SecretHealth(ctx context.Context, a *App) (map[string]int, error) {
	result := map[string]int{
		"totp_checked": 0, "totp_failed": 0,
		"bili_checked": 0, "bili_failed": 0,
	}
	rows, err := a.Store.DB.QueryContext(ctx, `
		SELECT username,COALESCE(totp_secret_enc,'') FROM users`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var username, encrypted string
		if rows.Scan(&username, &encrypted) != nil || encrypted == "" {
			continue
		}
		result["totp_checked"]++
		if _, err := a.Cipher.DecryptBytes(encrypted, "totp:"+username); err != nil {
			result["totp_failed"]++
		}
	}
	_ = rows.Close()
	rows, err = a.Store.DB.QueryContext(ctx, `
		SELECT user_id,ciphertext FROM bili_credentials`)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, encrypted string
		if rows.Scan(&userID, &encrypted) != nil {
			continue
		}
		result["bili_checked"]++
		var value map[string]any
		if err := a.Cipher.DecryptJSON(encrypted, "bili:"+userID, &value); err != nil {
			result["bili_failed"]++
		}
	}
	return result, rows.Err()
}

func PromptLine(prompt string) (string, error) {
	fmt.Print(prompt)
	var value string
	_, err := fmt.Fscanln(os.Stdin, &value)
	return strings.TrimSpace(value), err
}

func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		return string(raw), err
	}
	var value string
	_, err := fmt.Fscanln(os.Stdin, &value)
	return value, err
}

func printTerminalQR(value string) {
	code, err := qrcode.New(value, qrcode.Low)
	if err != nil {
		return
	}
	for _, row := range code.Bitmap() {
		var builder strings.Builder
		for _, dark := range row {
			if dark {
				builder.WriteString("██")
			} else {
				builder.WriteString("  ")
			}
		}
		fmt.Println(builder.String())
	}
}
