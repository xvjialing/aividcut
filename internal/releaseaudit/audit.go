// Package releaseaudit 只读检查明确指定的发行输入，不授予安装或修复权限。
package releaseaudit

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const maxFiles = 8192
const maxFileBytes = 512 << 20

type Input struct {
	Role string `json:"role"`
	Path string `json:"path"`
}
type Config struct {
	TargetOS           string  `json:"targetOS"`
	TargetArch         string  `json:"targetArch"`
	PackageRoot        string  `json:"packageRoot"`
	Artifacts          []Input `json:"artifacts"`
	RuntimeDirectories []Input `json:"runtimeDirectories"`
	FrontendDirectory  string  `json:"frontendDirectory,omitempty"`
}
type Finding struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}
type Module struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum,omitempty"`
}
type Import struct {
	Name           string `json:"name"`
	System         bool   `json:"system"`
	ResolvedPath   string `json:"resolvedPath,omitempty"`
	VerifiedInside bool   `json:"verifiedInside,omitempty"`
	Delayed        bool   `json:"delayed,omitempty"`
}
type Artifact struct {
	Role          string            `json:"role"`
	Path          string            `json:"path"`
	ResolvedPath  string            `json:"resolvedPath,omitempty"`
	SHA256        string            `json:"sha256,omitempty"`
	Bytes         int64             `json:"bytes"`
	Format        string            `json:"format,omitempty"`
	Architectures []string          `json:"architectures,omitempty"`
	Imports       []Import          `json:"imports,omitempty"`
	DebugSections bool              `json:"debugSections"`
	GoVersion     string            `json:"goVersion,omitempty"`
	GoSettings    map[string]string `json:"goSettings,omitempty"`
	GoModules     []Module          `json:"goModules,omitempty"`
	Probe         *Probe            `json:"probe,omitempty"`
}
type Directory struct {
	Role   string `json:"role"`
	Path   string `json:"path"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256,omitempty"`
}
type Report struct {
	SchemaVersion   int         `json:"schemaVersion"`
	TargetOS        string      `json:"targetOS"`
	TargetArch      string      `json:"targetArch"`
	PackageRoot     string      `json:"packageRoot"`
	Artifacts       []Artifact  `json:"artifacts"`
	Directories     []Directory `json:"directories"`
	Findings        []Finding   `json:"findings"`
	Blockers        int         `json:"blockers"`
	Reviews         int         `json:"reviews"`
	ReleaseVerified bool        `json:"releaseVerified"`
	Limits          []string    `json:"limits"`
}
type Options struct{ ProbeFFmpeg bool }

func ReadConfig(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return c, errors.New("配置必须为64KiB以内的普通文件")
	}
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return c, errors.New("配置末尾必须为空")
	}
	base, e := filepath.Abs(filepath.Dir(path))
	if e != nil {
		return c, e
	}
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	c.PackageRoot = abs(c.PackageRoot)
	c.FrontendDirectory = abs(c.FrontendDirectory)
	for i := range c.Artifacts {
		c.Artifacts[i].Path = abs(c.Artifacts[i].Path)
	}
	for i := range c.RuntimeDirectories {
		c.RuntimeDirectories[i].Path = abs(c.RuntimeDirectories[i].Path)
	}
	return c, nil
}

