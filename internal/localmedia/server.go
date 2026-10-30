package localmedia

import (
	"aividcut/internal/projectfonts"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"image/jpeg"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 素材服务仅监听回环地址，URL 是本次进程会话的有界访问能力。
type Server struct {
	mu        sync.RWMutex
	files     map[string]string
	fonts     map[string]string
	token     string
	base      string
	listener  net.Listener
	server    *http.Server
	blobs     map[string]jpegBlob
	blobClock uint64
}

type jpegBlob struct {
	data    []byte
	stamp   uint64
	created time.Time
}

// RegisterJPEG只接收有界缩略图，不开放任意内容或路径的新访问入口。
func (s *Server) RegisterJPEG(id string, data []byte) (string, error) {
	return s.registerJPEG(id, data, 320, 180)
}

// RegisterSourceJPEG接收更大的受控源帧，沿用相同令牌、字节和缓存预算。
func (s *Server) RegisterSourceJPEG(id string, data []byte) (string, error) {
	return s.registerJPEG(id, data, 960, 540)
}
func (s *Server) registerJPEG(id string, data []byte, width, height int) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\?#") || len(data) == 0 || len(data) > 256<<10 {
		return "", errors.New("缩略图编号或数据无效")
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > width || config.Height > height {
		return "", fmt.Errorf("派生图必须为%d×%d以内的有效JPEG", width, height)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.files[id]; exists {
		return "", errors.New("缩略图编号与素材冲突")
	}
	if s.blobs == nil {
		s.blobs = map[string]jpegBlob{}
	}
	s.blobClock++
	s.blobs[id] = jpegBlob{data: append([]byte{}, data...), stamp: s.blobClock, created: time.Now()}
	for {
		var total int
		var oldest string
		var stamp uint64
		for key, value := range s.blobs {
			total += len(value.data)
			if oldest == "" || value.stamp < stamp {
				oldest, stamp = key, value.stamp
			}
		}
		if len(s.blobs) <= 128 && total <= 32<<20 {
			break
		}
		delete(s.blobs, oldest)
	}
	return fmt.Sprintf("%s/media/%s?token=%s", s.base, url.PathEscape(id), s.token), nil
}

func New() (*Server, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{files: map[string]string{}, token: hex.EncodeToString(random[:]), base: "http://" + ln.Addr().String(), listener: ln}
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	go s.server.Serve(ln)
	return s, nil
}

func (s *Server) Register(id, path string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\?#") {
		return "", errors.New("无效的媒体编号")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("素材文件不存在或不是普通文件")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if _, exists := s.blobs[id]; exists {
		s.mu.Unlock()
		return "", errors.New("素材编号与缩略图冲突")
	}
	delete(s.fonts, id)
	s.files[id] = absolute
	s.mu.Unlock()
	return fmt.Sprintf("%s/media/%s?token=%s", s.base, url.PathEscape(id), s.token), nil
}

// RegisterFont 仅为已校验的工程字体开放带能力令牌的字节读取；普通媒体不增加CORS。
func (s *Server) RegisterFont(id, path, expected string) (string, error) {
	font, err := projectfonts.Read(path)
	if err != nil {
		return "", err
	}
	expected = strings.TrimPrefix(expected, "sha256:")
	if len(expected) != 64 || font.SHA256 != expected {
		return "", errors.New("工程字体内容已变化，请重新导入或定位原字体")
	}
	address, err := s.Register(id, path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.fonts == nil {
		s.fonts = map[string]string{}
	}
	s.fonts[id] = expected
	s.mu.Unlock()
	return address, nil
}

func (s *Server) Reset() {
	s.mu.Lock()
	s.files = map[string]string{}
	s.fonts = map[string]string{}
	s.blobs = map[string]jpegBlob{}
	s.mu.Unlock()
}

func (s *Server) Unregister(id string) {
	s.mu.Lock()
	delete(s.files, id)
	delete(s.fonts, id)
	delete(s.blobs, id)
	s.mu.Unlock()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.token)) != 1 {
		http.Error(w, "没有素材访问权限", http.StatusForbidden)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/media/")
	if id == r.URL.Path || strings.ContainsAny(id, "/\\") {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	blob, found := s.blobs[id]
	if found {
		s.blobClock++
		blob.stamp = s.blobClock
		s.blobs[id] = blob
	}
	s.mu.Unlock()
	if found {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		w.Header().Set("Cache-Control", "private, no-store")
		http.ServeContent(w, r, id+".jpg", blob.created, bytes.NewReader(blob.data))
		return
	}
	s.mu.RLock()
	path, ok := s.files[id]
	fontSHA, isFont := s.fonts[id]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if isFont {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "private, no-store")
		font, err := projectfonts.Read(path)
		if err != nil || font.SHA256 != fontSHA {
			http.Error(w, "工程字体已变化或无法读取", http.StatusGone)
			return
		}
		kind := "font/ttf"
		if strings.EqualFold(filepath.Ext(path), ".otf") {
			kind = "font/otf"
		}
		w.Header().Set("Content-Type", kind)
		http.ServeContent(w, r, filepath.Base(path), time.Time{}, bytes.NewReader(font.Data))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}
