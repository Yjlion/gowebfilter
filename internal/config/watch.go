package config

import (
	"context"
	"log/slog"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchDir watches dir for any create/write/remove/rename event and invokes
// onChange after debouncing bursts of events (e.g. an editor's
// write-then-rename save, or several policy files changing together during
// a backup restore) into a single reload - the Go equivalent of the Python
// original's watchfiles-based hot-reload. Runs until ctx is cancelled. If
// the watcher can't be created (e.g. missing dir on some platforms),
// hot-reload is silently disabled, matching the Python original's
// fail-open behavior when watchfiles is unavailable.
func WatchDir(ctx context.Context, dir string, debounce time.Duration, onChange func()) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("hot-reload disabled: could not create watcher", "dir", dir, "err", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(dir); err != nil {
		slog.Warn("hot-reload disabled: could not watch dir", "dir", dir, "err", err)
		return
	}

	var timer *time.Timer
	reload := make(chan struct{}, 1)

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-watcher.Events:
			if !ok {
				return
			}
			if timer == nil {
				timer = time.AfterFunc(debounce, func() {
					select {
					case reload <- struct{}{}:
					default:
					}
				})
			} else {
				timer.Reset(debounce)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("watcher error", "dir", dir, "err", err)
		case <-reload:
			onChange()
		}
	}
}