func Audit(ctx context.Context, c Config, options Options) (Report, error) {
	r := Report{SchemaVersion: 1, TargetOS: c.TargetOS, TargetArch: c.TargetArch, PackageRoot: c.PackageRoot, Artifacts: []Artifact{}, Directories: []Directory{}, Findings: []Finding{}, Limits: []string{"普通/延迟PE导入不覆盖动态MLT/Qt加载、驱动和所有传递依赖，不是完整SBOM", "未验证签名/公证、对应源码、许可履行、专利和双端安装运行", "去符号/trimpath不能保证源码或前端资源不可提取"}}
	if e := ctx.Err(); e != nil {
		return r, e
	}
	if (c.TargetOS != "darwin" && c.TargetOS != "windows") || (c.TargetArch != "arm64" && c.TargetArch != "amd64") || c.PackageRoot == "" || len(c.Artifacts) > 32 || len(c.RuntimeDirectories) > 8 {
		return r, errors.New("目标、包根或输入数量无效")
	}
	root, e := filepath.EvalSymlinks(c.PackageRoot)
	if e != nil {
		return r, e
	}
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() {
		return r, errors.New("包根必须为存在的目录")
	}
	r.PackageRoot = root
	add := func(level, code, path, message string) {
		r.Findings = append(r.Findings, Finding{level, code, path, message})
		if level == "blocker" {
			r.Blockers++
		}
		if level == "review" {
			r.Reviews++
		}
	}
	roles := map[string]bool{}
	for _, v := range c.Artifacts {
		if e := ctx.Err(); e != nil {
			return r, e
		}
		if v.Role != "app" && v.Role != "worker" && v.Role != "ffmpeg" && v.Role != "ffprobe" && v.Role != "ocr" {
			return r, fmt.Errorf("未知产物角色：%s", v.Role)
		}
		if roles[v.Role] {
			return r, fmt.Errorf("重复产物角色：%s", v.Role)
		}
		roles[v.Role] = true
		a := Artifact{Role: v.Role, Path: v.Path}
		resolved, err := filepath.EvalSymlinks(v.Path)
		if err != nil {
			add("blocker", "artifact_missing", v.Path, "指定产物不可读取")
			r.Artifacts = append(r.Artifacts, a)
			continue
		}
		a.ResolvedPath = resolved
		if !within(root, resolved) {
			add("blocker", "outside_package", v.Path, "产物在发行包外；开发机可用不等于随包可用")
		}
		a, err = inspect(v, a)
		if err != nil {
			add("blocker", "artifact_invalid", v.Path, err.Error())
			r.Artifacts = append(r.Artifacts, a)
			continue
		}
		if a.Format != c.TargetOS || !contains(a.Architectures, c.TargetArch) {
			add("blocker", "target_mismatch", v.Path, "产物OS或架构与目标不一致")
		}
		for i, dependency := range a.Imports {
			if dependency.System {
				continue
			}
			if filepath.IsAbs(dependency.Name) && !within(root, dependency.Name) {
				add("blocker", "external_import", v.Path, "链接包外库："+dependency.Name)
			} else if target, known := importTarget(a.ResolvedPath, dependency.Name); known {
				if !within(root, target) {
					add("blocker", "external_import", v.Path, "相对依赖越出发行包："+dependency.Name)
					continue
				}
				resolved, err := filepath.EvalSymlinks(target)
				if err != nil {
					add("blocker", "missing_import", v.Path, "包内依赖缺失："+dependency.Name)
					continue
				}
				info, err := os.Stat(resolved)
				if err != nil || !info.Mode().IsRegular() || !within(root, resolved) {
					add("blocker", "external_import", v.Path, "依赖未解析为包内普通文件："+dependency.Name)
					continue
				}
				a.Imports[i].ResolvedPath = resolved
				a.Imports[i].VerifiedInside = true
			} else {
				add("review", "import_resolution", v.Path, "需核对打包及加载路径："+dependency.Name)
			}
		}
		if a.DebugSections {
			add("review", "debug_sections", v.Path, "产物仍有调试段，请核对发行符号策略")
		}
		if options.ProbeFFmpeg && (v.Role == "ffmpeg" || v.Role == "ffprobe") {
			if c.TargetOS != runtime.GOOS || c.TargetArch != runtime.GOARCH || a.Format != runtime.GOOS || !contains(a.Architectures, runtime.GOARCH) {
				add("review", "probe_foreign", v.Path, "外来平台产物不执行")
			} else {
				a.Probe, err = probeVersion(ctx, v.Path, v.Role, a.SHA256)
				if err != nil {
					add("review", "probe_failed", v.Path, err.Error())
				} else if a.Probe.Nonfree {
					add("blocker", "ffmpeg_nonfree", v.Path, "构建启用nonfree，需重新核对可再发行性")
				} else if a.Probe.GPL {
					add("review", "ffmpeg_gpl", v.Path, "观察到GPL构建，需核对分发范围、许可证及对应源码")
				}
			}
		} else if v.Role == "ffmpeg" || v.Role == "ffprobe" {
			add("review", "ffmpeg_unprobed", v.Path, "未执行版本探测，不能推断具体构建许可")
		}
		r.Artifacts = append(r.Artifacts, a)
	}
	for _, role := range []string{"app", "worker", "ffmpeg", "ffprobe"} {
		if !roles[role] {
			add("blocker", "missing_role", "", "发行输入缺少"+role)
		}
	}
	dirRoles := map[string]bool{}
	for _, v := range c.RuntimeDirectories {
		if (v.Role != "mlt_repository" && v.Role != "mlt_profiles") || dirRoles[v.Role] {
			return r, errors.New("运行时目录角色无效或重复")
		}
		dirRoles[v.Role] = true
		d, err := scanDirectory(ctx, root, v, false, add)
		r.Directories = append(r.Directories, d)
		if err != nil {
			add("blocker", "directory_invalid", v.Path, err.Error())
		}
	}
	for _, role := range []string{"mlt_repository", "mlt_profiles"} {
		if !dirRoles[role] {
			add("blocker", "missing_runtime", "", "发行输入缺少"+role)
		}
	}
	if c.FrontendDirectory != "" {
		d, err := scanDirectory(ctx, root, Input{"frontend", c.FrontendDirectory}, true, add)
		r.Directories = append(r.Directories, d)
		if err != nil {
			add("blocker", "frontend_invalid", c.FrontendDirectory, err.Error())
		}
		r.Limits = append(r.Limits, "前端扫描仅覆盖指定dist目录，不证明嵌入二进制逐字节相同")
	}
	add("review", "license_closure", "", "需按实际MLT模块、Qt、FFmpeg及其它依赖核对许可文本、构建方式和对应源码；不以顶层包名代替许可闭包")
	return r, nil
}
func within(root, path string) bool {
	rel, e := filepath.Rel(root, filepath.Clean(path))
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func contains(values []string, v string) bool {
	for _, s := range values {
		if s == v {
			return true
		}
	}
	return false
}
func inspect(v Input, a Artifact) (Artifact, error) {
	f, e := os.Open(v.Path)
	if e != nil {
		return a, e
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil || !before.Mode().IsRegular() {
		return a, errors.New("产物必须为普通文件")
	}
	if before.Size() > maxFileBytes {
		return a, errors.New("单文件超过512MiB检查边界")
	}
	a.Bytes = before.Size()
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(f, maxFileBytes+1)); e != nil {
		return a, e
	}
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	if e = binaryMetadata(f, &a); e != nil {
		return a, e
	}
	if b, err := buildinfo.Read(f); err == nil {
		a.GoVersion = b.GoVersion
		a.GoSettings = map[string]string{}
		for _, s := range b.Settings {
			if contains([]string{"-trimpath", "-tags", "-buildmode", "CGO_ENABLED", "GOOS", "GOARCH"}, s.Key) {
				a.GoSettings[s.Key] = s.Value
			}
		}
		for _, m := range b.Deps {
			if m.Replace != nil {
				m = m.Replace
			}
			a.GoModules = append(a.GoModules, Module{m.Path, m.Version, m.Sum})
		}
	}
	after, e := os.Stat(v.Path)
	resolved, resolveErr := filepath.EvalSymlinks(v.Path)
	if e != nil || resolveErr != nil || resolved != a.ResolvedPath || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return a, errors.New("检查期间产物变化")
	}
	return a, nil
}
func binaryMetadata(f *os.File, a *Artifact) error {
	imports := map[string]bool{}
	delayed := map[string]bool{}
	archs := map[string]bool{}
	mach := func(m *macho.File) {
		a.Format = "darwin"
		arch := "unknown"
		if m.Cpu == macho.CpuArm64 {
			arch = "arm64"
		}
		if m.Cpu == macho.CpuAmd64 {
			arch = "amd64"
		}
		archs[arch] = true
		libs, _ := m.ImportedLibraries()
		for _, n := range libs {
			imports[n] = true
		}
		for _, s := range m.Sections {
			if s.Seg == "__DWARF" {
				a.DebugSections = true
			}
		}
	}
	if m, e := macho.NewFile(f); e == nil {
		mach(m)
	} else if fat, e := macho.NewFatFile(f); e == nil {
		for _, m := range fat.Arches {
			mach(m.File)
		}
	} else if p, e := pe.NewFile(f); e == nil {
		a.Format = "windows"
		switch p.Machine {
		case pe.IMAGE_FILE_MACHINE_AMD64:
			archs["amd64"] = true
		case pe.IMAGE_FILE_MACHINE_ARM64:
			archs["arm64"] = true
		default:
			archs["unknown"] = true
		}
		libs, e := peImportDetails(p, f)
		if e != nil {
			return e
		}
		for _, n := range libs {
			imports[n.Name] = true
			if n.Delayed {
				delayed[n.Name] = true
			}
		}
		for _, s := range p.Sections {
			if strings.HasPrefix(s.Name, ".debug") {
				a.DebugSections = true
			}
		}
	} else {
		return errors.New("不是受支持的Mach-O或PE二进制")
	}
	for n := range archs {
		a.Architectures = append(a.Architectures, n)
	}
	sort.Strings(a.Architectures)
	names := []string{}
	for n := range imports {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a.Imports = append(a.Imports, Import{Name: n, System: systemImport(a.Format, n), Delayed: delayed[n]})
	}
	return nil
}

