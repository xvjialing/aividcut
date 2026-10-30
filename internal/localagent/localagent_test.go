package localagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"aividcut/internal/editor"
)

const fixtureEnvironment = "AIVIDCUT_LOCALAGENT_FIXTURE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fixtureEnvironment); mode != "" {
		os.Exit(fixtureCLI(mode))
	}
	os.Exit(m.Run())
}

// fixture 是真正的子进程，不访问任何提供商或用户认证文件。
func fixtureCLI(mode string) int {
	if strings.HasPrefix(mode, "execute-") {
		return fixtureExecutionCLI(mode)
	}
	if strings.HasPrefix(mode, "inspect-") {
		return fixtureMetadata(mode)
	}
	if mode == "sleep" {
		time.Sleep(60 * time.Second)
		return 0
	}
	if mode == "flood" {
		for i := 0; i < 10000; i++ {
			if _, err := os.Stdout.Write(bytes.Repeat([]byte("x"), 2048)); err != nil {
				return 1
			}
		}
		return 0
	}
	if mode == "bad-json" {
		fmt.Println("not-json")
		return 0
	}
	if mode == "exit" {
		fmt.Fprintln(os.Stderr, "sk-test-SECRET-should-not-be-shown")
		return 2
	}
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 512<<10))
	parts := bytes.SplitN(data, []byte("<AIVIDCUT_CONTEXT>\n"), 2)
	if len(parts) != 2 {
		return 3
	}
	var payload struct {
		Project  editor.Project `json:"project"`
		Expected struct {
			Revision int64  `json:"baseRevision"`
			Key      string `json:"idempotencyKey"`
		} `json:"expectedResponse"`
	}
	if json.Unmarshal(parts[1], &payload) != nil {
		return 4
	}
	track := ""
	for _, candidate := range payload.Project.Tracks {
		if candidate.Kind == "text" {
			track = candidate.ID
			break
		}
	}
	clip := editor.DefaultClip("fixture-title", track, "text")
	clip.Name, clip.Text, clip.DurationFrames = "标题", "欢迎使用 AIVidCut", 30
	request := editor.EditRequest{BaseRevision: payload.Expected.Revision, IdempotencyKey: payload.Expected.Key, Label: "添加标题", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}}
	if mode == "wrong-revision" {
		request.BaseRevision++
	}
	if mode == "wrong-key" {
		request.IdempotencyKey = "other"
	}
	if mode == "unsafe-asset" {
		request.Commands = []editor.Command{{Type: "add_asset", Asset: &editor.Asset{ID: "secret", Kind: "image", Path: "/private/secret.png"}}}
	}
	if mode == "outside-selection" {
		request.Commands[0].Clip.StartFrame = 10000
	}
	encoded, _ := json.Marshal(request)
	claude := false
	finalPath := ""
	for i, arg := range os.Args[1:] {
		if arg == "--safe-mode" {
			claude = true
		}
		if arg == "--output-last-message" {
			finalPath = os.Args[i+2]
		}
	}
	if mode == "tool" {
		if claude {
			fmt.Println(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","id":"bad"}]}}`)
		} else {
			fmt.Println(`{"type":"item.started","item":{"type":"command_execution","command":"touch /tmp/unsafe"}}`)
		}
		time.Sleep(time.Second)
		return 0
	}
	if claude {
		fmt.Println(`{"type":"system","subtype":"init","tools":["StructuredOutput"],"mcp_servers":[]}`)
		fmt.Println(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"StructuredOutput","id":"structured-1"}]}}`)
		fmt.Println(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"structured-1"}]}}`)
		fmt.Printf("{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"structured_output\":%s}\n", encoded)
	} else {
		fmt.Println(`{"type":"thread.started","thread_id":"fixture"}`)
		fmt.Println(`{"type":"turn.started"}`)
		message, _ := json.Marshal(string(encoded))
		fmt.Printf("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":%s}}\n", message)
		if finalPath != "" {
			if err := os.WriteFile(finalPath, encoded, 0600); err != nil {
				return 5
			}
		}
		if mode != "incomplete" {
			fmt.Println(`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":10}}`)
		}
	}
	return 0
}

