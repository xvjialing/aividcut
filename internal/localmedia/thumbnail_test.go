package localmedia

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThumbnailBlobCapabilitiesLimitsRangesAndReset(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var output bytes.Buffer
	if err := jpeg.Encode(&output, image.NewRGBA(image.Rect(0, 0, 32, 18)), nil); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	url, err := s.RegisterJPEG("thumb", data)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Range", "bytes=0-9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || !bytes.Equal(body, data[:10]) || resp.Header.Get("Content-Type") != "image/jpeg" || resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("缩略图响应不符", resp.StatusCode, resp.Header)
	}
	resp, err = http.Get(strings.Split(url, "?")[0])
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("未签名访问被接受")
	}
	for _, bad := range []struct {
		id   string
		data []byte
	}{{"../bad", data}, {"bad", []byte("fake")}, {"large", make([]byte, (256<<10)+1)}} {
		if _, err := s.RegisterJPEG(bad.id, bad.data); err == nil {
			t.Fatal("非法blob被接受", bad.id)
		}
	}
	path := filepath.Join(t.TempDir(), "file")
	os.WriteFile(path, []byte("x"), 0600)
	if _, err := s.Register("thumb", path); err == nil {
		t.Fatal("覆盖图片能力")
	}
	s.Register("source", path)
	if _, err := s.RegisterJPEG("source", data); err == nil {
		t.Fatal("覆盖素材能力")
	}
	for i := 0; i < 130; i++ {
		if _, err := s.RegisterJPEG(fmt.Sprint("t", i), data); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.RLock()
	count, total := len(s.blobs), 0
	for _, v := range s.blobs {
		total += len(v.data)
	}
	s.mu.RUnlock()
	if count != 128 || total > 32<<20 {
		t.Fatal("缓存超界", count, total)
	}
	resp, _ = http.Get(url)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("最旧图片未淘汰")
	}
	fresh, _ := s.RegisterJPEG("fresh", data)
	s.Reset()
	resp, _ = http.Get(fresh)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("切换后旧图片能力未撤销")
	}
}

func TestSourceFrameJPEGKeepsThumbnailAndTokenBoundaries(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var data bytes.Buffer
	if err = jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 960, 540)), nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterJPEG("thumbnail-too-large", data.Bytes()); err == nil {
		t.Fatal("普通缩略图边界被放宽")
	}
	address, err := s.RegisterSourceJPEG("source-detail", data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Equal(body, data.Bytes()) || response.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatal("详细源帧没有经原令牌媒体服务返回")
	}
	response, err = http.Get(strings.Split(address, "?")[0])
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("详细源帧绕过访问令牌")
	}
	var oversized bytes.Buffer
	jpeg.Encode(&oversized, image.NewRGBA(image.Rect(0, 0, 962, 540)), nil)
	if _, err = s.RegisterSourceJPEG("oversized", oversized.Bytes()); err == nil {
		t.Fatal("源帧尺寸上界未保持")
	}
}
