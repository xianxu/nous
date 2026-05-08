package brainsync

import (
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// RefWatcher emits the brain path on every change to its
// .git/refs/heads/main file, which corresponds to a local commit (or a
// fetch updating the local ref). Consumers verify whether there's
// something local-only to push before acting.
//
// One RefWatcher serves multiple brains; events identify which brain
// changed by absolute path.
type RefWatcher struct {
	fs   *fsnotify.Watcher
	out  chan string
	stop chan struct{}
	done chan struct{}
	// pathToBrain maps the watched ref-file path back to its containing brain.
	pathToBrain map[string]string
}

// NewRefWatcher watches each brain's .git/refs/heads/ directory and
// emits the brain path when refs/heads/main changes (or is created).
//
// We watch the directory rather than the file directly because the file
// doesn't exist until the first commit; fsnotify can't watch a missing path.
func NewRefWatcher(brains []string) (*RefWatcher, error) {
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &RefWatcher{
		fs:          fs,
		out:         make(chan string, 16),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		pathToBrain: make(map[string]string),
	}
	for _, b := range brains {
		dir := filepath.Join(b, ".git", "refs", "heads")
		if _, err := os.Stat(dir); err != nil {
			log.Printf("brainsync: skipping %s (no %s — not a git repo yet?)", b, dir)
			continue
		}
		if err := fs.Add(dir); err != nil {
			fs.Close()
			return nil, err
		}
		w.pathToBrain[dir] = b
	}
	go w.loop()
	return w, nil
}

func (w *RefWatcher) loop() {
	defer close(w.done)
	for {
		select {
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			// Filter to refs/heads/main writes/creates only.
			if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if filepath.Base(ev.Name) != "main" {
				continue
			}
			dir := filepath.Dir(ev.Name)
			if brain, ok := w.pathToBrain[dir]; ok {
				select {
				case w.out <- brain:
				case <-w.stop:
					return
				}
			}
		case <-w.stop:
			return
		}
	}
}

// Events returns a channel of brain paths whose ref changed.
func (w *RefWatcher) Events() <-chan string { return w.out }

// Close stops the watcher and releases resources.
func (w *RefWatcher) Close() {
	close(w.stop)
	<-w.done
	w.fs.Close()
	close(w.out)
}
