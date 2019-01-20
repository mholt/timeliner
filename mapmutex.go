package timeliner

import "sync"

// Modified from https://medium.com/@petrlozhkin/kmutex-lock-mutex-by-unique-id-408467659c24

type mapMutex struct {
	cond *sync.Cond
	set  map[interface{}]struct{}
}

func newMapMutex() *mapMutex {
	return &mapMutex{
		cond: sync.NewCond(new(sync.Mutex)),
		set:  make(map[interface{}]struct{}),
	}
}

func (mmu *mapMutex) Lock(key interface{}) {
	mmu.cond.L.Lock()
	defer mmu.cond.L.Unlock()
	for mmu.locked(key) {
		mmu.cond.Wait()
	}
	mmu.set[key] = struct{}{}
	return
}

func (mmu *mapMutex) Unlock(key interface{}) {
	mmu.cond.L.Lock()
	defer mmu.cond.L.Unlock()
	delete(mmu.set, key)
	mmu.cond.Broadcast()
}

func (mmu *mapMutex) locked(key interface{}) (ok bool) {
	_, ok = mmu.set[key]
	return
}