func fixtureMetadata(mode string) int {
	args := os.Args[1:]
	if len(args) == 0 {
		return 9
	}
	switch args[0] {
	case "features":
		for _, name := range restrictedCodexFeatures {
			value := "false"
			if mode == "inspect-feature-enabled" && name == "hooks" {
				value = "true"
			}
			fmt.Printf("%s stable %s\n", name, value)
		}
		fmt.Println("unified_exec stable true")
	case "mcp":
		if mode == "inspect-mcp" {
			fmt.Println(`[{"name":"external","enabled":true}]`)
		} else {
			fmt.Println(`[]`)
		}
	case "app-server":
		if mode == "inspect-requirements" {
			fmt.Println(`{"appServerVersion":"0.164.0","configRequirements":{"requirements":{"featureRequirements":{"hooks":true}}}}`)
		} else if mode == "inspect-incomplete" {
			fmt.Println(`{"appServerVersion":"0.164.0","configRequirements":{}}`)
		} else {
			fmt.Println(`{"appServerVersion":"0.164.0","configRequirements":{"requirements":null}}`)
		}
	default:
		return 8
	}
	return 0
}

func fixturePlanner(t *testing.T, mode string) *Planner {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	planner, err := NewPlanner(Config{StateDir: t.TempDir(), WorkDir: t.TempDir(), Executables: map[Provider]string{Codex: executable, Claude: executable}, Timeout: 3 * time.Second, MaxOutputBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	planner.inspect = func(_ context.Context, provider Provider, path, profile string) Installation {
		return Installation{Provider: provider, Path: path, Installed: true, AuthState: "signed_in", ProfileDirectory: profile, Isolation: Isolation{Supported: true, Status: "test_fixture"}}
	}
	planner.environment = func(provider Provider, profile string) []string {
		return append(childEnvironment(provider, profile), fixtureEnvironment+"="+mode)
	}
	return planner
}

func TestExecutableFixturePlansAreValidatedWithoutEditing(t *testing.T) {
	for _, provider := range []Provider{Codex, Claude} {
		t.Run(string(provider), func(t *testing.T) {
			planner := fixturePlanner(t, "success")
			project := editor.NewProject("测试工程")
			before := editor.CloneProject(project)
			var events []Event
			result, err := planner.Plan(context.Background(), PlanInput{Provider: provider, Project: project, Instruction: "添加标题"}, func(event Event) { events = append(events, event) })
			if err != nil {
				t.Fatal(err)
			}
			if !result.CoreValidated || len(result.Request.Commands) != 1 || len(events) == 0 {
				t.Fatalf("规划结果无效：%+v", result)
			}
			if !reflect.DeepEqual(before, project) {
				t.Fatal("规划改变了原工程")
			}
			leftovers, _ := os.ReadDir(planner.config.WorkDir)
			if len(leftovers) != 0 {
				t.Fatal("规划后遗留含上下文的临时文件")
			}
		})
	}
}

func TestExecutableFixtureRejectsInvalidPlansAndTools(t *testing.T) {
	for _, mode := range []string{"bad-json", "tool", "wrong-revision", "wrong-key", "unsafe-asset", "incomplete", "exit"} {
		t.Run(mode, func(t *testing.T) {
			planner := fixturePlanner(t, mode)
			_, err := planner.Plan(context.Background(), PlanInput{Provider: Codex, Project: editor.NewProject("原工程"), Instruction: "添加标题"}, nil)
			if err == nil {
				t.Fatal("不安全或不完整的输出不应成为可提交计划")
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatal("原始CLI错误泄露了敏感输出")
			}
		})
	}
}

func TestPlannerIsolationUnknownNeverStartsCLI(t *testing.T) {
	planner := fixturePlanner(t, "sleep")
	planner.inspect = func(context.Context, Provider, string, string) Installation {
		return Installation{Installed: true, AuthState: "signed_in", Isolation: Isolation{Reason: "隔离未知"}}
	}
	start := time.Now()
	_, err := planner.Plan(context.Background(), PlanInput{Provider: Codex, Project: editor.NewProject("工程"), Instruction: "添加标题"}, nil)
	if !errors.Is(err, ErrUnsupported) || time.Since(start) > time.Second {
		t.Fatalf("隔离未知不应启动子进程：%v", err)
	}
}

func TestInspectionCancellationPreservesContextError(t *testing.T) {
	planner := fixturePlanner(t, "sleep")
	planner.inspect = func(ctx context.Context, _ Provider, _, _ string) Installation {
		<-ctx.Done()
		return Installation{Installed: true}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := planner.Plan(ctx, PlanInput{Provider: Codex, Project: editor.NewProject("工程"), Instruction: "添加标题"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("检测阶段超时应可解释为上下文超时：%v", err)
	}
}

func TestOutputLimitAndTimeoutKillExecutableFixture(t *testing.T) {
	planner := fixturePlanner(t, "flood")
	planner.config.MaxOutputBytes = 1024
	_, err := planner.Plan(context.Background(), PlanInput{Provider: Codex, Project: editor.NewProject("工程"), Instruction: "添加标题"}, nil)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("没有限制输出：%v", err)
	}
	planner = fixturePlanner(t, "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = planner.Plan(ctx, PlanInput{Provider: Codex, Project: editor.NewProject("工程"), Instruction: "添加标题"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("超时未终止进程：%v", err)
	}
}

func TestPrerequisitesAndScopeDoNotFabricateEdits(t *testing.T) {
	project := editor.NewProject("工程")
	planner := fixturePlanner(t, "success")
	_, err := planner.Plan(context.Background(), PlanInput{Provider: Codex, Project: project, Instruction: "去除停顿", Workflow: "talking-head"}, nil)
	if !errors.Is(err, ErrPrerequisite) {
		t.Fatal("没有转写证据时不可伪造口播剪辑")
	}
	clip := editor.DefaultClip("existing", project.Tracks[1].ID, "text")
	clip.Text = "原文"
	clip.DurationFrames = 60
	project.Clips = []editor.Clip{clip}
	planner = fixturePlanner(t, "outside-selection")
	_, err = planner.Plan(context.Background(), PlanInput{Provider: Codex, Project: project, Selection: []string{clip.ID}, Instruction: "添加标题"}, nil)
	if err == nil {
		t.Fatal("不能把新片段加到选区之外")
	}
	project.Tracks[1].Locked = true
	request := editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "key", Label: "解锁", Commands: []editor.Command{{Type: "update_track", Track: &editor.Track{ID: project.Tracks[1].ID, Kind: "text", Name: "标题", Locked: false}}}}
	if ValidatePlan(project, request) == nil {
		t.Fatal("AI规划不能自行解除锁轨")
	}
}

func TestCommandRecipesHaveNoDangerousFallback(t *testing.T) {
	for _, provider := range []Provider{Codex, Claude} {
		schema, _ := planSchema(3, "plan-key")
		spec := buildCommand(provider, "/native/cli", "/private/job", "/private/job/schema.json", schema, "/private/profile", []byte("--dangerously-skip-permissions"))
		joined := strings.Join(spec.args, " ")
		if strings.Contains(joined, "dangerously") || strings.Contains(joined, "bypassPermissions") {
			t.Fatal("不应提供危险降级")
		}
		if provider == Codex && (!strings.Contains(joined, "--ignore-user-config") || !strings.Contains(joined, "read-only")) {
			t.Fatal("缺少Codex隔离参数")
		}
		if provider == Claude && (!strings.Contains(joined, "--safe-mode") || !strings.Contains(joined, "--strict-mcp-config")) {
			t.Fatal("缺少Claude隔离参数")
		}
		if !bytes.Equal(spec.input, []byte("--dangerously-skip-permissions")) {
			t.Fatal("用户文本必须走stdin，不得拼成CLI参数")
		}
	}
}

func TestSensitiveEnvironmentIsNotForwarded(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--require /tmp/inject.js")
	t.Setenv("CODEX_INTERNAL_REMOTE_MCP", "unsafe")
	t.Setenv("AIVIDCUT_SECRET_FIXTURE", "not-forwarded")
	environment := strings.Join(childEnvironment(Codex, "/profile"), "\n")
	for _, bad := range []string{"NODE_OPTIONS=", "CODEX_INTERNAL_REMOTE_MCP=", "AIVIDCUT_SECRET_FIXTURE="} {
		if strings.Contains(environment, bad) {
			t.Fatalf("透传了不需要的环境：%s", bad)
		}
	}
	if !strings.Contains(environment, "CODEX_HOME=/profile") {
		t.Fatal("未限定CLI认证状态作用域")
	}
	t.Setenv("CODEX_HOME", "/normal-codex-scope")
	t.Setenv("CLAUDE_CONFIG_DIR", "/normal-claude-scope")
	if !strings.Contains(strings.Join(childEnvironment(Codex, ""), "\n"), "CODEX_HOME=/normal-codex-scope") {
		t.Fatal("普通检测应使用用户明确配置的 CLI 作用域")
	}
	if strings.Contains(strings.Join(childEnvironment(Codex, "/isolated"), "\n"), "/normal-") {
		t.Fatal("独立作用域不能混入已有 CLI 状态目录")
	}
}

func TestIsolationChecksCanBecomeReadyAfterNormalScopeLogin(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("当前平台的进程边界仍待验证")
	}
	for _, test := range []struct{ name, mode, auth, expected string }{
		{"ready", "inspect-ready", "signed_in", "ready"},
		{"login", "inspect-ready", "signed_out", "needs_login"},
		{"hooks", "inspect-feature-enabled", "signed_in", "features_unverified"},
		{"mcp", "inspect-mcp", "signed_in", "mcp_unverified"},
		{"managed", "inspect-requirements", "signed_in", "policy_unverified"},
		{"missing-policy-field", "inspect-incomplete", "signed_in", "policy_unverified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := t.TempDir()
			if err := os.Chmod(profile, 0700); err != nil {
				t.Fatal(err)
			}
			path, _ := os.Executable()
			probe := func(args ...string) ([]byte, []byte, error) {
				return run(context.Background(), commandSpec{executable: path, args: args, environment: append(childEnvironment(Codex, profile), fixtureEnvironment+"="+test.mode)}, 16<<10, nil)
			}
			installation := Installation{Provider: Codex, Version: "0.164.0", AuthState: test.auth}
			verifyInstallationIsolation(context.Background(), &installation, profile, probe, func(context.Context, Provider) IsolationCheck {
				return IsolationCheck{ID: "local_management", Status: "pass", Reason: "fixture 未配置管理策略"}
			})
			if installation.Isolation.Status != test.expected || installation.Isolation.Supported != (test.expected == "ready") {
				t.Fatalf("错误的隔离状态：%+v", installation.Isolation)
			}
		})
	}
}

