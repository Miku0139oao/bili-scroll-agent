package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) transcribe(ctx context.Context, userID, bvid, modelName string) (map[string]any, error) {
	if modelName != "tiny" && modelName != "base" && modelName != "small" && modelName != "medium" {
		return nil, errors.New("unsupported ASR model")
	}
	ffmpeg := resolveCommand(a.Settings.FFmpegBin)
	whisper := resolveCommand(a.Settings.WhisperBin)
	if ffmpeg == "" {
		return nil, errors.New("ffmpeg is not installed")
	}
	if whisper == "" {
		return nil, errors.New("whisper.cpp CLI is not installed; set BILI_SCROLL_WHISPER_BIN")
	}
	if strings.TrimSpace(a.Settings.WhisperModelPath) == "" {
		return nil, errors.New("set BILI_SCROLL_WHISPER_MODEL_PATH to a GGML model")
	}
	credential, err := a.credential(ctx, userID)
	if err != nil {
		return nil, err
	}
	audioURL, err := a.Bili.AudioURL(ctx, credential, bvid)
	if err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp("", "bili-scroll-asr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	audioPath := filepath.Join(work, bvid+".m4s")
	wavPath := filepath.Join(work, bvid+".wav")
	outputBase := filepath.Join(work, bvid)
	if err := download(ctx, audioURL, credential.Cookie(), audioPath); err != nil {
		return nil, err
	}
	convert := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-i", audioPath, "-ar", "16000", "-ac", "1", wavPath)
	if output, err := convert.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %s", truncateText(string(output), 500))
	}
	command := exec.CommandContext(ctx, whisper,
		"-m", a.Settings.WhisperModelPath,
		"-f", wavPath,
		"-otxt",
		"-of", outputBase,
		"-l", "auto",
	)
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("whisper failed: %s", truncateText(string(output), 800))
	}
	raw, err := os.ReadFile(outputBase + ".txt")
	if err != nil {
		return nil, errors.New("whisper did not produce a transcript")
	}
	textValue := strings.TrimSpace(string(raw))
	return map[string]any{
		"bvid": bvid, "model": modelName, "text": textValue, "empty": textValue == "",
	}, nil
}

func download(ctx context.Context, source, cookie, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "bili-scroll-agent/0.2-go")
	request.Header.Set("Referer", "https://www.bilibili.com/")
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response, err := (&http.Client{Timeout: 3 * time.Minute}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("audio download failed: HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, io.LimitReader(response.Body, 1<<30))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func resolveCommand(value string) string {
	if value == "" {
		return ""
	}
	if info, err := os.Stat(value); err == nil && !info.IsDir() {
		absolute, _ := filepath.Abs(value)
		return absolute
	}
	found, _ := exec.LookPath(value)
	return found
}
