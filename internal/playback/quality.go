package playback

import (
	"errors"
	"math"

	"aividcut/internal/editor"
)

// QualityScale仅改变预览合成，不能写回工程或改变时间/样本数据。
func QualityScale(quality string) (float64, error) {
	switch quality {
	case "", "full":
		return 1, nil
	case "half":
		return .5, nil
	case "quarter":
		return .25, nil
	default:
		return 0, errors.New("预览精度仅支持全、半或四分之一")
	}
}
func renderProject(project editor.Project, scale float64) (editor.Project, error) {
	p := editor.CloneProject(project)
	p.Settings.Width = int(math.Round(float64(p.Settings.Width) * scale))
	p.Settings.Height = int(math.Round(float64(p.Settings.Height) * scale))
	if p.Settings.Width < 16 || p.Settings.Height < 16 {
		return editor.Project{}, errors.New("当前画幅在此预览精度下小于16像素，请提高精度")
	}
	for i := range p.Clips {
		p.Clips[i].Transform.X *= scale
		p.Clips[i].Transform.Y *= scale
	}
	return p, nil
}

func scaledTextFontPixels(clip editor.Clip, scale float64) (int, error) {
	pixels, err := textFontPixels(clip)
	if err != nil {
		return 0, err
	}
	return max(1, int(math.Round(float64(pixels)*scale))), nil
}
