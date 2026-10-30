package localmedia

import (
	"aividcut/internal/projectfonts"
	"bytes"
	"golang.org/x/image/font/gofont/goregular"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredMediaSupportsRangesAndRejectsUnsignedRequests(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	u, _ := s.Register("asset-one", path)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Range", "bytes=3-6")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(body) != "3456" {
		t.Fatalf("Range 结果 %d: %s", resp.StatusCode, body)
	}
	resp, err = http.Get(strings.Split(u, "?")[0])
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("未签名请求返回 %d", resp.StatusCode)
	}
	s.Reset()
	resp, _ = http.Get(u)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal("切换工程后旧素材仍可访问")
	}
}

func TestOnlyVerifiedFontsAllowSignedByteReads(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "font.ttf")
	if err = os.WriteFile(path, goregular.TTF, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := projectfonts.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	address, err := s.RegisterFont("font", path, "sha256:"+file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("GET", address, nil)
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Access-Control-Allow-Origin") != "*" || !bytes.Equal(data, goregular.TTF) {
		t.Fatal("字体没有通过能力读取", response.StatusCode)
	}
	response, err = http.Get(strings.Split(address, "?")[0])
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 || response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("未授权字体读取未拒绝")
	}
	if err = os.WriteFile(path, []byte("不能作为字体读取的其它内容"), 0600); err != nil {
		t.Fatal(err)
	}
	response, err = http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 410 || bytes.Contains(data, []byte("其它内容")) {
		t.Fatal("字体被替换后仍返回其它文件内容")
	}
	s.Reset()
	response, err = http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("旧字体地址未失效")
	}
}
