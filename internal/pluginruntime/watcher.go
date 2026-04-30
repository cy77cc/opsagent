package pluginruntime

import (
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watcherState manages debounced file system events.
type watcherState struct {
	debounce time.Duration
	timers   map[string]*time.Timer
}

// debounceEvent fires fn after the debounce period for the given key.
// If debounceEvent is called again with the same key before the timer fires,
// the previous timer is cancelled and a new one starts.
func (w *watcherState) debounceEvent(key string, fn func()) {
	if timer, ok := w.timers[key]; ok {
		timer.Stop()
	}
	w.timers[key] = time.AfterFunc(w.debounce, fn)
}

// startWatcher begins watching the plugins directory for changes.
// It watches for create, write, and remove events on plugin.yaml files
// and triggers reload or unload via debounced callbacks.
func (g *Gateway) startWatcher() error {
	if g.cfg.PluginsDir == "" {
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(g.cfg.PluginsDir); err != nil {
		watcher.Close()
		return err
	}

	ws := &watcherState{
		debounce: g.cfg.FileWatchDebounce,
		timers:   make(map[string]*time.Timer),
	}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer watcher.Close()

		for {
			select {
			case <-g.ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				g.handleFsEvent(event, ws)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				g.logger.Error().Err(err).Msg("file watcher error")
			}
		}
	}()

	return nil
}

// handleFsEvent processes a file system event.
// Only plugin.yaml files trigger reload or unload actions.
func (g *Gateway) handleFsEvent(event fsnotify.Event, ws *watcherState) {
	if !isPluginManifest(event.Name) {
		return
	}

	pluginName := extractPluginName(g.cfg.PluginsDir, event.Name)
	if pluginName == "" {
		return
	}

	switch {
	case event.Op&(fsnotify.Create|fsnotify.Write) != 0:
		ws.debounceEvent(pluginName, func() {
			g.logger.Info().Str("plugin", pluginName).Msg("plugin manifest changed, reloading")
			if err := g.ReloadPlugin(pluginName); err != nil {
				g.logger.Error().Err(err).Str("plugin", pluginName).Msg("failed to reload plugin")
			}
		})
	case event.Op&fsnotify.Remove != 0:
		ws.debounceEvent(pluginName, func() {
			g.logger.Info().Str("plugin", pluginName).Msg("plugin manifest removed, unloading")
			g.mu.Lock()
			if p, ok := g.plugins[pluginName]; ok {
				g.stopPlugin(pluginName, p)
				delete(g.plugins, pluginName)
			}
			g.mu.Unlock()
		})
	}
}

// isPluginManifest checks if the path is a plugin.yaml file.
func isPluginManifest(path string) bool {
	return filepath.Base(path) == "plugin.yaml"
}

// extractPluginName extracts the plugin name from a manifest path relative
// to the plugins root directory.
//
// A valid plugin manifest lives at <pluginsDir>/<pluginName>/plugin.yaml.
// If the manifest sits directly in pluginsDir (e.g. <pluginsDir>/plugin.yaml),
// there is no plugin subdirectory, so "" is returned.
//
// Examples (with pluginsDir="/etc/opsagent/plugins"):
//   - /etc/opsagent/plugins/my-plugin/plugin.yaml -> "my-plugin"
//   - /etc/opsagent/plugins/plugin.yaml -> ""
func extractPluginName(pluginsDir, manifestPath string) string {
	rel, err := filepath.Rel(pluginsDir, manifestPath)
	if err != nil {
		return ""
	}

	// If the relative path escapes the plugins directory (starts with ".."),
	// the manifest is not inside a plugin subdirectory.
	if len(rel) >= 2 && rel[:2] == ".." {
		return ""
	}

	// rel is "my-plugin/plugin.yaml" for a valid plugin manifest,
	// or "plugin.yaml" for a root-level manifest.
	dir := filepath.Dir(rel)

	// If the manifest sits directly in the plugins root, dir will be "."
	// and there is no plugin name.
	if dir == "." {
		return ""
	}

	return filepath.Base(dir)
}