func importTarget(image, name string) (string, bool) {
	if filepath.IsAbs(name) {
		return filepath.Clean(name), true
	}
	if strings.HasPrefix(name, "@loader_path/") {
		return filepath.Clean(filepath.Join(filepath.Dir(image), strings.TrimPrefix(name, "@loader_path/"))), true
	}
	return "", false
}
func systemImport(format, name string) bool {
	if format == "darwin" {
		return within("/System/Library", name) || within("/usr/lib", name)
	}
	n := strings.ToLower(name)
	if strings.ContainsAny(n, "/\\") {
		return false
	}
	return strings.HasPrefix(n, "api-ms-win-") || strings.HasPrefix(n, "ext-ms-win-") || contains([]string{"kernel32.dll", "ntdll.dll", "user32.dll", "advapi32.dll", "shell32.dll", "ole32.dll", "oleaut32.dll", "ws2_32.dll", "bcrypt.dll", "secur32.dll", "crypt32.dll", "gdi32.dll", "comdlg32.dll", "comctl32.dll", "shlwapi.dll", "version.dll", "winmm.dll", "uxtheme.dll", "dwmapi.dll"}, n)
}
func scanDirectory(ctx context.Context, root string, v Input, frontend bool, add func(string, string, string, string)) (Directory, error) {
	d := Directory{Role: v.Role, Path: v.Path}
	resolved, e := filepath.EvalSymlinks(v.Path)
	if e != nil {
		return d, e
	}
	st, e := os.Stat(resolved)
	if e != nil || !st.IsDir() {
		return d, errors.New("指定路径不是目录")
	}
	if !frontend && !within(root, resolved) {
		add("blocker", "runtime_outside_package", v.Path, "运行时目录不在发行包内")
	}
	h := sha256.New()
	visited := 0
	e = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		visited++
		if visited > maxFiles*4 {
			return errors.New("目录节点数超过检查边界")
		}
		if entry.IsDir() {
			return nil
		}
		d.Files++
		if d.Files > maxFiles {
			return errors.New("目录文件数超过8192检查边界")
		}
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, e := filepath.EvalSymlinks(path)
			if e != nil {
				return e
			}
			if !within(root, target) {
				add("blocker", "external_symlink", path, "符号链接指向发行包外")
			}
			add("review", "symlink_not_walked", path, "不递归跟随目录内符号链接，需核对目标内容")
			link, e := os.Readlink(path)
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(resolved, path)
			fmt.Fprintln(h, filepath.ToSlash(rel), "symlink", link)
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("目录含非普通文件")
		}
		if info.Size() > maxFileBytes || d.Bytes+info.Size() > 2<<30 {
			return errors.New("目录内容超过单文件512MiB/总计2GiB边界")
		}
		d.Bytes += info.Size()
		rel, _ := filepath.Rel(resolved, path)
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		fileHash := sha256.New()
		var reader io.Reader = f
		if frontend {
			inline, err := hashFrontend(ctx, f, fileHash)
			if err != nil {
				return err
			}
			if strings.HasSuffix(strings.ToLower(path), ".map") || contains([]string{".ts", ".tsx", ".jsx"}, strings.ToLower(filepath.Ext(path))) || inline {
				add("blocker", "frontend_source", path, "前端目录含源码或source map")
			}
			reader = strings.NewReader("")
		}
		if _, e = io.Copy(fileHash, reader); e != nil {
			return e
		}
		after, e := os.Stat(path)
		if e != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return errors.New("检查期间目录内容变化")
		}
		fmt.Fprintf(h, "%s\x00%x\n", filepath.ToSlash(rel), fileHash.Sum(nil))
		return nil
	})
	final, statErr := os.Stat(resolved)
	finalResolved, resolveErr := filepath.EvalSymlinks(v.Path)
	if e == nil && (statErr != nil || resolveErr != nil || resolved != finalResolved || !os.SameFile(st, final) || !st.ModTime().Equal(final.ModTime())) {
		e = errors.New("检查期间目录变化")
	}
	if e == nil {
		if d.Files == 0 {
			return d, errors.New("指定内容目录为空")
		}
		d.SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	return d, e
}