func TestIsolationUnknownPolicyAndForeignConfigFailClosed(t *testing.T) {
	profile := t.TempDir()
	if err := os.Chmod(profile, 0700); err != nil {
		t.Fatal(err)
	}
	info := Installation{Provider: Codex, Version: "0.164.0", AuthState: "signed_in"}
	probe := func(...string) ([]byte, []byte, error) {
		t.Fatal("前置条件失败不能继续运行CLI")
		return nil, nil, nil
	}
	policy := func(context.Context, Provider) IsolationCheck {
		return IsolationCheck{ID: "local_management", Status: "unknown", Reason: "策略未知"}
	}
	verifyInstallationIsolation(context.Background(), &info, profile, probe, policy)
	if info.Isolation.Supported {
		t.Fatal("未知管理条件不能变为可用")
	}
	if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte("# 用户既有配置"), 0600); err != nil {
		t.Fatal(err)
	}
	info.Isolation = Isolation{}
	verifyInstallationIsolation(context.Background(), &info, profile, probe, policy)
	if info.Isolation.Supported || info.Isolation.Status != "profile_required" {
		t.Fatal("不能忽略独立目录中的额外配置")
	}
	data, _ := os.ReadFile(filepath.Join(profile, "config.toml"))
	if string(data) != "# 用户既有配置" {
		t.Fatal("检测不得修改用户配置")
	}
}

