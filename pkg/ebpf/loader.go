package ebpf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
)

type Loader struct {
	objects  map[string]*ebpf.Collection
	ringBufs map[string]*ringbuf.Reader
	mu       sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
}

type EventHandler func(data []byte) error

func New() *Loader {
	ctx, cancel := context.WithCancel(context.Background())
	return &Loader{
		objects:  make(map[string]*ebpf.Collection),
		ringBufs: make(map[string]*ringbuf.Reader),
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (l *Loader) Close() error {
	l.cancel()
	l.mu.Lock()
	defer l.mu.Unlock()

	var errs []error
	for _, rb := range l.ringBufs {
		if err := rb.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	for _, obj := range l.objects {
		obj.Close()
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing ebpf: %v", errs)
	}
	return nil
}

func (l *Loader) LoadBPF(bpfDir, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	objPath := filepath.Join(bpfDir, name+".o")
	if _, err := os.Stat(objPath); os.IsNotExist(err) {
		return fmt.Errorf("bpf object not found: %s", objPath)
	}

	spec, err := ebpf.LoadCollectionSpec(objPath)
	if err != nil {
		return fmt.Errorf("loading spec %s: %w", objPath, err)
	}

	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return fmt.Errorf("creating collection %s: %w", name, err)
	}

	l.objects[name] = coll
	return nil
}

func (l *Loader) OpenRingBuf(mapName string) (*ringbuf.Reader, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, coll := range l.objects {
		m := coll.Maps[mapName]
		if m == nil {
			continue
		}

		rb, err := ringbuf.NewReader(m)
		if err != nil {
			return nil, fmt.Errorf("opening ringbuf %s: %w", mapName, err)
		}

		l.ringBufs[mapName] = rb
		return rb, nil
	}

	return nil, fmt.Errorf("map %s not found", mapName)
}

func (l *Loader) StartRingBufHandler(mapName string, handler EventHandler) error {
	rb, err := l.OpenRingBuf(mapName)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-l.ctx.Done():
				return
			default:
				record, err := rb.Read()
				if err != nil {
					if l.ctx.Err() != nil {
						return
					}
					continue
				}
				if err := handler(record.RawSample); err != nil {
					fmt.Fprintf(os.Stderr, "handler error: %v\n", err)
				}
			}
		}
	}()

	return nil
}

func EnsurePermissions() error {
	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("removing memlock: %w", err)
	}
	return nil
}
