//go:build !windows

package playback

func workerEnvironment(path string) ([]string, error) { return []string{"PATH=/usr/bin:/bin"}, nil }
