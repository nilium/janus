package main

import (
	"sync"
	"time"
)

type Signal <-chan struct{}

func (s Signal) Wait()                                { <-s }
func (s Signal) AfterFunc(fn func())                  { go func() { s.Wait(); fn() }() }
func (s Signal) DelayFunc(d time.Duration, fn func()) { go func() { s.Wait(); time.Sleep(d); fn() }() }

type BroadcastFunc func()

func (fn BroadcastFunc) Close() error { fn(); return nil }

func mksignal() (Signal, BroadcastFunc) {
	var (
		sig  = make(chan struct{})
		once sync.Once
	)
	return sig, func() { once.Do(func() { close(sig) }) }
}
