package workflows

import "embed"

//go:embed builtin/*/SKILL.md
var builtinSkills embed.FS

// BuiltinSkill 只读取包内登记的方法说明，不访问或安装用户CLI配置。
func BuiltinSkill(id string) (string, error) {
	if _, err := definition(id); err != nil {
		return "", err
	}
	data, err := builtinSkills.ReadFile("builtin/" + id + "/SKILL.md")
	return string(data), err
}

// SourceFrameSkill 是画面输入的方法补充，不伪装成确定性编辑工作流。
func SourceFrameSkill() string {
	data, _ := builtinSkills.ReadFile("builtin/source-frames/SKILL.md")
	return string(data)
}