func TestPlannerRequiresAbsoluteDedicatedScope(t *testing.T) {
	if _, err := NewPlanner(Config{StateDir: "relative"}); err == nil {
		t.Fatal("相对作用域可能漂移")
	}
	planner, _ := NewPlanner(Config{})
	_, err := planner.Plan(context.Background(), PlanInput{Provider: Codex, Project: editor.NewProject("工程"), Instruction: "添加标题"}, nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("不能从未配置的默认用户目录启动规划：%v", err)
	}
}

func TestWorkingDirectoryRejectsAncestorConfiguration(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".codex", "hooks.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	child, err := os.MkdirTemp(directory, "job-")
	if err != nil {
		t.Fatal(err)
	}
	if check := isolatedWorkingDirectory(child); check.Status == "pass" {
		t.Fatal("不能继承祖先目录中的项目hooks")
	}
}

func TestPlanCannotChangeLinkedGroupToEscapeScope(t *testing.T) {
	project := editor.NewProject("工程")
	clip := editor.DefaultClip("title", project.Tracks[1].ID, "text")
	clip.Text, clip.DurationFrames, clip.LinkGroup = "原文", 30, "original-link"
	project.Clips = []editor.Clip{clip}
	clip.LinkGroup = "other-link"
	request := editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "plan", Commands: []editor.Command{{Type: "update_clip", Clip: &clip}}}
	if ValidatePlan(project, request) == nil {
		t.Fatal("规划不得改写链接关系绕过选区或关联传播")
	}
	clip.LinkGroup = ""
	project.Clips[0].LinkGroup = ""
	request.Commands = []editor.Command{{Type: "delete_clip", ClipID: clip.ID}, {Type: "add_clip", Clip: &clip}}
	if ValidatePlan(project, request) == nil {
		t.Fatal("不能通过删除再新增复用已有片段身份")
	}
}

