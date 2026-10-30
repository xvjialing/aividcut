//go:build production

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestProductionDependencyInventoryMatchesEmbeddedChunks(t *testing.T) {
	data, err := assets.ReadFile("frontend/dist/dependency-inventory.json")
	if err != nil {
		t.Fatal("正式包缺少实际构建依赖清单", err)
	}
	var inventory struct {
		SchemaVersion  int                                        `json:"schemaVersion"`
		LockfileName   string                                     `json:"lockfileName"`
		LockfileSHA256 string                                     `json:"lockfileSHA256"`
		PackageManager string                                     `json:"packageManager"`
		Packages       []struct{ Name, Version, LockPath string } `json:"packages"`
		Chunks         []struct {
			File, SHA256 string
			Bytes        int
		} `json:"chunks"`
		LicenseApproval bool `json:"licenseApproval"`
	}
	if err = json.Unmarshal(data, &inventory); err != nil || inventory.SchemaVersion != 2 || inventory.LockfileName != "pnpm-lock.yaml" || len(inventory.LockfileSHA256) != 64 || !strings.HasPrefix(inventory.PackageManager, "pnpm@") || len(inventory.Packages) == 0 || len(inventory.Chunks) == 0 || inventory.LicenseApproval {
		t.Fatal("正式依赖清单无效或冒充许可批准", err)
	}
	for _, chunk := range inventory.Chunks {
		if !fs.ValidPath(chunk.File) || strings.Contains(chunk.File, "\\") {
			t.Fatal("嵌入chunk不是安全相对路径")
		}
		content, err := assets.ReadFile("frontend/dist/" + chunk.File)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		if len(content) != chunk.Bytes || hex.EncodeToString(sum[:]) != chunk.SHA256 {
			t.Fatal("实际嵌入chunk与清单不同", chunk.File)
		}
	}
	for _, pkg := range inventory.Packages {
		if pkg.Name == "" || pkg.Version == "" || !strings.HasPrefix(pkg.LockPath, "node_modules/") || strings.Contains(pkg.LockPath, "..") {
			t.Fatal("实际包身份无效")
		}
	}
	if strings.Contains(string(data), "/Users/") || strings.Contains(string(data), "src/App.tsx") {
		t.Fatal("依赖清单泄漏开发路径或自有源码模块名")
	}
}

func TestProductionBundleExcludesFrontendSourcesAndInlineMaps(t *testing.T) {
	files := 0
	err := fs.WalkDir(assets, "frontend/dist", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files++
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".map" || ext == ".ts" || ext == ".tsx" || ext == ".jsx" {
			t.Errorf("正式嵌入资源不能包含前端源码或映射：%s", path)
		}
		if ext == ".js" || ext == ".css" || ext == ".html" {
			data, err := assets.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), "sourceMappingURL=data:") {
				t.Errorf("正式资源不能含内联source map：%s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("正式嵌入资源为空")
	}
}

func TestProductionEmbeddedAssetBundle(t *testing.T) {
	handler := application.AssetFileServerFS(assets)
	read := func(path string) string {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 || response.Body.Len() == 0 {
			t.Fatalf("打包资源不可用：%s，状态%d", path, response.Code)
		}
		contentType := response.Header().Get("Content-Type")
		if strings.HasSuffix(path, ".js") && !strings.Contains(contentType, "javascript") {
			t.Fatal("脚本类型错误", path, contentType)
		}
		if strings.HasSuffix(path, ".css") && !strings.Contains(contentType, "text/css") {
			t.Fatal("样式类型错误", path, contentType)
		}
		return response.Body.String()
	}
	body := read("/")
	links := regexp.MustCompile(`(?:src|href)="(/assets/[^\"]+)"`).FindAllStringSubmatch(body, -1)
	if len(links) < 2 {
		t.Fatal("入口未声明脚本与样式")
	}
	for _, link := range links {
		read(link[1])
	}
}
