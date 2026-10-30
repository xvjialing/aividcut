package application

import (
	"aividcut/internal/media"
	"aividcut/internal/mediaruntime"
	"aividcut/internal/mediatools"
	"context"
	"runtime"
)

type runtimeChoice struct {
	data    string
	paths   mediatools.Config
	origins map[string]string
	stamp   string
	warning string
	tools   media.Toolchain
}

func selectMediaRuntime(ctx context.Context, settings mediatools.Config, executable string, getenv func(string) string) runtimeChoice {
	choice := runtimeChoice{data: getenv("AIVIDCUT_MLT_DATA")}
	base := settings
	base.UseBundled = false
	allowPATH := true
	if settings.UseBundled {
		base = mediatools.Config{}
		bundle, err := mediaruntime.Find(ctx, executable, runtime.GOOS, runtime.GOARCH)
		choice.stamp = "absent:" + bundle.ManifestPath
		if err != nil {
			allowPATH = false
			choice.stamp = "invalid:" + bundle.ManifestPath
			choice.warning = "随包媒体核对失败，自动媒体已停用，可明确选择手动配置：" + err.Error()
		} else if bundle.Present {
			choice.data = bundle.Paths.Data
			choice.stamp = bundle.Fingerprint
			base = mediatools.Config{FFmpeg: bundle.Paths.FFmpeg, FFprobe: bundle.Paths.FFprobe, Worker: bundle.Paths.Worker, Repository: bundle.Paths.Repository, Profiles: bundle.Paths.Profiles}
		}
		if getenv("AIVIDCUT_MLT_WORKER") != "" || getenv("AIVIDCUT_MLT_REPOSITORY") != "" || getenv("AIVIDCUT_MLT_PROFILES") != "" || getenv("AIVIDCUT_MLT_DATA") != "" {
			base.Worker, base.Repository, base.Profiles = "", "", ""
			choice.data = getenv("AIVIDCUT_MLT_DATA")
		}
	}
	choice.paths, choice.origins = mediatools.Requested(base, getenv)
	if settings.UseBundled {
		for key, origin := range choice.origins {
			if origin == "saved" {
				choice.origins[key] = "bundled"
			}
			if !allowPATH && (origin == "path" || origin == "unset") {
				choice.origins[key] = "bundled_invalid"
			}
		}
	}
	if allowPATH {
		choice.tools = media.DiscoverConfigured(choice.paths.FFmpeg, choice.paths.FFprobe)
	} else {
		choice.tools = media.DiscoverExplicit(choice.paths.FFmpeg, choice.paths.FFprobe)
	}
	return choice
}
