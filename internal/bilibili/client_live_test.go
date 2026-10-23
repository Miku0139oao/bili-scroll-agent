package bilibili

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveSearch(t *testing.T) {
	if os.Getenv("BILI_SCROLL_LIVE_BILIBILI") != "1" {
		t.Skip("set BILI_SCROLL_LIVE_BILIBILI=1 for the live Bilibili smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := New()
	videos, err := client.Search(ctx, "DeepSeek", 3, "pubdate", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) == 0 {
		t.Log("live search returned no videos; the API response was structurally valid")
		return
	}
	for _, video := range videos {
		if video.BVID == "" || video.Title == "" {
			t.Fatalf("invalid live result: %+v", video)
		}
	}
}

func TestLiveQRLoginGeneration(t *testing.T) {
	if os.Getenv("BILI_SCROLL_LIVE_BILIBILI") != "1" {
		t.Skip("set BILI_SCROLL_LIVE_BILIBILI=1 for the live Bilibili smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key, loginURL, _, err := New().StartLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if key == "" || loginURL == "" {
		t.Fatal("empty QR login response")
	}
}

func TestLivePublicVideoAndSubtitleShape(t *testing.T) {
	if os.Getenv("BILI_SCROLL_LIVE_BILIBILI") != "1" {
		t.Skip("set BILI_SCROLL_LIVE_BILIBILI=1 for the live Bilibili smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := New()
	if err := client.prepareAnonymous(ctx); err != nil {
		t.Fatal(err)
	}
	credential := Credential{
		Buvid3: strings.TrimPrefix(strings.Split(client.anonCookie, ";")[0], "buvid3="),
	}
	video, err := client.VideoSummary(ctx, credential, "BV1d8pU6xEZX")
	if err != nil {
		t.Fatal(err)
	}
	if video.BVID == "" || video.Title == "" {
		t.Fatalf("invalid video: %+v", video)
	}
	subtitle, err := client.Subtitle(ctx, credential, video.BVID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := subtitle["empty"]; !ok {
		t.Fatalf("subtitle response missing empty flag: %#v", subtitle)
	}
}
