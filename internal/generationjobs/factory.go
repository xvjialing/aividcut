package generationjobs

import (
	"errors"

	"aividcut/internal/generation"
)

func BuiltinProvider(provider, model string, secrets generation.SecretSource, references generation.ReferenceSource) (generation.Provider, error) {
	cfg := generation.Config{ModelID: model, Secrets: secrets, References: references}
	switch provider {
	case generation.OpenAI:
		return generation.NewOpenAI(cfg)
	case generation.Google:
		return generation.NewGemini(cfg)
	case generation.MiniMax:
		return generation.NewMiniMax(cfg)
	case generation.Seedance:
		return generation.NewSeedance(cfg)
	default:
		return nil, errors.New("未声明的生成服务")
	}
}

// 目录仅列出适配器逐项声明的模型，不把同家族的任意模型当作兼容。
func Catalogue() ([]generation.Capability, error) {
	models := [][2]string{
		{generation.OpenAI, "gpt-image-2.5-sunburst"}, {generation.OpenAI, "gpt-image-2.5-flare"},
		{generation.OpenAI, "gpt-image-2.5-sunburst-2026-09-08"}, {generation.OpenAI, "gpt-image-2.5-flare-2026-09-08"},
		{generation.Google, "gemini-2.5-flash-image"},
		{generation.MiniMax, "MiniMax-H3"}, {generation.MiniMax, "MiniMax-H3-Highspeed"},
		{generation.MiniMax, "MiniMax-H3-Lite"}, {generation.MiniMax, "MiniMax-H3-Max"},
		{generation.Seedance, generation.Seedance25Model},
	}
	result := []generation.Capability{}
	for _, pair := range models {
		provider, err := BuiltinProvider(pair[0], pair[1], nil, nil)
		if err != nil {
			return nil, err
		}
		result = append(result, provider.Capabilities()...)
	}
	return result, nil
}