func TestAuthParsingDoesNotExposeAccountOrKey(t *testing.T) {
	state, reason := parseAuth(Codex, nil, []byte("Logged in using API key sk-private-SECRET"), nil)
	if state != "signed_in" || strings.Contains(reason, "SECRET") {
		t.Fatal("认证诊断暴露密钥")
	}
	state, reason = parseAuth(Claude, []byte(`{"loggedIn":false,"authMethod":"none","email":"private@example.com"}`), nil, nil)
	if state != "signed_out" || strings.Contains(reason, "example.com") {
		t.Fatal("认证状态不正确或暴露账户信息")
	}
}

func TestStrictJSONRejectsTrailingAndUnknownData(t *testing.T) {
	for _, data := range []string{`{"commands":[]} {"extra":true}`, "```json\n{}\n```", `{"commands":[],"execute":"shell"}`} {
		if _, err := ParseEditRequest([]byte(data), 1024); err == nil {
			t.Fatal("非结构化或多余内容不应被接受")
		}
	}
}

func TestPromptOmitsSourcePathsAndURLs(t *testing.T) {
	project := editor.NewProject("工程")
	project.Assets = []editor.Asset{{ID: "asset", Name: "素材", Kind: "image", Path: "/private/account-secret.png", URL: "https://private.example/token", ThumbnailURL: "private-thumb", Fingerprint: "private-hash", Width: 100, Height: 100}}
	input := PlanInput{Project: project, Instruction: "添加标题"}
	workflow, _ := workflowFor(input)
	data, err := buildPrompt(input, workflow, "key")
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"account-secret", "private.example", "private-thumb", "private-hash"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("上下文泄露不必要信息：%s", secret)
		}
	}
}
