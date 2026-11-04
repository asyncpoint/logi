package input

import (
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// Watcher turns filesystem notifications into coalesced wake-up hints. An
// event never describes what changed; it only means "inspect the file". Multiple events collapse into a single pending wake-up.
type Watcher struct {
	fw   *fsnotify.Watcher
	wake chan struct{}
	done chan struct{}
}

// NewWatcher watches the directory containing path so that rename/create
// (rotation) events are observed, not just writes to one inode.
func NewWatcher(path string) (*Watcher, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(filepath.Dir(abs)); err != nil {
		fw.Close()
		return nil, err
	}
	w := &Watcher{fw: fw, wake: make(chan struct{}, 1), done: make(chan struct{})}
	base := filepath.Base(abs)
	go func() {
		defer close(w.done)
		for {
			select {
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				if strings.EqualFold(filepath.Base(ev.Name), base) {
					w.poke()
				}
			case _, ok := <-fw.Errors:
				if !ok {
					return
				}
				w.poke() // a watcher error is also a reason to look at the file
			}
		}
	}()
	return w, nil
}

func (w *Watcher) poke() {
	select {
	case w.wake <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// Wake is signalled when the file may have changed.
func (w *Watcher) Wake() <-chan struct{} { return w.wake }

// Close stops the watcher and its goroutine.
func (w *Watcher) Close() {
	w.fw.Close()
	<-w.done
}
